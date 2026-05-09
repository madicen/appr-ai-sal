package review

import (
	"context"
	"errors"
	"fmt"
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

// APIHTTPError is a non-2xx response from an HTTP-based AI backend.
type APIHTTPError struct {
	Provider string
	Status   int
	Body     string
	// RetryAfter is the greater of HTTP Retry-After (when present) and any
	// embedded hint in the body (e.g. Gemini 429: "Please retry in 16.497428316s").
	RetryAfter time.Duration
}

func (e *APIHTTPError) Error() string {
	if e == nil {
		return "<nil>"
	}
	body := e.Body
	if len(body) > 800 {
		body = body[:800] + "…"
	}
	s := fmt.Sprintf("%s: HTTP %03d: %s", e.Provider, e.Status, body)
	if e.RetryAfter > 0 {
		s += fmt.Sprintf(" (retry-after %s)", e.RetryAfter.Round(time.Millisecond))
	}
	return s
}

// Retryable reports whether the HTTP status often resolves with a backoff retry.
func (e *APIHTTPError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Status {
	case 408, 425, 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

// IsRetryableCompleteError returns true for timeouts, rate limits, and common transient failures.
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
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"),
		strings.Contains(msg, "timed out"),
		strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "eof"),
		strings.Contains(msg, "broken pipe"):
		return true
	case strings.Contains(msg, "429"),
		strings.Contains(msg, "rate limit"),
		strings.Contains(msg, "too many requests"),
		strings.Contains(msg, "resource exhausted"),
		strings.Contains(msg, "overloaded"),
		strings.Contains(msg, "503"),
		strings.Contains(msg, "502"),
		strings.Contains(msg, "504"),
		strings.Contains(msg, "quota"),
		strings.Contains(msg, "insufficient_quota"),
		strings.Contains(msg, "rate_limit"),
		strings.Contains(msg, "requests per minute"):
		return true
	default:
		return false
	}
}

// isQuotaOrRateLimitError reports errors where a longer backoff helps (429,
// quota messages, overloaded APIs). Used to raise minimum wait on retries.
func isQuotaOrRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	var he *APIHTTPError
	if errors.As(err, &he) && he != nil {
		if he.Status == 429 || he.Status == 529 {
			return true
		}
		if he.Retryable() {
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
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "quota") ||
		strings.Contains(msg, "insufficient_quota") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "resource exhausted") ||
		strings.Contains(msg, "429")
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

func sleepBeforeInferenceRetry(ctx context.Context, backoffIndex int, base, maxWait time.Duration, lastErr error) error {
	return sleepInterruptible(ctx, retrySleepDuration(backoffIndex, base, maxWait, lastErr))
}

func completeWithRetry(ctx context.Context, cfg *aiconfig.Config, fn func(context.Context) (string, error)) (string, error) {
	max := cfg.InferenceRetryMaxAttempts()
	base := cfg.InferenceRetryBase()
	maxWait := cfg.InferenceRetryMaxBackoff()

	var lastErr error
	for attempt := 0; attempt < max; attempt++ {
		if attempt > 0 {
			if err := sleepBeforeInferenceRetry(ctx, attempt-1, base, maxWait, lastErr); err != nil {
				return "", err
			}
		}
		out, err := fn(ctx)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !IsRetryableCompleteError(err) {
			return "", err
		}
		if attempt == max-1 {
			break
		}
	}
	return "", fmt.Errorf("AI inference failed after %d attempts: %w", max, lastErr)
}

// stageWithRetry retries any stage that returns a transiently-failing error
// (timeouts, parse failures, transient HTTP). Stages that succeed or return a
// non-retryable error exit immediately. This is broader than completeWithRetry
// (which only retries inside one Complete call) and complements it: if a
// specialist's JSON response is unparseable on the first attempt, the whole
// stage runs again rather than failing the review.
//
// notify, if non-nil, is called on each retry with the attempt number (1-based)
// and the error that triggered it, so the UI can surface progress.
func stageWithRetry(ctx context.Context, cfg *aiconfig.Config, name string, notify func(attempt int, err error), fn func(context.Context) error) error {
	max := cfg.InferenceRetryMaxAttempts()
	if max < 1 {
		max = 1
	}
	base := cfg.InferenceRetryBase()
	maxWait := cfg.InferenceRetryMaxBackoff()

	var lastErr error
	for attempt := 0; attempt < max; attempt++ {
		if attempt > 0 {
			if notify != nil {
				notify(attempt, lastErr)
			}
			if err := sleepBeforeInferenceRetry(ctx, attempt-1, base, maxWait, lastErr); err != nil {
				return err
			}
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableStageError(err) {
			return err
		}
		if attempt == max-1 {
			break
		}
	}
	return fmt.Errorf("%s failed after %d attempts: %w", name, max, lastErr)
}

// isRetryableStageError accepts the IsRetryableCompleteError set plus errors
// that bubble up through specialist parsing (e.g. "parse specialist output:
// invalid character"), since a transient model glitch can produce malformed
// JSON that retrying often clears.
func isRetryableStageError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if IsRetryableCompleteError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "parse specialist output"),
		strings.Contains(msg, "parse vibe-coach output"),
		strings.Contains(msg, "parse repo arbiter"),
		strings.Contains(msg, "parse repo expert narrative"),
		strings.Contains(msg, "no json object found"),
		strings.Contains(msg, "unexpected end of json"),
		strings.Contains(msg, "invalid character"):
		return true
	}
	return false
}
