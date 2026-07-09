package ai

import (
	"context"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

func TestAttemptBudgetConsume(t *testing.T) {
	t.Parallel()
	b := NewAttemptBudget(3)
	if b.Remaining() != 3 {
		t.Fatalf("Remaining = %d, want 3", b.Remaining())
	}
	got := 0
	for b.tryConsume() {
		got++
	}
	if got != 3 {
		t.Fatalf("consumed %d, want 3", got)
	}
	if b.Remaining() != 0 {
		t.Fatalf("Remaining after drain = %d, want 0", b.Remaining())
	}
	if b.tryConsume() {
		t.Fatal("tryConsume should be false once exhausted")
	}
}

func TestAttemptBudgetFloor(t *testing.T) {
	t.Parallel()
	if n := NewAttemptBudget(0).Remaining(); n != 1 {
		t.Fatalf("NewAttemptBudget(0) floored to %d, want 1", n)
	}
	if n := NewAttemptBudget(-5).Remaining(); n != 1 {
		t.Fatalf("NewAttemptBudget(-5) floored to %d, want 1", n)
	}
}

// TestCompleteWithRetryHonorsBudget proves the inner per-Complete retry loop
// makes no more provider calls than the shared attempt budget allows, even
// when its own max-attempts would permit more.
func TestCompleteWithRetryHonorsBudget(t *testing.T) {
	t.Parallel()
	cfg := &aiconfig.Config{
		RetryMaxAttempts: 5, // inner loop would allow 5 without a budget
		RetryBaseMS:      1,
		RetryMaxMS:       1,
	}
	ctx := WithAttemptBudget(context.Background(), NewAttemptBudget(3))

	calls := 0
	_, _, err := completeWithRetry(ctx, cfg, func(context.Context) (Result, error) {
		calls++
		return Result{}, &APIHTTPError{Status: 503} // retryable, non-quota
	})
	if err == nil {
		t.Fatal("expected an error after exhausting the budget")
	}
	if calls != 3 {
		t.Fatalf("provider called %d times, want 3 (bounded by the budget, not RetryMaxAttempts=5)", calls)
	}
}

func TestCompleteWithRetryNoBudgetUsesMaxAttempts(t *testing.T) {
	t.Parallel()
	cfg := &aiconfig.Config{RetryMaxAttempts: 4, RetryBaseMS: 1, RetryMaxMS: 1}
	calls := 0
	_, _, _ = completeWithRetry(context.Background(), cfg, func(context.Context) (Result, error) {
		calls++
		return Result{}, &APIHTTPError{Status: 503}
	})
	if calls != 4 {
		t.Fatalf("without a budget, provider should be called RetryMaxAttempts=4 times, got %d", calls)
	}
}
