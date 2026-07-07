package ai

import (
	"context"
	"time"
)

// CallReport is the telemetry for one completed logical inference call
// (including any transport retries). It is delivered to the observer installed
// via WithUsageObserver so higher layers (the review runner) can aggregate
// per-stage and per-run usage/cost without importing transport details.
type CallReport struct {
	Provider string
	Model    string
	// Stage is the telemetry label read from the context (see
	// applog.WithStage), e.g. "specialist security" or "repo-arbiter". Empty
	// when the caller set no stage.
	Stage    string
	Usage    Usage
	Duration time.Duration
	Retries  int
	// Err is non-nil when the call ultimately failed after retries. Failed
	// calls usually carry zero Usage but are still reported so callers can
	// count attempts.
	Err error
}

// usageObserverKey is the context key carrying the per-run usage sink.
type usageObserverKey struct{}

// WithUsageObserver returns a context whose inference calls report a CallReport
// to fn once each has completed. fn may be invoked from multiple goroutines
// concurrently (e.g. parallel specialists), so it must be safe for concurrent
// use. Passing a nil fn returns ctx unchanged.
//
// This is the R1 seam: usage/cost metering attaches here, at the single
// provider layer, so the whole call graph is metered without threading a sink
// through every review signature.
func WithUsageObserver(ctx context.Context, fn func(CallReport)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, usageObserverKey{}, fn)
}

// usageObserverFromContext returns the observer set by WithUsageObserver, or
// nil when none is installed.
func usageObserverFromContext(ctx context.Context) func(CallReport) {
	if ctx == nil {
		return nil
	}
	if fn, ok := ctx.Value(usageObserverKey{}).(func(CallReport)); ok {
		return fn
	}
	return nil
}
