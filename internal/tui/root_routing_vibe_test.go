package tui

import (
	"testing"

	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// The deferred vibe-coach feature is driven by two custom messages that
// fly between a goroutine and the overlay: stagedFindingPostedMsg (which
// triggers the approve→summary transition after the last card is posted)
// and vibeCoachDoneMsg (the goroutine's response). Both flow THROUGH the
// root Model.Update — which historically called `ro.Update(msg)` and
// discarded the returned tea.Cmd, OR didn't have a case for the message
// at all.
//
// The user-visible symptom was the overlay flipping to
// phaseGeneratingSummary ("Refining summary with your final selections…")
// and never advancing — the cmd that would have kicked off the LLM
// goroutine was dropped on the floor by the root forwarder. These tests
// pin the routing so neither cmd is lost again.

// rootRoutingTestDraft builds a minimal draft with exactly one inline
// finding so the overlay flips into phaseApprove with a single card —
// just enough for stagedFindingPostedMsg to walk through advanceCard
// → enterSummary.
func rootRoutingTestDraft() *review.Draft {
	return &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: deferTestDiff,
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
func newRootRoutingTestModel(t *testing.T) (*Model, *reviewOverlay) {
	t.Helper()
	m := New(Options{AIConfig: aiconfig.DefaultConfig()})
	m.width = 120
	m.height = 44
	ro := newReviewOverlay(m.width, m.height, false, false, false, m.opts.AIConfig)
	ro.adoptDraft(rootRoutingTestDraft())
	m.overlayStack.Push(ro, overlay.DefaultOverlayConfig())
	m.currentReviewOverlay = ro
	return m, ro
}

// stagedFindingPostedMsg arriving for the LAST card must walk through the
// overlay's advanceCard → enterSummary chain AND have its returned cmd
// (the runVibeCoachCmd closure) propagated by the root Update. Without
// the propagation the overlay sits in phaseGeneratingSummary with
// coachInFlight=true but no goroutine is ever scheduled.
func TestRootForwardsStagedFindingPostedCmdSoVibeCoachActuallyRuns(t *testing.T) {
	m, ro := newRootRoutingTestModel(t)
	if ro.phase != phaseApprove {
		t.Fatalf("preconditions: phase %v, want phaseApprove", ro.phase)
	}
	if len(ro.cards) != 1 {
		t.Fatalf("preconditions: cards %d, want 1", len(ro.cards))
	}

	// Mark the only card as posted so advanceCard's "are we done?"
	// branch fires when stagedFindingPostedMsg arrives.
	ro.cards[0].state = cardPending

	_, cmd := m.Update(stagedFindingPostedMsg{})
	if cmd == nil {
		t.Fatalf("root Update dropped the runVibeCoachCmd from the overlay; UI would hang on phaseGeneratingSummary")
	}
	if ro.phase != phaseGeneratingSummary {
		t.Errorf("expected overlay in phaseGeneratingSummary, got %v", ro.phase)
	}
	if !ro.coachInFlight {
		t.Errorf("expected coachInFlight=true after enterSummary scheduled the goroutine")
	}
}

// A vibeCoachDoneMsg arriving with a stale atSkipHash makes the overlay
// re-issue (it returns m.enterSummary() = runVibeCoachCmd). The root
// Update must have a case for vibeCoachDoneMsg AND forward that cmd —
// pre-fix there was no case at all, so the message fell through and the
// goroutine response was silently dropped.
func TestRootRoutesVibeCoachDoneMsgAndForwardsRetryCmd(t *testing.T) {
	m, ro := newRootRoutingTestModel(t)
	// Simulate "vibe-coach already in flight against an old skip set".
	ro.phase = phaseGeneratingSummary
	ro.coachInFlight = true
	ro.draft.UserSkipPostKeys = map[string]struct{}{"o/r#1|a.go|1|RIGHT": {}}

	stale := &review.VibeCoachResult{Verdict: review.VibeVerdictComment, Summary: "stale"}
	_, cmd := m.Update(vibeCoachDoneMsg{result: stale, atSkipHash: "definitely-different"})
	if cmd == nil {
		t.Fatalf("root Update did not forward the stale-completion retry cmd; UI would hang on phaseGeneratingSummary")
	}
	if !ro.coachInFlight {
		t.Errorf("expected coachInFlight=true after stale-completion re-issue")
	}
}

// Non-stale vibeCoachDoneMsg installs the result and flips into
// phaseSummary. Root routing must reach the overlay handler at all —
// without the explicit case, this message bypasses it entirely.
func TestRootRoutesVibeCoachDoneMsgFreshResultInstallsAndAdvances(t *testing.T) {
	m, ro := newRootRoutingTestModel(t)
	ro.phase = phaseGeneratingSummary
	ro.coachInFlight = true
	ro.draft.UserSkipPostKeys = nil
	curHash := skipSetHash(ro.draft.UserSkipPostKeys)

	fresh := &review.VibeCoachResult{
		Verdict: review.VibeVerdictApprove,
		Summary: "LGTM",
	}
	_, _ = m.Update(vibeCoachDoneMsg{result: fresh, atSkipHash: curHash})
	if ro.coachInFlight {
		t.Errorf("expected coachInFlight=false after fresh-result install, still true")
	}
	if ro.phase != phaseSummary {
		t.Errorf("expected phase=phaseSummary, got %v", ro.phase)
	}
	if ro.draft.VibeCoach == nil || ro.draft.VibeCoach.Summary != "LGTM" {
		t.Errorf("expected the fresh VibeCoachResult to be installed on the draft")
	}
}
