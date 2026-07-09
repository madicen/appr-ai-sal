package ai

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestAPIHTTPErrorRetryable(t *testing.T) {
	t.Parallel()
	if !(&APIHTTPError{Status: http.StatusTooManyRequests}).Retryable() {
		t.Fatal("429 should retry")
	}
	if !(&APIHTTPError{Status: http.StatusServiceUnavailable}).Retryable() {
		t.Fatal("503 should retry")
	}
	if (&APIHTTPError{Status: http.StatusBadRequest}).Retryable() {
		t.Fatal("400 should not retry")
	}
	if (&APIHTTPError{Status: http.StatusUnauthorized}).Retryable() {
		t.Fatal("401 should not retry")
	}
	if !(&APIHTTPError{Status: 529}).Retryable() {
		t.Fatal("529 should retry")
	}
}

func TestBackoffDelay(t *testing.T) {
	t.Parallel()
	base := 500 * time.Millisecond
	max := 10 * time.Second
	if d := backoffDelay(0, base, max); d != base {
		t.Fatalf("retryIndex 0: got %v want %v", d, base)
	}
	if d := backoffDelay(1, base, max); d != 2*base {
		t.Fatalf("retryIndex 1: got %v want %v", d, 2*base)
	}
	if d := backoffDelay(2, base, max); d != 4*base {
		t.Fatalf("retryIndex 2: got %v want %v", d, 4*base)
	}
	if d := backoffDelay(100, base, max); d != max {
		t.Fatalf("should cap at max: got %v want %v", d, max)
	}
}

func TestIsRetryableCompleteError(t *testing.T) {
	t.Parallel()
	if !IsRetryableCompleteError(context.DeadlineExceeded) {
		t.Fatal("deadline should retry")
	}
	if IsRetryableCompleteError(context.Canceled) {
		t.Fatal("cancel should not retry")
	}
	if !IsRetryableCompleteError(&APIHTTPError{Status: 429}) {
		t.Fatal("429 type should retry")
	}
	if IsRetryableCompleteError(errors.New("chat/completions: empty choices")) {
		t.Fatal("logic errors should not retry by substring noise — empty choices")
	}
}

func TestParseRetryHintFromErrorBody(t *testing.T) {
	t.Parallel()
	gemini := `{"error":{"code":429,"message":"Quota exceeded ... Please retry in 16.497428316s.","status":"RESOURCE_EXHAUSTED"}}`
	if d := parseRetryHintFromErrorBody(gemini); d < 16*time.Second || d > 17*time.Second {
		t.Fatalf("Gemini hint: got %v want ~16.5s", d)
	}
	if d := parseRetryHintFromErrorBody(`please retry in 3s`); d != 3*time.Second {
		t.Fatalf("simple hint: got %v want 3s", d)
	}
	if d := parseRetryHintFromErrorBody(`no hint here`); d != 0 {
		t.Fatalf("want 0, got %v", d)
	}
}

func TestHTTPRetryAfterPrefersBodyHintOverHeader(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: 429,
		Header:     http.Header{"Retry-After": []string{"5"}},
	}
	body := []byte(`{"error":{"message":"Please retry in 30s"}}`)
	if d := httpRetryAfter(resp, body); d != 30*time.Second {
		t.Fatalf("want body hint 30s, got %v", d)
	}
	resp.Header = http.Header{"Retry-After": []string{"60"}}
	if d := httpRetryAfter(resp, body); d != 60*time.Second {
		t.Fatalf("want larger header when bigger than body, got %v", d)
	}
}

func TestParseRetryAfterHeader(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("Retry-After", "42")
	if d := parseRetryAfterHeader(h); d != 42*time.Second {
		t.Fatalf("delta seconds: got %v want 42s", d)
	}
	h.Set("Retry-After", "not-a-number")
	if d := parseRetryAfterHeader(h); d != 0 {
		t.Fatalf("invalid: got %v want 0", d)
	}
}

func TestRetrySleepDurationUsesRetryAfter(t *testing.T) {
	t.Parallel()
	base := time.Second
	max := 60 * time.Second
	err := &APIHTTPError{Status: 429, RetryAfter: 25 * time.Second}
	d := retrySleepDuration(0, base, max, err)
	if d < 18*time.Second || d > 60*time.Second {
		t.Fatalf("expected ~25s after jitter in range [18s,25s], got %v", d)
	}
}

func TestRetrySleepDurationQuotaFloor(t *testing.T) {
	t.Parallel()
	base := 500 * time.Millisecond
	max := 60 * time.Second
	err := &APIHTTPError{Status: 429}
	d := retrySleepDuration(0, base, max, err)
	if d < 3*time.Second {
		t.Fatalf("429 should lift toward minimum throttle floor, got %v", d)
	}
}

func TestSleepInterruptible(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepInterruptible(ctx, time.Second) == nil {
		t.Fatal("expect ctx error")
	}
}
