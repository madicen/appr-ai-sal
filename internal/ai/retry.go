package ai

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

	var lastErr error
	for attempt := 0; attempt < max; attempt++ {
		if attempt > 0 {
			if err := SleepBeforeRetry(ctx, attempt-1, base, maxWait, lastErr); err != nil {
				return Result{}, attempt, err
			}
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
	return Result{}, max - 1, fmt.Errorf("AI inference failed after %d attempts: %w", max, lastErr)
}
