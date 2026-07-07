package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestNormalizeVibeVerdict(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"approve", VibeVerdictApprove},
		{"REQUEST_CHANGES", VibeVerdictRequestChanges},
		{"comment_only", VibeVerdictComment},
		{"", ""},
		{"maybe", ""},
	}
	for _, tc := range cases {
		if got := NormalizeVibeVerdict(tc.in); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestReconciledMergeVerdictDowngradesWhenAllInlinesSkipped(t *testing.T) {
	skipped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip me"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{skipped}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges, Summary: "Needs work."},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, skipped): {},
		},
	}
	if got := d.EffectiveMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("EffectiveMergeVerdict = %q want request_changes", got)
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictComment {
		t.Fatalf("ReconciledMergeVerdict = %q want comment", got)
	}
	if got := d.PostEvent(); got != "COMMENT" {
		t.Fatalf("PostEvent = %q want COMMENT", got)
	}
	note := d.VerdictReconciliationNote()
	if !strings.Contains(note, "downgraded") || !strings.Contains(note, "Request changes") {
		t.Fatalf("VerdictReconciliationNote should explain the downgrade, got: %s", note)
	}
}

func TestReconciledMergeVerdictKeepsRequestChangesWithGeneralErrors(t *testing.T) {
	// Mirror the user's screenshot: every inline got skipped/suppressed but
	// PR-wide error severities remain — the verdict should hold.
	skipped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				skipped,
				{Path: "", Line: 0, Severity: SeverityError, Comment: "README missing entries"},
			}},
			{Specialist: SpecTesting, Findings: []Finding{
				{Path: "", Line: 0, Severity: SeverityError, Comment: "no tests added"},
			}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges, Summary: "Block."},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, skipped): {},
		},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("ReconciledMergeVerdict = %q want request_changes (PR-wide error blockers remain)", got)
	}
	if note := d.VerdictReconciliationNote(); note != "" {
		t.Fatalf("VerdictReconciliationNote should be empty when no downgrade happened, got: %s", note)
	}
}

func TestReconciledMergeVerdictKeepsRequestChangesWithSurvivingPrompt(t *testing.T) {
	live := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "stays"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{live}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Block.",
			Prompts: []AuthorPrompt{
				{Title: "Keep me", AgentPrompt: "do it",
					FindingRefs: []FindingRef{{Specialist: SpecDocs, Path: "b.go", Line: 2}}},
			},
		},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("ReconciledMergeVerdict = %q want request_changes (surviving prompt is a blocker)", got)
	}
}

func TestReconciledMergeVerdictKeepsRequestChangesWhenArbiterOverrode(t *testing.T) {
	d := &Draft{
		PR:        &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove},
		RepoArbiter: &RepoArbiterResult{
			VerdictOverride:  VibeVerdictRequestChanges,
			EffectiveVerdict: VibeVerdictRequestChanges,
			UserSummary:      "Repo arbiter says block.",
		},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("ReconciledMergeVerdict = %q want request_changes (arbiter override is a blocker)", got)
	}
}

// TestEffectiveMergeVerdictGuardsRelaxingOverride is the regression for the
// "balanced review approved over a request-changes" report: the arbiter
// overrode to approve, but a surviving paste-ready prompt (real blocker)
// means the override may not relax the verdict — it's clamped back to the
// vibe-coach's request_changes.
func TestEffectiveMergeVerdictGuardsRelaxingOverride(t *testing.T) {
	live := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "stays"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecTesting, Findings: []Finding{live}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Block.",
			Prompts: []AuthorPrompt{
				{Title: "Fix", AgentPrompt: "do it",
					FindingRefs: []FindingRef{{Specialist: SpecTesting, Path: "b.go", Line: 2}}},
			},
		},
		RepoArbiter: &RepoArbiterResult{
			VerdictOverride:  VibeVerdictApprove,
			EffectiveVerdict: VibeVerdictApprove,
		},
	}
	if got := d.EffectiveMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("EffectiveMergeVerdict = %q want request_changes (relaxing override blocked by surviving prompt)", got)
	}
}

// TestEffectiveMergeVerdictHonoursOverrideWhenBlockersCleared confirms the
// arbiter still gets its relaxed verdict when it actually cleared the
// blockers (here, it suppressed the only finding the prompt referenced, so
// no blocking content survives).
func TestEffectiveMergeVerdictHonoursOverrideWhenBlockersCleared(t *testing.T) {
	cleared := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "gone"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecTesting, Findings: []Finding{cleared}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges, Summary: "Block."},
		RepoArbiter: &RepoArbiterResult{
			VerdictOverride:  VibeVerdictApprove,
			EffectiveVerdict: VibeVerdictApprove,
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecTesting, "b.go", 2, "RIGHT"): {},
			},
		},
	}
	if got := d.EffectiveMergeVerdict(); got != VibeVerdictApprove {
		t.Fatalf("EffectiveMergeVerdict = %q want approve (arbiter cleared the blocker, override stands)", got)
	}
}

// TestEffectiveMergeVerdictAllowsStricterOverride keeps the arbiter free to
// TIGHTEN the verdict (approve → request_changes) regardless of blockers.
func TestEffectiveMergeVerdictAllowsStricterOverride(t *testing.T) {
	d := &Draft{
		PR:        &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove},
		RepoArbiter: &RepoArbiterResult{
			VerdictOverride:  VibeVerdictRequestChanges,
			EffectiveVerdict: VibeVerdictRequestChanges,
		},
	}
	if got := d.EffectiveMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("EffectiveMergeVerdict = %q want request_changes (stricter override always allowed)", got)
	}
}

func TestReconciledMergeVerdictPassesThroughNonRequestChanges(t *testing.T) {
	d := &Draft{
		PR:        &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictApprove {
		t.Fatalf("ReconciledMergeVerdict = %q want approve (no downgrade for non-request-changes)", got)
	}
}
