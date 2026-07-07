package ai

import "context"

// jsonModeKey is the context key carrying the "this call wants native JSON
// mode" intent from a pipeline stage down into the review.Complete shim, which
// translates it into Request.WantJSON. It lets the fixed ai.CompleteFunc
// signature (system/user/worktree strings, no schema) still opt a call into
// JSON mode without a wider signature change — mirroring applog.WithStage.
type jsonModeKey struct{}

// WithJSONMode returns a context that marks inference made under it as wanting
// native JSON mode. The review.Complete shim reads this and sets
// Request.WantJSON so JSON-producing stages (specialists, PR agents, arbiter,
// witness, vibe-coach, repair) request json_object / responseMimeType from
// providers that support it. Markdown-brief calls simply do not set it.
func WithJSONMode(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, jsonModeKey{}, true)
}

// JSONModeFromContext reports whether WithJSONMode was applied to ctx.
func JSONModeFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, ok := ctx.Value(jsonModeKey{}).(bool)
	return ok && v
}

// wantsJSON reports whether the request should be sent in native JSON mode:
// either an explicit WantJSON flag or a non-empty schema (schema implies JSON).
func (r Request) wantsJSON() bool {
	return r.WantJSON || len(r.JSONSchema) > 0
}
