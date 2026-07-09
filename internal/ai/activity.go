package ai

import (
	"context"
	"time"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// ActivityReport is a throttled streaming-liveness heartbeat (P6 streaming).
// It is delivered to the observer installed via WithActivityObserver as tokens
// arrive during a streaming call, so higher layers (the review runner → the
// TUI running overlay) can show a long call visibly progressing instead of
// looking hung. It is cheap and throttled (at most a few per second per call).
type ActivityReport struct {
	// Stage is the telemetry label read from the context (see
	// applog.WithStage), e.g. "specialist security" — it lets the overlay
	// attach the heartbeat to the right agent row.
	Stage string
	// Tokens is a running count of visible deltas received so far this call
	// (an approximate token/chunk count — providers stream at different
	// granularities). Monotonic within a call.
	Tokens int
	// Bytes is the running count of visible delta bytes received this call.
	Bytes int
}

// activityObserverKey is the context key carrying the per-run liveness sink.
type activityObserverKey struct{}

// WithActivityObserver returns a context whose streaming inference calls report
// an ActivityReport to fn as deltas arrive (throttled). fn may be invoked from
// multiple goroutines concurrently (parallel specialists), so it must be safe
// for concurrent use. A nil fn returns ctx unchanged.
//
// This mirrors WithUsageObserver: liveness metering attaches at the single
// provider layer, so the whole streaming call graph surfaces heartbeats without
// threading a channel through every review signature.
func WithActivityObserver(ctx context.Context, fn func(ActivityReport)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, activityObserverKey{}, fn)
}

// activityObserverFromContext returns the observer set by WithActivityObserver,
// or nil when none is installed.
func activityObserverFromContext(ctx context.Context) func(ActivityReport) {
	if ctx == nil {
		return nil
	}
	if fn, ok := ctx.Value(activityObserverKey{}).(func(ActivityReport)); ok {
		return fn
	}
	return nil
}

// activityEmitter throttles per-call liveness heartbeats so a fast token stream
// does not flood the Progress channel. It is used by a single stream-reading
// goroutine, so its fields need no synchronisation.
type activityEmitter struct {
	fn          func(ActivityReport)
	stage       string
	tokens      int
	bytes       int
	last        time.Time
	minInterval time.Duration
}

// activityMinInterval caps heartbeat frequency to at most ~5/sec per call.
const activityMinInterval = 200 * time.Millisecond

func newActivityEmitter(ctx context.Context) *activityEmitter {
	return &activityEmitter{
		fn:          activityObserverFromContext(ctx),
		stage:       applog.StageFromContext(ctx),
		minInterval: activityMinInterval,
	}
}

// tick records one delta (which also counts an empty-delta heartbeat such as an
// SSE ping) and emits a throttled ActivityReport.
func (e *activityEmitter) tick(delta string) {
	if delta != "" {
		e.tokens++
		e.bytes += len(delta)
	}
	if e.fn == nil {
		return
	}
	now := time.Now()
	if now.Sub(e.last) < e.minInterval {
		return
	}
	e.last = now
	e.fn(ActivityReport{Stage: e.stage, Tokens: e.tokens, Bytes: e.bytes})
}

// flush emits a final, un-throttled heartbeat with the total counts so the
// overlay's last displayed token count reflects the whole response.
func (e *activityEmitter) flush() {
	if e.fn == nil {
		return
	}
	e.fn(ActivityReport{Stage: e.stage, Tokens: e.tokens, Bytes: e.bytes})
}
