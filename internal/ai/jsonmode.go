package ai

import (
	"context"
	"encoding/json"
)

// jsonModeKey is the context key carrying the "this call wants native JSON
// mode" intent from a pipeline stage down into the review.Complete shim, which
// translates it into Request.WantJSON. It lets the fixed ai.CompleteFunc
// signature (system/user/worktree strings, no schema) still opt a call into
// JSON mode without a wider signature change — mirroring applog.WithStage.
type jsonModeKey struct{}

// jsonSchemaKey is the context key carrying a per-agent JSON schema down into
// the review.Complete shim, which translates it into Request.JSONSchema. Like
// jsonModeKey it rides the context so the fixed ai.CompleteFunc signature does
// not need to grow a schema parameter: a JSON stage attaches its
// registry-derived schema (Q2) with WithJSONSchema and providers that support
// schema-constrained JSON (Gemini responseSchema) receive it, while providers
// that only support schema-less JSON mode ignore it and fall back to plain
// json_object / responseMimeType.
type jsonSchemaKey struct{}

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

// WithJSONSchema returns a context carrying schema as the JSON schema for
// inference made under it. The review.Complete shim reads it and sets
// Request.JSONSchema so schema-capable providers constrain the output shape
// (Gemini responseSchema). A non-empty schema also implies native JSON mode,
// so callers do not need to combine it with WithJSONMode. Passing an empty
// schema is a no-op (the context is returned unchanged) so stages can pass an
// optional schema unconditionally.
func WithJSONSchema(ctx context.Context, schema json.RawMessage) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(schema) == 0 {
		return ctx
	}
	return context.WithValue(ctx, jsonSchemaKey{}, schema)
}

// JSONSchemaFromContext returns the JSON schema attached with WithJSONSchema,
// or nil when none was set.
func JSONSchemaFromContext(ctx context.Context) json.RawMessage {
	if ctx == nil {
		return nil
	}
	v, ok := ctx.Value(jsonSchemaKey{}).(json.RawMessage)
	if !ok {
		return nil
	}
	return v
}

// wantsJSON reports whether the request should be sent in native JSON mode:
// either an explicit WantJSON flag or a non-empty schema (schema implies JSON).
func (r Request) wantsJSON() bool {
	return r.WantJSON || len(r.JSONSchema) > 0
}
