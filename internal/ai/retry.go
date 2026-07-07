package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// IsRetryableCompleteError returns true for timeouts, rate limits, and common
// transient failures. Classification is TYPE-BASED: it inspects error types
// (context errors, *APIHTTPError, *ClaudeExecError, net.Error, url.Error, and
// sentinel syscall/io errors via errors.Is) rather than scanning the whole
// error message for magic substrings like "eof" or "429". The old
// substring-anywhere scan produced false positives (e.g. any message
// containing "eof", such as a JS "beforeEach"-adjacent stack, was treated as
// retryable); the typed Claude error (parsed once from the subprocess exit +
// stderr) and the existing HTTP APIHTTPError taxonomy replace it.
func IsRetryableCompleteError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Claude subprocess: retry only transient classes (rate-limit / network).
	var ce *ClaudeExecError
	if errors.As(err, &ce) {
		return ce.Retryable()
	}
	// HTTP providers: the APIHTTPError taxonomy owns 429/5xx/Retry-After.
	var he *APIHTTPError
	if errors.As(err, &he) && he.Retryable() {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return true
	}
	// Sentinel transport failures, matched by type/identity (not substring):
	// connection reset/refused/broken pipe/timed-out and stream truncation.
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}

// isQuotaOrRateLimitError reports errors where a longer backoff helps (429,
// quota messages, overloaded APIs). Used to raise the minimum wait on retries.
// Type-based: it keys off *APIHTTPError (status + provider error body) and the
// typed *ClaudeExecError class rather than scanning the whole error string.
func isQuotaOrRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	var ce *ClaudeExecError
	if errors.As(err, &ce) && ce != nil && ce.Class == ClaudeClassRateLimited {
		return true
	}
	var he *APIHTTPError
	if errors.As(err, &he) && he != nil {
		if he.Status == 429 || he.Status == 529 {
			return true
		}
		if he.Retryable() {
			// Inspecting the provider's own error BODY (a bounded, structured
			// field) is fine; this is not a scan of the whole wrapped message.
			low := strings.ToLower(he.Body)
			if strings.Contains(low, "quota") ||
				strings.Contains(low, "rate_limit") ||
				strings.Contains(low, "rate limit") ||
				strings.Contains(low, "too many requests") ||
				strings.Contains(low, "resource exhausted") ||
				strings.Contains(low, "overloaded") ||
				strings.Contains(low, "requests per") {
				return true
			}
		}
	}
	return false
}

