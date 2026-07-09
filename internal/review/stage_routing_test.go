package review

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
)

// routeCall records one provider invocation's stage + model so a test can
// assert per-stage model routing (Q7).
type routeCall struct{ stage, model string }

// routingProvider is a fake ai.Provider that records the (stage, model) of
// every call and returns a per-model canned response. The base-provider hook
// hands it the stage-routed cfg, so cfg.Model is exactly the model the runner
// selected for that stage.
type routingProvider struct {
	cfg    *aiconfig.Config
	mu     *sync.Mutex
	calls  *[]routeCall
	respFn func(model string) string
}

func (p routingProvider) Name() string                  { return "routing-fake" }
func (p routingProvider) Capabilities() ai.Capabilities { return ai.Capabilities{} }
func (p routingProvider) Complete(ctx context.Context, _ ai.Request) (ai.Result, error) {
	model := p.cfg.Model
	p.mu.Lock()
	*p.calls = append(*p.calls, routeCall{stage: applog.StageFromContext(ctx), model: model})
	p.mu.Unlock()
	return ai.Result{Text: p.respFn(model), Model: model}, nil
}

// installRoutingProvider installs a routingProvider and returns the shared
// call log + a restore func.
func installRoutingProvider(t *testing.T, respFn func(model string) string) (*[]routeCall, func()) {
	t.Helper()
	var mu sync.Mutex
	calls := &[]routeCall{}
	restore := ai.SetBaseProviderForTest(func(cfg *aiconfig.Config) (ai.Provider, error) {
		return routingProvider{cfg: cfg, mu: &mu, calls: calls, respFn: respFn}, nil
	})
	return calls, restore
}

// sequentialRepoConfig returns a Default repoconfig with the parallel toggles
// off so a test sees deterministic call ordering.
func sequentialRepoConfig() *repoconfig.Config {
	rc := repoconfig.Default()
	rc.ParallelSpecialists = false
	rc.ParallelPRAgents = false
	return rc
}

// TestPerStageModelRouting is the Q7 per-stage routing acceptance test: with
// stage_models {security: opus, default: haiku}, security routes to opus and
// every other specialist to haiku.
func TestPerStageModelRouting(t *testing.T) {
	calls, restore := installRoutingProvider(t, func(string) string {
		return `{"summary":"ok","findings":[]}`
	})
	defer restore()

	runCfg := &aiconfig.Config{
		Provider:         aiconfig.ProviderClaude,
		Model:            "base",
		ReviewStrictness: aiconfig.ReviewBalanced,
		RetryMaxAttempts: 1,
		StageModels:      map[string]string{"security": "opus", "default": "haiku"},
	}
	out := make(chan Progress, 1000)
	breaker := newRunBreaker(time.Now(), 0, 0)
	_ = runSpecialistsPhase(context.Background(), runCfg, sequentialRepoConfig(), "/tmp/wt", &gh.PR{}, "",
		nil, "", "", nil, "", "", "", "", breaker, out)
	close(out)

	got := map[string]string{}
	for _, c := range *calls {
		got[c.stage] = c.model
	}
	if got["specialist "+SpecSecurity] != "opus" {
		t.Fatalf("security routed to %q, want opus", got["specialist "+SpecSecurity])
	}
	for _, name := range []string{SpecFormatting, SpecDesign, SpecTesting, SpecDocs} {
		if got["specialist "+name] != "haiku" {
			t.Fatalf("%s routed to %q, want haiku (stage_models default)", name, got["specialist "+name])
		}
	}
}

// TestNoStageModelsBackwardCompat proves that a config with no stage_models is
// behavior-identical to today: every stage runs on the single profile model.
func TestNoStageModelsBackwardCompat(t *testing.T) {
	calls, restore := installRoutingProvider(t, func(string) string {
		return `{"summary":"ok","findings":[]}`
	})
	defer restore()

	runCfg := &aiconfig.Config{
		Provider:         aiconfig.ProviderClaude,
		Model:            "only-model",
		ReviewStrictness: aiconfig.ReviewBalanced,
		RetryMaxAttempts: 1,
	}
	out := make(chan Progress, 1000)
	breaker := newRunBreaker(time.Now(), 0, 0)
	_ = runSpecialistsPhase(context.Background(), runCfg, sequentialRepoConfig(), "/tmp/wt", &gh.PR{}, "",
		nil, "", "", nil, "", "", "", "", breaker, out)
	close(out)

	if len(*calls) == 0 {
		t.Fatal("expected specialist calls")
	}
	for _, c := range *calls {
		if c.model != "only-model" {
			t.Fatalf("stage %q ran on %q; a config with no stage_models must use the single profile model", c.stage, c.model)
		}
	}
	// And ForStage must be a pure no-op (same pointer) for every stage.
	for _, name := range AllSpecialists {
		if runCfg.ForStage(name) != runCfg {
			t.Fatalf("ForStage(%q) cloned the config with no stage_models set", name)
		}
	}
}

// TestEnsembleUnionAndDedupe is the Q7 ensemble acceptance test: a stage
// configured with two models runs on both, and the findings union with the
// cross-specialist dedupe so an identical finding collapses while a
// model-unique one survives.
func TestEnsembleUnionAndDedupe(t *testing.T) {
	const dupe = "SQL injection risk in the query builder path"
	const uniqueB = "Hardcoded secret token committed in the config loader"
	respFn := func(model string) string {
		switch model {
		case "model-a":
			return `{"summary":"a","findings":[{"severity":"error","comment":"` + dupe + `"}]}`
		case "model-b":
			return `{"summary":"b","findings":[{"severity":"error","comment":"` + dupe + `"},{"severity":"error","comment":"` + uniqueB + `"}]}`
		default:
			return `{"summary":"","findings":[]}`
		}
	}
	calls, restore := installRoutingProvider(t, respFn)
	defer restore()

	runCfg := &aiconfig.Config{
		Provider:         aiconfig.ProviderClaude,
		Model:            "base",
		ReviewStrictness: aiconfig.ReviewBalanced,
		RetryMaxAttempts: 1,
		Ensemble:         map[string][]string{SpecSecurity: {"model-a", "model-b"}},
	}
	out := make(chan Progress, 1000)
	breaker := newRunBreaker(time.Now(), 0, 0)
	results := runSpecialistsPhase(context.Background(), runCfg, sequentialRepoConfig(), "/tmp/wt", &gh.PR{}, "",
		nil, "", "", nil, "", "", "", "", breaker, out)
	close(out)

	// Both ensemble models were invoked for the security stage.
	models := map[string]bool{}
	for _, c := range *calls {
		if c.stage == "specialist "+SpecSecurity {
			models[c.model] = true
		}
	}
	if !models["model-a"] || !models["model-b"] {
		t.Fatalf("security ensemble did not call both models: %v", models)
	}

	var sec *SpecialistResult
	for i := range results {
		if results[i].Specialist == SpecSecurity {
			sec = &results[i]
		}
	}
	if sec == nil {
		t.Fatal("no security result")
	}
	if len(sec.Findings) != 2 {
		t.Fatalf("ensemble union produced %d findings, want 2 (dupe collapsed, unique kept): %+v", len(sec.Findings), sec.Findings)
	}
	var sawDupe, sawUnique bool
	for _, f := range sec.Findings {
		if strings.Contains(f.Comment, "SQL injection") {
			sawDupe = true
		}
		if strings.Contains(f.Comment, "Hardcoded secret") {
			sawUnique = true
		}
	}
	if !sawDupe || !sawUnique {
		t.Fatalf("union missing findings: dupe=%v unique=%v", sawDupe, sawUnique)
	}
}
