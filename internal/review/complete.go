package review

import (
	"context"

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
