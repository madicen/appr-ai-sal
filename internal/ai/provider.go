package ai

import (
	"context"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
)

// retryProvider wraps a base provider with exponential-backoff retries and
// one log line per logical call (provider, model, stage, duration, retry
// count). This is the seam that later middleware (a concurrency semaphore,
// response caching, multi-model routing) hangs off of.
type retryProvider struct {
	cfg   *aiconfig.Config
	inner Provider
}

func (p *retryProvider) Name() string { return p.inner.Name() }

func (p *retryProvider) Capabilities() Capabilities { return p.inner.Capabilities() }

func (p *retryProvider) Complete(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	res, retries, err := completeWithRetry(ctx, p.cfg, func(ctx context.Context) (Result, error) {
		return p.inner.Complete(ctx, req)
	})
	// Telemetry only — provider/model/stage are non-secret labels; the API
	// key is never passed here. The stage label is read from the context via
	// applog.WithStage set by the caller. See internal/applog.
	applog.LLMCall(ctx, string(p.cfg.Provider), p.cfg.AIModelOrDefault(), retries, time.Since(start), err)
	return res, err
}
