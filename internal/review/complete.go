package review

import (
	"context"
	"encoding/json"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
)

// Complete runs inference for the given prompts using cfg.Provider transport.
// worktree is only used for the Claude subprocess (cwd and --add-dir).
//
// It is a thin shim over internal/ai: it builds an ai.Request, looks up the
// provider via the registry, and returns the completion text. Retry/backoff,
// per-call logging, and usage capture all live in internal/ai. The signature
// is preserved so the large call-site blast radius (and the shared
// ai.CompleteFunc injected into subpackages) stays unchanged.
func Complete(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt, worktree string) (string, error) {
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}
	provider, err := ai.ProviderFor(cfg)
	if err != nil {
		return "", err
	}
	res, err := provider.Complete(ctx, ai.Request{
		System:   systemPrompt,
		User:     userPrompt,
		Worktree: worktree,
		Stage:    applog.StageFromContext(ctx),
		// JSON-producing stages mark their context with ai.WithJSONMode (via
		// completeJSON / the witness / repair); providers that support native
		// JSON mode then request json_object / responseMimeType. The F2
		// salvage ladder still runs on every response as the fallback.
		WantJSON: ai.JSONModeFromContext(ctx),
		// Q2: a JSON stage may also attach its per-agent, registry-derived
		// schema with ai.WithJSONSchema. Schema-capable providers (Gemini
		// responseSchema) constrain the output shape; schema-less JSON
		// providers ignore it and use plain json_object mode.
		JSONSchema: ai.JSONSchemaFromContext(ctx),
		// P6 streaming: the runner installs ai.WithStreaming once per run, so
		// every stage streams (SSE / claude stream-json) — surfacing
		// token-liveness and swapping the whole-response HTTP timeout for
		// idle/first-byte timeouts. The accumulated Result/Usage is identical
		// to the non-streaming path, so ad-hoc callers that do not opt in
		// (evals, one-off tools) are unaffected.
		Stream: ai.StreamingFromContext(ctx),
	})
	return res.Text, err
}

// Complete satisfies ai.CompleteFunc so subpackages can inject it without an
// import cycle.
var _ ai.CompleteFunc = Complete

// completeJSON is Complete for JSON-producing stages: it opts the call into
// native JSON mode (ai.WithJSONMode) so providers that support it constrain the
// output to JSON. It keeps the ai.CompleteFunc-shaped signature so call sites
// change by name only. The salvage ladder (llmjson.Parse) remains the parse
// fallback — native JSON mode reduces parse-failure retries, it does not
// replace the ladder.
func completeJSON(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt, worktree string) (string, error) {
	return Complete(ai.WithJSONMode(ctx), cfg, systemPrompt, userPrompt, worktree)
}

// completeJSONWithSchema is completeJSON that also attaches a per-agent,
// registry-derived JSON schema (Q2) to the call so schema-capable providers
// constrain the response shape (Gemini responseSchema). It keeps the
// ai.CompleteFunc-shaped call convention (schema rides the context, not the
// signature) and degrades to plain native JSON mode when schema is empty or
// the provider only supports schema-less JSON mode. The llmjson salvage ladder
// still parses every response as the fallback.
func completeJSONWithSchema(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt, worktree string, schema json.RawMessage) (string, error) {
	return completeJSON(ai.WithJSONSchema(ctx, schema), cfg, systemPrompt, userPrompt, worktree)
}
