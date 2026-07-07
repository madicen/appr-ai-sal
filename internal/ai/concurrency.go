package ai

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// DefaultMaxConcurrentInference is the fallback cap on how many inference
// calls may be in flight at once across a whole run when no explicit limit is
// configured. It intentionally mirrors the repoconfig default so both layers
// agree. A conservative 3 keeps parallel specialist / PR-agent dispatch from
// bursting past provider rate limits (the reason parallel mode was previously
// off by default).
const DefaultMaxConcurrentInference = 3

// ResolveMaxConcurrentInference maps a requested limit onto a safe positive
// value. Any value <= 0 resolves to DefaultMaxConcurrentInference so a
// zero-valued config field or a hostile negative never means "unlimited" and
// can never size a semaphore at 0 (which would deadlock every call).
func ResolveMaxConcurrentInference(n int) int {
	if n <= 0 {
		return DefaultMaxConcurrentInference
	}
	return n
}

// inferenceLimiter is a per-run, shared weighted semaphore that caps how many
// Complete calls run concurrently across the entire call graph — including the
// hidden repair pass and the PR-agent calls — regardless of which caller
// dispatched them. It lives at the provider seam (see retryProvider.Complete)
// so every ai.Provider.Complete is gated no matter who invokes it.
type inferenceLimiter struct {
	sem *semaphore.Weighted
}

// concurrencyLimitKey is the context key carrying the per-run inference
// limiter. Storing the limiter on the context (rather than on a provider
// value) is what makes the cap genuinely shared per run: review.Complete
// rebuilds a fresh provider for every call, so a decorator instance would gate
// nothing, whereas every Complete derives from the one run context and reads
// the one shared semaphore.
type concurrencyLimitKey struct{}

// WithConcurrencyLimit returns a context whose inference calls are capped at
// ResolveMaxConcurrentInference(n) concurrent Complete calls. Install it once
// per run (like WithUsageObserver); every stage goroutine that derives from
// the returned context shares the single underlying semaphore, so the cap
// holds across all concurrent stages of one run.
//
// A nil ctx is treated as context.Background().
func WithConcurrencyLimit(ctx context.Context, n int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	n = ResolveMaxConcurrentInference(n)
	return context.WithValue(ctx, concurrencyLimitKey{}, &inferenceLimiter{
		sem: semaphore.NewWeighted(int64(n)),
	})
}

// limiterFromContext returns the limiter installed by WithConcurrencyLimit, or
// nil when none is present (in which case calls are ungated, preserving
// behaviour for callers that never opt in).
func limiterFromContext(ctx context.Context) *inferenceLimiter {
	if ctx == nil {
		return nil
	}
	if lim, ok := ctx.Value(concurrencyLimitKey{}).(*inferenceLimiter); ok {
		return lim
	}
	return nil
}

// acquire blocks until a slot is free or ctx is cancelled. On success it
// returns a release func that MUST be called exactly once to return the slot;
// on cancellation it returns ctx.Err() promptly and a no-op release. This is
// the ctx-cancellation-while-blocked contract R2 requires.
func (l *inferenceLimiter) acquire(ctx context.Context) (func(), error) {
	if l == nil || l.sem == nil {
		return func() {}, nil
	}
	if err := l.sem.Acquire(ctx, 1); err != nil {
		// Acquire returns ctx.Err() when ctx is cancelled while blocked and
		// does not hold the slot in that case, so the release is a no-op.
		return func() {}, err
	}
	return func() { l.sem.Release(1) }, nil
}
