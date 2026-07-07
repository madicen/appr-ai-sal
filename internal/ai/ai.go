// Package ai is the leaf provider layer for appr-ai-sal inference.
//
// It owns all backend transport (the Claude CLI subprocess and the HTTP
// providers), retry/backoff, usage/cost capture, and inference-call logging.
// It depends only on internal/aiconfig and internal/applog (both leaves), so
// it introduces no import cycle with internal/review or any TUI package: this
// is the single seam where later middleware attaches (usage metering, a
// concurrency semaphore, response caching, multi-model routing).
package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// Request is one inference request. System/User are the two prompt halves;
// Worktree is honoured only by tool-capable providers (the Claude subprocess).
type Request struct {
	System, User string
	// Worktree is the checked-out PR directory; used only by tool-capable
	// providers (Claude reads files there).
	Worktree string
	// JSONSchema, when set, requests native JSON mode from providers that
	// support it. Wiring is deferred (R5); the field is carried through today.
	JSONSchema json.RawMessage
	// Stage is a telemetry label (e.g. "specialist security"). Logging today
	// reads the stage from the context via applog.WithStage; the field is
	// carried for later use.
	Stage string
}

// Usage captures token counts and cost when the provider reports them; fields
// left at zero mean the backend did not surface that datum.
type Usage struct {
	InputTokens, OutputTokens int
	CostUSD                   float64
}

// Result is one completed inference: the assistant text plus any usage/model
// metadata the backend returned.
type Result struct {
	Text  string
	Usage Usage
	Model string
}

// Capabilities describes what a provider backend can do. HTTP providers today
// review the diff blind (no repo tools); only the Claude subprocess gets
// Read/Glob/Grep. NativeJSON/Streaming are conservative defaults (false) that
// later workstreams flip on.
type Capabilities struct {
	RepoTools, NativeJSON, Streaming bool
}

// Provider is one inference backend. Complete performs a single logical
// inference (retry/backoff and logging are layered on by ProviderFor).
type Provider interface {
	Complete(ctx context.Context, req Request) (Result, error)
	Capabilities() Capabilities
	Name() string
}

// CompleteFunc is the single shared inference-function type used across the
// codebase. review.Complete (a thin shim over this package) matches it, and
// subpackages that must call inference without importing review accept a value
// of this type — replacing the five per-package CompleteFunc typedefs that
// previously existed only to dodge an import cycle.
type CompleteFunc func(ctx context.Context, cfg *aiconfig.Config, system, user, worktree string) (string, error)

// testBaseProviderHook, when non-nil, supplies the base (retry-less) provider
// instead of the built-in registry. It exists ONLY so tests — including
// sibling-package tests such as internal/review — can inject a fake backend and
// exercise the real retry / attempt-budget / concurrency middleware end to end.
// Production code never sets it. Not safe to mutate concurrently, so callers
// (via SetBaseProviderForTest) must not run such tests in parallel.
var testBaseProviderHook func(cfg *aiconfig.Config) (Provider, error)

// SetBaseProviderForTest installs a base-provider factory for tests and returns
// a restore function. Test-only: see testBaseProviderHook.
func SetBaseProviderForTest(fn func(cfg *aiconfig.Config) (Provider, error)) func() {
	prev := testBaseProviderHook
	testBaseProviderHook = fn
	return func() { testBaseProviderHook = prev }
}

// baseProviderFor returns the raw (retry-less, unlogged) provider for cfg.
func baseProviderFor(cfg *aiconfig.Config) (Provider, error) {
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}
	if testBaseProviderHook != nil {
		return testBaseProviderHook(cfg)
	}
	switch cfg.Provider {
	case aiconfig.ProviderClaude:
		return &claudeProvider{cfg: cfg}, nil
	case aiconfig.ProviderOllama, aiconfig.ProviderOpenAICompatible:
		return &openAIProvider{cfg: cfg}, nil
	case aiconfig.ProviderGemini:
		return &geminiProvider{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unsupported AI provider %q", cfg.Provider)
	}
}

// ProviderFor returns the Provider for cfg wrapped with retry/backoff and
// per-call logging. This is the registry the rest of the app looks providers
// up through.
func ProviderFor(cfg *aiconfig.Config) (Provider, error) {
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}
	base, err := baseProviderFor(cfg)
	if err != nil {
		return nil, err
	}
	return &retryProvider{cfg: cfg, inner: base}, nil
}

// CapabilitiesFor reports the capabilities of cfg's provider. Unknown
// providers report the zero value (all false) rather than erroring, so callers
// can branch on RepoTools without handling a lookup error.
func CapabilitiesFor(cfg *aiconfig.Config) Capabilities {
	base, err := baseProviderFor(cfg)
	if err != nil {
		return Capabilities{}
	}
	return base.Capabilities()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
