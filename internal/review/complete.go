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
	})
	return res.Text, err
}

// Complete satisfies ai.CompleteFunc so subpackages can inject it without an
// import cycle.
var _ ai.CompleteFunc = Complete
