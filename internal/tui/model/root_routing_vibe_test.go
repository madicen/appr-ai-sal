package model

import (
	"testing"

	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
)

// The TUI's lazy vibe-coach re-run (kicked off when the user changed
// skips between approve and summary) is driven by two custom messages
// that fly between a goroutine and the overlay: data.StagedFindingPostedMsg
// (which triggers the approve→summary transition after the last card
// is posted) and reviewtab.VibeCoachDoneMsg (the goroutine's response).
// Both flow THROUGH the root Model.Update — which historically called
// `ro.Update(msg)` and discarded the returned tea.Cmd, OR didn't have
// a case for the message at all.
//
// The user-visible symptom was the overlay flipping to
// PhaseGeneratingSummary ("Refining summary with your final selections…")
// and never advancing — the cmd that would have kicked off the LLM
// goroutine was dropped on the floor by the root forwarder. These tests
// pin the routing so neither cmd is lost again.

// rootRoutingTestDiff is a one-file unified diff with two added lines so
// the overlay can anchor a finding at line 1. Inlined here (rather than
// imported from the review package) because the overlay tests live in
// their own package now.
const rootRoutingTestDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1,2 @@
+a
+b
`

// rootRoutingTestDraft builds a minimal draft with exactly one inline
// finding so the overlay flips into PhaseApprove with a single card —
// just enough for data.StagedFindingPostedMsg to walk through advanceCard
// → enterSummary.
func rootRoutingTestDraft() *review.Draft {
	return &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: rootRoutingTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
		},
	}
}

// newRootRoutingTestModel constructs a Model with an aiConfig (required for
// enterSummary to schedule the LLM goroutine) and a review overlay pushed
// onto the modal stack with the draft already adopted.
func newRootRoutingTestModel(t *testing.T) (*Model, *reviewtab.Model) {
	t.Helper()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.width = 120
	m.height = 44
	ro := reviewtab.New(m.width, m.height, false, false, false, m.opts.AIConfig, false)
	ro.AdoptDraft(rootRoutingTestDraft())
	m.overlayStack.Push(ro, overlay.DefaultOverlayConfig())
	m.currentReviewOverlay = ro
	return m, ro
}

// data.StagedFindingPostedMsg routed through the root Update must reach
// the overlay handler so the focused finding (in its agent tab) is marked
// posted. The root forwards the overlay's returned cmd unconditionally.
func TestRootRoutesStagedFindingPostedMarksCardPosted(t *testing.T) {
	m, ro := newRootRoutingTestModel(t)
	ro.SelectAgentTab(review.SpecDocs)
	if ro.Phase() != reviewtab.PhaseApprove {
		t.Fatalf("preconditions: phase %v, want PhaseApprove (agent tab)", ro.Phase())
	}
	if ro.CardCount() != 1 {
		t.Fatalf("preconditions: cards %d, want 1", ro.CardCount())
	}

	_, _ = m.Update(data.StagedFindingPostedMsg{})
	if ro.CardStateAt(0) != reviewtab.CardPosted {
		t.Errorf("expected the focused finding to be marked posted, got %v", ro.CardStateAt(0))
	}
}

// A reviewtab.VibeCoachDoneMsg arriving with a stale AtSkipHash makes the
// overlay re-issue (it returns m.enterSummary() = runVibeCoachCmd). The
// root Update must have a case for VibeCoachDoneMsg AND forward that cmd —
// pre-fix there was no case at all, so the message fell through and the
// goroutine response was silently dropped.
func TestRootRoutesVibeCoachDoneMsgAndForwardsRetryCmd(t *testing.T) {
	m, ro := newRootRoutingTestModel(t)
	// Simulate "vibe-coach already in flight against an old skip set".
	ro.SetForcedPhase(reviewtab.PhaseGeneratingSummary)
	ro.SetForcedCoachInFlight(true)
	ro.Draft().UserSkipPostKeys = map[string]struct{}{"o/r#1|a.go|1|RIGHT": {}}

	stale := &review.VibeCoachResult{Verdict: review.VibeVerdictComment, Summary: "stale"}
	_, cmd := m.Update(reviewtab.VibeCoachDoneMsg{Result: stale, AtSkipHash: "definitely-different"})
	if cmd == nil {
		t.Fatalf("root Update did not forward the stale-completion retry cmd; UI would hang on PhaseGeneratingSummary")
	}
	if !ro.CoachInFlight() {
		t.Errorf("expected CoachInFlight()=true after stale-completion re-issue")
	}
}

// Non-stale VibeCoachDoneMsg installs the result and flips into
// PhaseSummary. Root routing must reach the overlay handler at all —
// without the explicit case, this message bypasses it entirely.
func TestRootRoutesVibeCoachDoneMsgFreshResultInstallsAndAdvances(t *testing.T) {
	m, ro := newRootRoutingTestModel(t)
	ro.SetForcedPhase(reviewtab.PhaseGeneratingSummary)
	ro.SetForcedCoachInFlight(true)
	ro.Draft().UserSkipPostKeys = nil
	curHash := reviewtab.SkipSetHash(ro.Draft().UserSkipPostKeys)

	fresh := &review.VibeCoachResult{
		Verdict: review.VibeVerdictApprove,
		Summary: "LGTM",
	}
	_, _ = m.Update(reviewtab.VibeCoachDoneMsg{Result: fresh, AtSkipHash: curHash})
	if ro.CoachInFlight() {
		t.Errorf("expected CoachInFlight()=false after fresh-result install, still true")
	}
	if ro.Phase() != reviewtab.PhaseSummary {
		t.Errorf("expected phase=PhaseSummary, got %v", ro.Phase())
	}
	if ro.Draft().VibeCoach == nil || ro.Draft().VibeCoach.Summary != "LGTM" {
		t.Errorf("expected the fresh VibeCoachResult to be installed on the draft")
	}
}
