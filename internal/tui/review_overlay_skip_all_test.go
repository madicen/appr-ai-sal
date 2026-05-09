package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

const skipAllTestDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1 @@
+a
diff --git a/b.go b/b.go
--- /dev/null
+++ b/b.go
@@ -0,0 +1 @@
+b
`

func TestReviewOverlaySkipAllFindingsOpensConfirmApprove(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
			{Specialist: review.SpecSecurity, Findings: []review.Finding{
				{Path: "b.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c2"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	if ro.phase != phaseApprove {
		t.Fatalf("phase %v want phaseApprove", ro.phase)
	}
	if len(ro.cards) != 2 {
		t.Fatalf("cards %d want 2", len(ro.cards))
	}
	out, _ := ro.actSkipCurrent()
	ro = out.(*reviewOverlay)
	if ro.phase != phaseApprove {
		t.Fatalf("after one skip phase %v want phaseApprove", ro.phase)
	}
	out, _ = ro.actSkipCurrent()
	ro = out.(*reviewOverlay)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("after skipping all, phase %v want phaseConfirmApprove", ro.phase)
	}
	if !ro.approveAfterSkipDisagree {
		t.Fatal("expected approveAfterSkipDisagree")
	}
}

func TestReviewOverlaySkipAllWithCommentVerdictOpensConfirmApprove(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityInfo, Comment: "nit"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictComment},
	}
	ro.adoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("cards %d want 1", len(ro.cards))
	}
	out, _ := ro.actSkipCurrent()
	ro = out.(*reviewOverlay)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("phase %v want phaseConfirmApprove", ro.phase)
	}
}

func TestSummaryPhaseOfferApproveWithoutSummaryNoInlineCards(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "", Line: 0, Severity: review.SeverityWarning, Comment: "general only"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	if ro.phase != phaseSummary {
		t.Fatalf("phase %v want phaseSummary (no postable inlines)", ro.phase)
	}
	if !ro.summaryPhaseOfferApproveWithoutSummary() {
		t.Fatal("expected approve-only offer with no inline cards and no session posts")
	}
}

func TestSummaryPhaseOfferApproveWithoutSummaryFalseAfterPostingInline(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
			{Specialist: review.SpecSecurity, Findings: []review.Finding{
				{Path: "b.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c2"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	ro.cards[0].state = cardPosted
	ro.idx = 1
	out, _ := ro.actSkipCurrent()
	ro = out.(*reviewOverlay)
	if ro.phase != phaseSummary {
		t.Fatalf("phase %v want phaseSummary", ro.phase)
	}
	if ro.summaryPhaseOfferApproveWithoutSummary() {
		t.Fatal("should not offer approve-only after posting an inline comment this session")
	}
}

func TestSummaryPhaseOfferApproveWhenAllSkippedOrOnPR(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	ro.cards[0].state = cardAlreadyOnPR
	ro.phase = phaseSummary
	if !ro.summaryPhaseOfferApproveWithoutSummary() {
		t.Fatal("expected offer when all findings already on PR (no new posts)")
	}
}

func TestReviewOverlayNoFindingsRoutesToConfirmApprove(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		// Vibe-coach with no prompts and a non-approve verdict — exactly the
		// case the screenshot illustrated, where the old flow dumped the user
		// onto a near-empty post-summary screen.
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictComment},
	}
	ro.adoptDraft(d)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("phase %v want phaseConfirmApprove", ro.phase)
	}
	if !ro.noFindingsApprove {
		t.Fatal("expected noFindingsApprove to be set")
	}
	body := ro.renderConfirmApproveBody()
	if !strings.Contains(body, "No issues found by any agent") {
		t.Fatalf("body should explain the no-findings approval: %s", body)
	}
	if strings.Contains(body, "comment-only review") {
		t.Fatalf("should not offer comment-only fallback when there's nothing to comment on: %s", body)
	}
}

func TestReviewOverlayNoFindingsConfirmApproveSwallowsN(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
	}
	ro.adoptDraft(d)
	if ro.phase != phaseConfirmApprove || !ro.noFindingsApprove {
		t.Fatalf("setup phase=%v noFindingsApprove=%v want phaseConfirmApprove + true", ro.phase, ro.noFindingsApprove)
	}
	out, _ := ro.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	ro = out.(*reviewOverlay)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("n should be a no-op in no-findings approve, phase=%v", ro.phase)
	}
}

func TestReviewOverlayNoFindingsHelpHidesCommentOnlyHint(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
	}
	ro.adoptDraft(d)
	help := ro.helpForPhase()
	if strings.Contains(help, "comment-only review") {
		t.Fatalf("help should not mention the comment-only fallback in no-findings mode: %q", help)
	}
	if !strings.Contains(help, "y APPROVE") {
		t.Fatalf("help should still describe the approve key: %q", help)
	}
}

func TestReviewOverlaySummaryBannerReflectsReconciledVerdict(t *testing.T) {
	// vibe-coach said request_changes based on a single warning-severity inline
	// finding. After the user skips it, no error/critical content remains so
	// PostEvent reconciles to COMMENT — and the summary banner should match.
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "nit"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges, Summary: "Block."},
	}
	ro.adoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("cards %d want 1", len(ro.cards))
	}
	out, _ := ro.actSkipCurrent()
	ro = out.(*reviewOverlay)
	// After skipping the only inline, advanceCard routes us to confirm-approve
	// (skipDisagree path). Force phaseSummary so we can assert what banner
	// the summary screen would render and that PostEvent reconciles.
	ro.phase = phaseSummary
	ro.syncUserSkipsToDraft()
	if ev := ro.draft.PostEvent(); ev != "COMMENT" {
		t.Fatalf("PostEvent = %q want COMMENT after reconciliation", ev)
	}
	body := ro.renderSummaryBody()
	if !strings.Contains(body, "Comment only") {
		t.Fatalf("summary banner should reflect reconciled Comment-only verdict: %s", body)
	}
}

func TestReviewOverlayPostOneSkipRestGoesToSummary(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
			{Specialist: review.SpecSecurity, Findings: []review.Finding{
				{Path: "b.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c2"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	ro.cards[0].state = cardPosted
	ro.idx = 1
	out, _ := ro.actSkipCurrent()
	ro = out.(*reviewOverlay)
	if ro.phase != phaseSummary {
		t.Fatalf("posted one skipped one: phase %v want phaseSummary", ro.phase)
	}
	if ro.approveAfterSkipDisagree {
		t.Fatal("did not expect approveAfterSkipDisagree")
	}
}
