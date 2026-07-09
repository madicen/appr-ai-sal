package conventionwitness

import (
	"context"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// TestWitnessModelDecorrelation is the Q7 witness-decorrelation acceptance
// test: when stage_models["witness"] is set the witness runs on its own model
// (a different family than the specialists it audits); otherwise it stays on
// the profile model.
func TestWitnessModelDecorrelation(t *testing.T) {
	var gotModel string
	complete := func(_ context.Context, cfg *aiconfig.Config, _, _, _ string) (string, error) {
		gotModel = cfg.Model
		return `{"witnesses":[]}`, nil
	}
	findings := []FindingInput{{
		Specialist: "testing", Path: "a_test.go", Line: 3, Severity: "warning", Comment: "add coverage",
	}}

	routed := &aiconfig.Config{
		Provider:    aiconfig.ProviderClaude,
		Model:       "base",
		StageModels: map[string]string{"witness": "witness-model"},
	}
	if res := Run(context.Background(), routed, complete, "/tmp", PrWideRef{Number: 1}, findings, ""); res.Err != nil {
		t.Fatalf("routed witness run errored: %v", res.Err)
	}
	if gotModel != "witness-model" {
		t.Fatalf("witness ran on %q, want witness-model", gotModel)
	}

	// Control: no witness routing → the witness stays on the profile model.
	unrouted := &aiconfig.Config{Provider: aiconfig.ProviderClaude, Model: "base"}
	if res := Run(context.Background(), unrouted, complete, "/tmp", PrWideRef{Number: 1}, findings, ""); res.Err != nil {
		t.Fatalf("unrouted witness run errored: %v", res.Err)
	}
	if gotModel != "base" {
		t.Fatalf("unrouted witness ran on %q, want base (profile model)", gotModel)
	}
}
