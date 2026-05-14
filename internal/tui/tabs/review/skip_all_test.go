package review

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
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	if ro.phase != phaseApprove {
		t.Fatalf("phase %v want phaseApprove", ro.phase)
	}
	if len(ro.cards) != 2 {
		t.Fatalf("cards %d want 2", len(ro.cards))
	}
	out, _ := ro.actSkipCurrent()
	ro = out.(*Model)
	if ro.phase != phaseApprove {
		t.Fatalf("after one skip phase %v want phaseApprove", ro.phase)
	}
	out, _ = ro.actSkipCurrent()
	ro = out.(*Model)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("after skipping all, phase %v want phaseConfirmApprove", ro.phase)
	}
	if !ro.approveAfterSkipDisagree {
		t.Fatal("expected approveAfterSkipDisagree")
	}
}

func TestReviewOverlaySkipAllWithCommentVerdictOpensConfirmApprove(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("cards %d want 1", len(ro.cards))
	}
	out, _ := ro.actSkipCurrent()
	ro = out.(*Model)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("phase %v want phaseConfirmApprove", ro.phase)
	}
}

func TestSummaryPhaseOfferApproveWithoutSummaryNoInlineCards(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	if ro.phase != phaseSummary {
		t.Fatalf("phase %v want phaseSummary (no postable inlines)", ro.phase)
	}
	if !ro.summaryPhaseOfferApproveWithoutSummary() {
		t.Fatal("expected approve-only offer with no inline cards and no session posts")
	}
}

func TestSummaryPhaseOfferApproveWithoutSummaryFalseAfterPostingInline(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	ro.cards[0].state = cardPosted
	ro.idx = 1
	out, _ := ro.actSkipCurrent()
	ro = out.(*Model)
	if ro.phase != phaseSummary {
		t.Fatalf("phase %v want phaseSummary", ro.phase)
	}
	if ro.summaryPhaseOfferApproveWithoutSummary() {
		t.Fatal("should not offer approve-only after posting an inline comment this session")
	}
}

func TestSummaryPhaseOfferApproveWhenAllSkippedOrOnPR(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	ro.cards[0].state = cardAlreadyOnPR
	ro.phase = phaseSummary
	if !ro.summaryPhaseOfferApproveWithoutSummary() {
		t.Fatal("expected offer when all findings already on PR (no new posts)")
	}
}

func TestReviewOverlayNoFindingsRoutesToConfirmApprove(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
		// Vibe-coach with no prompts and a non-approve verdict — exactly the
		// case the screenshot illustrated, where the old flow dumped the user
		// onto a near-empty post-summary screen.
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictComment},
	}
	ro.AdoptDraft(d)
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
	ro := New(120, 44, false, false, false, nil)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
	}
	ro.AdoptDraft(d)
	if ro.phase != phaseConfirmApprove || !ro.noFindingsApprove {
		t.Fatalf("setup phase=%v noFindingsApprove=%v want phaseConfirmApprove + true", ro.phase, ro.noFindingsApprove)
	}
	out, _ := ro.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	ro = out.(*Model)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("n should be a no-op in no-findings approve, phase=%v", ro.phase)
	}
}

// TestNoFindingsConfirmApproveRendersApproveOnlyButton locks in the contract
// that the no-findings auto-approve screen offers two paths: "Approve PR (y)"
// which attaches the rendered "no issues found by any agent" body, and
// "Approve only (a)" which posts APPROVE with no body. Before this option
// existed the user could only post APPROVE with the AI-authored recap; now
// they can opt out of publishing any review text alongside the approval.
func TestNoFindingsConfirmApproveRendersApproveOnlyButton(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
	}
	ro.AdoptDraft(d)
	if ro.phase != phaseConfirmApprove || !ro.noFindingsApprove {
		t.Fatalf("setup phase=%v noFindingsApprove=%v want phaseConfirmApprove + true", ro.phase, ro.noFindingsApprove)
	}
	body := ro.renderConfirmApproveBody()
	if !strings.Contains(body, "Approve PR (y)") {
		t.Fatalf("no-findings confirm should still render the body-attached approve button: %s", body)
	}
	if !strings.Contains(body, "Approve only (a)") {
		t.Fatalf("no-findings confirm must render the bare-approve button: %s", body)
	}
	if !strings.Contains(body, "no comment body") {
		t.Fatalf("no-findings confirm should explain the approve-only path: %s", body)
	}
	help := ro.helpForPhase()
	if !strings.Contains(help, "APPROVE without body") {
		t.Fatalf("phaseConfirmApprove help line should describe the bare approve option: %q", help)
	}
}

// TestNoFindingsConfirmApproveAKeyTriggersBareApprove verifies that 'a' in
// the no-findings auto-approve flow dispatches a command (the bare-body
// approve) rather than being swallowed like 'n' is. The phase stays in
// phaseConfirmApprove until the post completes — matching how 'y' behaves
// here today.
func TestNoFindingsConfirmApproveAKeyTriggersBareApprove(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
	}
	ro.AdoptDraft(d)
	if ro.phase != phaseConfirmApprove || !ro.noFindingsApprove {
		t.Fatalf("setup phase=%v noFindingsApprove=%v want phaseConfirmApprove + true", ro.phase, ro.noFindingsApprove)
	}
	out, cmd := ro.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	ro = out.(*Model)
	if cmd == nil {
		t.Fatal("pressing 'a' at phaseConfirmApprove with noFindingsApprove should dispatch a bare-approve command")
	}
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("phase should remain phaseConfirmApprove until the post completes, got %v", ro.phase)
	}
}

