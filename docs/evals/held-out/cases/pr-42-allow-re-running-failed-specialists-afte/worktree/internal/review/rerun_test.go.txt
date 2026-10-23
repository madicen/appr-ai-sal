package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// recordingJSONProvider returns fixed JSON and records the last user prompt.
type recordingJSONProvider struct {
	out      string
	lastUser *string
}

func (recordingJSONProvider) Name() string                  { return "recording-json" }
func (recordingJSONProvider) Capabilities() ai.Capabilities { return ai.Capabilities{} }
func (p recordingJSONProvider) Complete(_ context.Context, req ai.Request) (ai.Result, error) {
	if p.lastUser != nil {
		*p.lastUser = req.User
	}
	return ai.Result{Text: p.out}, nil
}

func TestReplaceAgentAndMarkSynthesisStale(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Findings: []Finding{{Path: "a.go", Line: 1, Severity: SeverityWarning, Comment: "x"}}},
			{Specialist: SpecDesign, Err: errors.New("boom"), Outcome: OutcomeFailed},
		},
		Rerun: &RerunContext{
			PreArbiterSpecialists: []SpecialistResult{
				{Specialist: SpecFormatting, Findings: []Finding{{Path: "a.go", Line: 1, Severity: SeverityWarning, Comment: "x"}}},
				{Specialist: SpecDesign, Err: errors.New("boom"), Outcome: OutcomeFailed},
			},
		},
		DemotedHidden: []FlatFinding{{Specialist: SpecFormatting}},
	}
	if !d.CanRerunAgent(SpecDesign) {
		t.Fatal("failed design should be re-runnable")
	}
	if d.CanRerunAgent(SpecFormatting) {
		t.Fatal("clean formatting must not be re-runnable")
	}
	if d.CanRerunAgent(SpecDesign); d.Rerun == nil {
		t.Fatal("sanity")
	}
	// Skipped agents are not re-runnable.
	d.Specialists = append(d.Specialists, SpecialistResult{Specialist: SpecDocs, Outcome: OutcomeSkipped, OutcomeReason: "breaker"})
	if d.CanRerunAgent(SpecDocs) {
		t.Fatal("skipped docs must not be re-runnable")
	}
	d.Specialists = d.Specialists[:2]

	ok := SpecialistResult{Specialist: SpecDesign, Findings: []Finding{{Path: "b.go", Line: 2, Severity: SeverityWarning, Comment: "y"}}}
	if err := d.ReplaceAgent(SpecDesign, ok); err != nil {
		t.Fatal(err)
	}
	if d.Specialists[1].Err != nil || len(d.Specialists[1].Findings) != 1 {
		t.Fatalf("replace failed: %+v", d.Specialists[1])
	}
	if d.Rerun.PreArbiterSpecialists[1].Err != nil {
		t.Fatal("pre-arbiter snapshot should also be replaced")
	}
	d.MarkSynthesisStale()
	if !d.ArbiterStale || !d.VibeStale {
		t.Fatalf("stale flags = arbiter:%v vibe:%v", d.ArbiterStale, d.VibeStale)
	}
	if len(d.DemotedHidden) != 0 {
		t.Fatalf("DemotedHidden should clear on stale, got %d", len(d.DemotedHidden))
	}
	failed := d.FailedAgentNames()
	if len(failed) != 0 {
		t.Fatalf("FailedAgentNames after successful replace = %v, want none", failed)
	}
}

func TestStaleTransitionsOnArbiterAndVibe(t *testing.T) {
	d := &Draft{ArbiterStale: true, VibeStale: true}
	d.ClearArbiterStale()
	if d.ArbiterStale || !d.VibeStale {
		t.Fatalf("after arbiter clear: arbiter=%v vibe=%v", d.ArbiterStale, d.VibeStale)
	}
	d.ClearVibeStale()
	if d.VibeStale {
		t.Fatal("vibe should be clear")
	}
}

func TestCanRerunAgentNilRerun(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDesign, Err: errors.New("boom"), Outcome: OutcomeFailed},
		},
	}
	if d.CanRerunAgent(SpecDesign) {
		t.Fatal("without RerunContext, re-run must be disabled")
	}
	if err := d.ReplaceAgent(SpecDesign, SpecialistResult{Specialist: SpecDesign}); err == nil {
		t.Fatal("ReplaceAgent without Rerun should error")
	}
}

func TestRunSpecialistForDraftUsesRerunDiff(t *testing.T) {
	const marker = "RERUN-DIFF-MARKER-xyzzy"
	var sawUser string
	restore := ai.SetBaseProviderForTest(func(*aiconfig.Config) (ai.Provider, error) {
		return recordingJSONProvider{
			out:      `{"summary":"clean","findings":[]}`,
			lastUser: &sawUser,
		}, nil
	})
	defer restore()

	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderClaude
	cfg.ReviewStrictness = aiconfig.ReviewBalanced
	cfg.RetryStageAttemptBudget = 1
	cfg.RetryMaxAttempts = 1

	d := &Draft{
		PR:       &gh.PR{Repository: "o/r", Number: 1, Title: "t", Author: "a", BaseRef: "main", HeadRef: "feat"},
		Worktree: "",
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Err: errors.New("boom"), Outcome: OutcomeFailed},
		},
		Rerun: &RerunContext{
			SpecialistDiff: "diff --git a/README.md b/README.md\n+++ b/README.md\n@@ -1 +1 @@\n+" + marker + "\n",
			PreArbiterSpecialists: []SpecialistResult{
				{Specialist: SpecDocs, Err: errors.New("boom"), Outcome: OutcomeFailed},
			},
		},
	}
	res := RunSpecialistForDraft(context.Background(), cfg, d, SpecDocs, nil)
	if res.Err != nil {
		t.Fatalf("RunSpecialistForDraft: %v", res.Err)
	}
	if !strings.Contains(sawUser, marker) {
		t.Fatalf("user prompt must include Rerun.SpecialistDiff marker; got %q", sawUser)
	}
	if err := RefreshDraftAfterAgentReplace(d, SpecDocs, res); err != nil {
		t.Fatal(err)
	}
	if !d.ArbiterStale || !d.VibeStale {
		t.Fatal("refresh should mark synthesis stale")
	}
	if d.Specialists[0].Err != nil {
		t.Fatalf("specialists should carry successful re-run, got %+v", d.Specialists[0])
	}
}
