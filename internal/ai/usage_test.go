package ai

import (
	"context"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
)

// fakeProvider is a stub base provider that returns a fixed Result so the
// retryProvider's usage-reporting path can be exercised without transport.
type fakeProvider struct{ res Result }

func (f *fakeProvider) Complete(context.Context, Request) (Result, error) { return f.res, nil }
func (f *fakeProvider) Capabilities() Capabilities                        { return Capabilities{} }
func (f *fakeProvider) Name() string                                      { return "fake" }

// TestRetryProviderReportsUsageToObserver confirms the retryProvider forwards
// the backend's parsed usage (and the context stage label) to the observer
// installed via WithUsageObserver.
func TestRetryProviderReportsUsageToObserver(t *testing.T) {
	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderOpenAICompatible
	rp := &retryProvider{cfg: cfg, inner: &fakeProvider{res: Result{
		Text:  "hi",
		Usage: Usage{InputTokens: 111, OutputTokens: 22, CostUSD: 0.07},
		Model: "m",
	}}}

	var got CallReport
	ctx := WithUsageObserver(applog.WithStage(context.Background(), "vibe-coach"), func(r CallReport) { got = r })

	res, err := rp.Complete(ctx, Request{System: "s", User: "u"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Text != "hi" {
		t.Fatalf("text = %q", res.Text)
	}
	if got.Usage.InputTokens != 111 || got.Usage.OutputTokens != 22 || got.Usage.CostUSD != 0.07 {
		t.Fatalf("observer usage = %+v, want in=111 out=22 cost=0.07", got.Usage)
	}
	if got.Stage != "vibe-coach" {
		t.Fatalf("observer stage = %q, want vibe-coach", got.Stage)
	}
}

// TestRetryProviderNoObserverIsFine confirms a missing observer doesn't panic.
func TestRetryProviderNoObserverIsFine(t *testing.T) {
	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderOpenAICompatible
	rp := &retryProvider{cfg: cfg, inner: &fakeProvider{res: Result{Text: "ok"}}}
	if _, err := rp.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("Complete without observer: %v", err)
	}
}
