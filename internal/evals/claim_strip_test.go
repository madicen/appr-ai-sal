package evals

import (
	"path/filepath"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestStripUnsupportedClaims(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pkg", "real.go"), "package pkg\n\nfunc RealHelper() {}\n")

	in := []review.Finding{
		{Comment: "Call `InventedFn` here."},
		{Comment: "Prefer `RealHelper`."},
		{Comment: "No refs."},
	}
	out := stripUnsupportedClaims(dir, in)
	if len(out) != 2 {
		t.Fatalf("kept %d, want 2 (RealHelper + no-refs); got %#v", len(out), out)
	}
	if out[0].Comment != "Prefer `RealHelper`." || out[1].Comment != "No refs." {
		t.Fatalf("unexpected keep order: %#v", out)
	}
}

func TestStripUnsupportedClaimsNoWorktree(t *testing.T) {
	in := []review.Finding{{Comment: "Uses `Invented`."}}
	out := stripUnsupportedClaims("", in)
	if len(out) != 1 {
		t.Fatalf("empty worktree must be no-op, got %#v", out)
	}
}

func TestApplyPostEvalTreatmentClaimStrip(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
	obs := review.EvalObservation{
		Agents: []review.EvalAgentObservation{
			{Agent: "design", Findings: []review.Finding{
				{Comment: "See `Missing`."},
				{Comment: "ok"},
			}},
		},
	}
	t.Setenv(evalTreatmentEnv, "claim-strip-v1")
	got := applyPostEvalTreatment(dir, obs)
	if len(got.Agents[0].Findings) != 1 || got.Agents[0].Findings[0].Comment != "ok" {
		t.Fatalf("claim-strip-v1: got %#v", got.Agents[0].Findings)
	}

	// Fresh obs — strip mutates the agent findings slice in place.
	obs = review.EvalObservation{
		Agents: []review.EvalAgentObservation{
			{Agent: "design", Findings: []review.Finding{
				{Comment: "See `Missing`."},
				{Comment: "ok"},
			}},
		},
	}
	t.Setenv(evalTreatmentEnv, "")
	got = applyPostEvalTreatment(dir, obs)
	if len(got.Agents[0].Findings) != 2 {
		t.Fatalf("control must keep both findings, got %#v", got.Agents[0].Findings)
	}
}