// TestRegularConfirmApproveAKeyIgnored makes sure the bare-approve key only
// has an effect in the no-findings flow. In the regular phaseConfirmApprove
// branch (verdict was APPROVE because the user skipped every blocker, or the
// AI verdict was APPROVE outright) the 'y' button already posts APPROVE with
// no body, so 'a' would be a redundant alias and the screen deliberately
// keeps its single-button contract.
func TestRegularConfirmApproveAKeyIgnored(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	out, _ := ro.actSkipCurrent()
	ro = out.(*Model)
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("setup phase=%v want phaseConfirmApprove (skip-disagree path)", ro.phase)
	}
	if ro.noFindingsApprove {
		t.Fatal("setup must NOT have noFindingsApprove for this test")
	}
	out, cmd := ro.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	ro = out.(*Model)
	if cmd != nil {
		t.Fatal("'a' must be a no-op outside the no-findings auto-approve flow")
	}
	if ro.phase != phaseConfirmApprove {
		t.Fatalf("phase should remain phaseConfirmApprove, got %v", ro.phase)
	}
}

func TestReviewOverlayNoFindingsHelpHidesCommentOnlyHint(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: skipAllTestDiff,
	}
	ro.AdoptDraft(d)
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
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("cards %d want 1", len(ro.cards))
	}
	out, _ := ro.actSkipCurrent()
	ro = out.(*Model)
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
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	ro.cards[0].state = cardPosted
	ro.idx = 1
	out, _ := ro.actSkipCurrent()
	ro = out.(*Model)
	if ro.phase != phaseSummary {
		t.Fatalf("posted one skipped one: phase %v want phaseSummary", ro.phase)
	}
	if ro.approveAfterSkipDisagree {
		t.Fatal("did not expect approveAfterSkipDisagree")
	}
}

// TestSummaryPhaseAlwaysOffersApproveOnlyButton locks in the contract that
// the "Approve only" button is rendered at phaseSummary regardless of how
// the user got there — including the case where they posted inline
// comments this session, which the previous gate
// (summaryPhaseOfferApproveWithoutSummary) treated as disqualifying.
//
// The user-facing principle is that the GitHub APPROVE signal always
// represents the human reviewer's own judgement, so the option to
// approve must always be reachable from the final review screen.
func TestSummaryPhaseAlwaysOffersApproveOnlyButton(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	ro.cards[0].state = cardPosted
	ro.idx = 1
	out, _ := ro.actSkipCurrent()
	ro = out.(*Model)
	if ro.phase != phaseSummary {
		t.Fatalf("phase %v want phaseSummary", ro.phase)
	}
	if ro.summaryPhaseOfferApproveWithoutSummary() {
		t.Fatal("contextual nudge should still be off after a session post — guards the suggestion text only")
	}
	if !ro.summaryPhaseAllowApproveOnly() {
		t.Fatal("Approve only button must remain available even after posting an inline comment this session")
	}
	body := ro.renderSummaryBody()
	if !strings.Contains(body, "Approve only (a)") {
		t.Fatalf("rendered summary body should include the Approve only button: %s", body)
	}
	help := ro.helpForPhase()
	if !strings.Contains(help, "approve only") && !strings.Contains(help, "approve without summary") {
		t.Fatalf("phaseSummary help line should always mention approving: %q", help)
	}
}

// TestSummaryPhaseApproveOnlyKeySendsApprove verifies that pressing 'a' at
// phaseSummary triggers actPostApprove (which queues a GitHub APPROVE)
// even after the user has posted an inline comment this session — the
// gate that previously swallowed the keypress in that state has been
// loosened to summaryPhaseAllowApproveOnly.
func TestSummaryPhaseApproveOnlyKeySendsApprove(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
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
	ro.AdoptDraft(d)
	ro.cards[0].state = cardPosted
	ro.phase = phaseSummary
	out, cmd := ro.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	ro = out.(*Model)
	if cmd == nil {
		t.Fatal("pressing 'a' at phaseSummary after a session post should still trigger an APPROVE command")
	}
	if ro.phase != phaseSummary {
		t.Fatalf("phase should remain phaseSummary until the post completes, got %v", ro.phase)
	}
}

// TestSummaryPhaseAllowApproveOnlyDisabledInPeruse keeps peruse mode
// strictly read-only — even though the button is otherwise always
// available, peruse must never expose an action that posts to GitHub.
func TestSummaryPhaseAllowApproveOnlyDisabledInPeruse(t *testing.T) {
	ro := New(120, 44, false, false, false, nil)
	ro.peruse = true
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
	ro.AdoptDraft(d)
	ro.phase = phaseSummary
	if ro.summaryPhaseAllowApproveOnly() {
		t.Fatal("peruse mode must never allow Approve only — read-only walkthrough should not expose post actions")
	}
	body := ro.renderSummaryBody()
	if strings.Contains(body, "Approve only (a)") {
		t.Fatalf("peruse summary body should not render the Approve only button: %s", body)
	}
}
