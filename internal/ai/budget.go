package ai

import (
	"context"
	"sync/atomic"
)

// AttemptBudget caps the TOTAL number of underlying provider invocations for a
// single pipeline stage. It is the shared pool that both retry tiers draw from:
//
//   - the stage-level retry loop (review.stageWithRetry), which re-runs the
//     whole stage on a transient error, and
//   - the inner per-Complete retry loop (completeWithRetry) here in internal/ai.
//
// Before R4 the two multiplied: up to 5 stage attempts × up to 5 inner attempts
// ≈ 25 provider calls per stage. Sharing one AttemptBudget bounds the product:
// every provider call claims one unit, and when the pool is empty both loops
// stop. The budget is carried on the context (WithAttemptBudget) so no
// signatures change; a stage installs a fresh budget and every Complete call
// derived from that context consumes from it.
type AttemptBudget struct {
	remaining atomic.Int64
}

// NewAttemptBudget returns a budget allowing n total provider invocations
// (floored at 1 so a stage always gets at least one call).
func NewAttemptBudget(n int) *AttemptBudget {
	if n < 1 {
		n = 1
	}
	b := &AttemptBudget{}
	b.remaining.Store(int64(n))
	return b
}

// tryConsume atomically claims one attempt, returning false when the budget is
// already exhausted. Safe for concurrent callers (parallel specialists each
// hold their own budget, but a single stage's main + repair calls may race).
func (b *AttemptBudget) tryConsume() bool {
	if b == nil {
		return true
	}
	for {
		cur := b.remaining.Load()
		if cur <= 0 {
			return false
		}
		if b.remaining.CompareAndSwap(cur, cur-1) {
			return true
		}
	}
}

// Remaining reports how many attempts are left (never negative).
func (b *AttemptBudget) Remaining() int {
	if b == nil {
		return 0
	}
	r := b.remaining.Load()
	if r < 0 {
		return 0
	}
	return int(r)
}

type attemptBudgetKey struct{}

// WithAttemptBudget installs b on ctx so completeWithRetry (and any nested
// inference call in the same stage) draws from the same pool.
func WithAttemptBudget(ctx context.Context, b *AttemptBudget) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, attemptBudgetKey{}, b)
}

// AttemptBudgetFromContext returns the stage's shared attempt budget, or nil
// when none was installed (e.g. a bare inference call outside the pipeline, in
// which case only the inner per-Complete cap applies).
func AttemptBudgetFromContext(ctx context.Context) *AttemptBudget {
	b, _ := ctx.Value(attemptBudgetKey{}).(*AttemptBudget)
	return b
}