// parseRetryAfterHeader interprets Retry-After as delta-seconds or HTTP-date.
func parseRetryAfterHeader(h http.Header) time.Duration {
	s := strings.TrimSpace(h.Get("Retry-After"))
	if s == "" {
		return 0
	}
	if sec, err := strconv.Atoi(s); err == nil && sec >= 0 {
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(s); err == nil {
		until := time.Until(t)
		if until > 0 {
			return until
		}
	}
	return 0
}

// retryInMessageRE matches embedded hints such as Gemini's:
// "Please retry in 16.497428316s." inside JSON error.message.
var retryInMessageRE = regexp.MustCompile(`(?i)retry\s+in\s+([0-9]+(?:\.[0-9]+)?)\s*s`)

// parseRetryHintFromErrorBody extracts a recommended wait from error JSON or text
// when HTTP Retry-After is missing (common for Gemini 429 RESOURCE_EXHAUSTED).
func parseRetryHintFromErrorBody(body string) time.Duration {
	m := retryInMessageRE.FindStringSubmatch(body)
	if len(m) < 2 {
		return 0
	}
	sec, err := strconv.ParseFloat(m[1], 64)
	if err != nil || sec <= 0 || sec > 86400 {
		return 0
	}
	return time.Duration(sec * float64(time.Second))
}

// httpRetryAfter merges Retry-After headers with provider-specific hints in the body.
func httpRetryAfter(resp *http.Response, body []byte) time.Duration {
	if resp == nil {
		return 0
	}
	header := parseRetryAfterHeader(resp.Header)
	hint := parseRetryHintFromErrorBody(string(body))
	if hint > header {
		return hint
	}
	return header
}

func sleepInterruptible(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func backoffDelay(retryIndex int, base, max time.Duration) time.Duration {
	if retryIndex < 0 {
		retryIndex = 0
	}
	d := base
	for i := 0; i < retryIndex; i++ {
		next := d * 2
		if next < d {
			return max
		}
		d = next
		if d > max {
			return max
		}
	}
	return d
}

// jitterBackoffDelay applies multiplicative jitter in [0.75, 1.0] × d so parallel
// retries do not align on the same instant (reduces thundering herds against quotas).
func jitterBackoffDelay(d, max time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	lo := float64(d) * 0.75
	hi := float64(d)
	if hi <= lo {
		out := time.Duration(hi)
		if out > max {
			return max
		}
		return out
	}
	x := lo + rand.Float64()*(hi-lo)
	out := time.Duration(x)
	if out > max {
		return max
	}
	return out
}

// retrySleepDuration combines exponential backoff (2^n × base, capped), optional
// Retry-After from the API, higher floors for quota/rate-limit errors, and jitter.
func retrySleepDuration(backoffIndex int, base, maxWait time.Duration, lastErr error) time.Duration {
	d := backoffDelay(backoffIndex, base, maxWait)

	var he *APIHTTPError
	var serverHint time.Duration
	if errors.As(lastErr, &he) && he != nil && he.RetryAfter > 0 {
		serverHint = he.RetryAfter
		if he.RetryAfter > d {
			d = he.RetryAfter
		}
	}

	const minThrottle = 4 * time.Second
	if isQuotaOrRateLimitError(lastErr) && d < minThrottle {
		d = minThrottle
	}

	if d > maxWait {
		d = maxWait
	}
	out := jitterBackoffDelay(d, maxWait)
	// Do not wait less than an explicit server/body hint (e.g. Gemini "retry in 16s").
	if serverHint > 0 && out < serverHint && serverHint <= maxWait {
		out = serverHint
	}
	if out > maxWait {
		out = maxWait
	}
	return out
}

// SleepBeforeRetry sleeps for the computed backoff before the next attempt,
// returning early with the context error if the context is cancelled. It is
// exported so review.stageWithRetry (which shares the same backoff policy) can
// reuse it without re-implementing the schedule.
func SleepBeforeRetry(ctx context.Context, backoffIndex int, base, maxWait time.Duration, lastErr error) error {
	return sleepInterruptible(ctx, retrySleepDuration(backoffIndex, base, maxWait, lastErr))
}

// completeWithRetry returns the inference result plus the number of retries
// performed (0 on first-try success) so the caller can log retry telemetry.
func completeWithRetry(ctx context.Context, cfg *aiconfig.Config, fn func(context.Context) (Result, error)) (Result, int, error) {
	max := cfg.InferenceRetryMaxAttempts()
	base := cfg.InferenceRetryBase()
	maxWait := cfg.InferenceRetryMaxBackoff()
	// R4: draw every provider call from the stage's shared attempt budget (when
	// one is installed) so this inner loop and the stage-level retry loop can't
	// multiply. Each call — including the first — claims one unit; when the pool
	// is empty we stop even if this loop's own max hasn't been reached.
	budget := AttemptBudgetFromContext(ctx)

	var lastErr error
	attempt := 0
	for ; attempt < max; attempt++ {
		if attempt > 0 {
			if err := SleepBeforeRetry(ctx, attempt-1, base, maxWait, lastErr); err != nil {
				return Result{}, attempt, err
			}
		}
		if !budget.tryConsume() {
			// Shared budget exhausted (a prior sibling call in this stage used
			// it up). Surface a stable error so the stage loop stops too.
			if lastErr == nil {
				lastErr = errors.New("shared attempt budget exhausted")
			}
			return Result{}, attempt, fmt.Errorf("AI inference stopped after %d attempt(s): %w", attempt, lastErr)
		}
		out, err := fn(ctx)
		if err == nil {
			return out, attempt, nil
		}
		lastErr = err
		if !IsRetryableCompleteError(err) {
			return Result{}, attempt, err
		}
		if attempt == max-1 {
			break
		}
	}
	return Result{}, attempt, fmt.Errorf("AI inference failed after %d attempts: %w", attempt+1, lastErr)
}
