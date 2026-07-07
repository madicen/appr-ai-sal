package review

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
)

// TestUsageAccumulatorSumsAcrossStages verifies the per-run totals are the sum
// of every recorded call and that the per-stage breakdown is bucketed
// correctly (R1 aggregation math).
func TestUsageAccumulatorSumsAcrossStages(t *testing.T) {
	acc := newUsageAccumulator()
	acc.record(ai.CallReport{Stage: "specialist security", Usage: ai.Usage{InputTokens: 100, OutputTokens: 20, CostUSD: 0.10}, Duration: 2 * time.Second})
	acc.record(ai.CallReport{Stage: "specialist security", Usage: ai.Usage{InputTokens: 50, OutputTokens: 10, CostUSD: 0.05}, Duration: 1 * time.Second})
	acc.record(ai.CallReport{Stage: "repo-arbiter", Usage: ai.Usage{InputTokens: 300, OutputTokens: 40}, Duration: 3 * time.Second})

	snap := acc.snapshot(90 * time.Second)

	if snap.Calls != 3 {
		t.Fatalf("Calls = %d, want 3", snap.Calls)
	}
	if snap.InputTokens != 450 {
		t.Fatalf("InputTokens = %d, want 450", snap.InputTokens)
	}
	if snap.OutputTokens != 70 {
		t.Fatalf("OutputTokens = %d, want 70", snap.OutputTokens)
	}
	if snap.CostUSD < 0.149 || snap.CostUSD > 0.151 {
		t.Fatalf("CostUSD = %v, want ~0.15", snap.CostUSD)
	}
	if !snap.CostKnown {
		t.Fatalf("CostKnown should be true when a call reported cost")
	}
	if snap.WallClock != 90*time.Second {
		t.Fatalf("WallClock = %v, want 90s", snap.WallClock)
	}

	// Per-stage breakdown: two stages, security folded together.
	byStage := map[string]StageUsage{}
	for _, s := range snap.Stages {
		byStage[s.Stage] = s
	}
	if len(byStage) != 2 {
		t.Fatalf("expected 2 stage buckets, got %d (%v)", len(byStage), snap.Stages)
	}
	sec := byStage["specialist security"]
	if sec.Calls != 2 || sec.InputTokens != 150 || sec.OutputTokens != 30 {
		t.Fatalf("security stage = %+v, want calls=2 in=150 out=30", sec)
	}
	if sec.Duration != 3*time.Second {
		t.Fatalf("security stage duration = %v, want 3s", sec.Duration)
	}
	arb := byStage["repo-arbiter"]
	if arb.Calls != 1 || arb.InputTokens != 300 || arb.OutputTokens != 40 {
		t.Fatalf("arbiter stage = %+v, want calls=1 in=300 out=40", arb)
	}
}

// TestUsageAccumulatorCostUnknown confirms that when no provider reports cost
// the snapshot leaves CostKnown false and blank stages bucket as "other".
func TestUsageAccumulatorCostUnknown(t *testing.T) {
	acc := newUsageAccumulator()
	acc.record(ai.CallReport{Stage: "", Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}})
	snap := acc.snapshot(0)
	if snap.CostKnown {
		t.Fatalf("CostKnown should be false when no call reported cost")
	}
	if len(snap.Stages) != 1 || snap.Stages[0].Stage != "other" {
		t.Fatalf("blank stage should bucket as \"other\", got %+v", snap.Stages)
	}
}

func TestFormatTokenCount(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{-5, "0"},
		{0, "0"},
		{999, "999"},
		{1000, "1k"},
		{21_000, "21k"},
		{182_345, "182k"},
		{999_999, "999k"},
		{1_000_000, "1M"},
		{1_500_000, "1.5M"},
		{2_000_000, "2M"},
	}
	for _, tc := range cases {
		if got := FormatTokenCount(tc.in); got != tc.want {
			t.Errorf("FormatTokenCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatCostUSD(t *testing.T) {
	if got := FormatCostUSD(0.43); got != "~$0.43" {
		t.Errorf("FormatCostUSD(0.43) = %q, want ~$0.43", got)
	}
	if got := FormatCostUSD(0); got != "~$0.00" {
		t.Errorf("FormatCostUSD(0) = %q, want ~$0.00", got)
	}
	if got := FormatCostUSD(-1); got != "~$0.00" {
		t.Errorf("FormatCostUSD(-1) = %q, want ~$0.00", got)
	}
}

func TestFormatRunDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-1, "0s"},
		{42 * time.Second, "42s"},
		{6*time.Minute + 12*time.Second, "6m12s"},
		{time.Minute, "1m00s"},
		{time.Hour + 5*time.Minute, "1h05m"},
	}
	for _, tc := range cases {
		if got := FormatRunDuration(tc.in); got != tc.want {
			t.Errorf("FormatRunDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRunUsageSummary exercises the compact one-line render, including the
// graceful omission of the cost segment when cost is unavailable.
func TestRunUsageSummary(t *testing.T) {
	withCost := RunUsage{Calls: 14, InputTokens: 182_000, OutputTokens: 21_000, CostUSD: 0.43, CostKnown: true, WallClock: 6*time.Minute + 12*time.Second}
	if got, want := withCost.Summary(), "14 calls · 182k in / 21k out · ~$0.43 · 6m12s"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
	// Unknown cost → the "~$…" segment is dropped, not shown as ~$0.00.
	noCost := RunUsage{Calls: 1, InputTokens: 500, OutputTokens: 60, WallClock: 3 * time.Second}
	if got, want := noCost.Summary(), "1 call · 500 in / 60 out · 3s"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
	if (RunUsage{}).HasData() {
		t.Fatalf("empty RunUsage should report HasData()==false")
	}
}

// TestUsageObserverReachesAggregator wires an ai.CompleteFunc call (the real
// review.Complete shim → ai provider layer) with a usage observer installed on
// the context and asserts the stage's ai.Result.Usage lands in the aggregator.
// This is the R1 end-to-end seam: providers already parse usage; the observer
// carries it out without churning call sites.
func TestUsageObserverReachesAggregator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":200,"completion_tokens":50}}`))
	}))
	defer srv.Close()

	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderOpenAICompatible
	cfg.BaseURL = srv.URL
	cfg.Model = "qwen"

	acc := newUsageAccumulator()
	ctx := ai.WithUsageObserver(applog.WithStage(context.Background(), "specialist security"), acc.record)

	out, err := Complete(ctx, cfg, "sys", "user", "")
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("Complete text = %q, want ok", out)
	}

	snap := acc.snapshot(0)
	if snap.Calls != 1 {
		t.Fatalf("Calls = %d, want 1", snap.Calls)
	}
	if snap.InputTokens != 200 || snap.OutputTokens != 50 {
		t.Fatalf("usage = in %d / out %d, want 200/50", snap.InputTokens, snap.OutputTokens)
	}
	if len(snap.Stages) != 1 || snap.Stages[0].Stage != "specialist security" {
		t.Fatalf("stage bucket = %+v, want single \"specialist security\"", snap.Stages)
	}
	if snap.Stages[0].InputTokens != 200 || snap.Stages[0].OutputTokens != 50 {
		t.Fatalf("stage usage = %+v, want in=200 out=50", snap.Stages[0])
	}
}
