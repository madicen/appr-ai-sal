package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// init initialises bubblezone once for this test file. The overlay
// renders use zone.Mark in renderSummaryBody / renderApprovalBody, and
// without a global manager those panic. zone.NewGlobal is idempotent
// in practice (and other tests in the package call it the same way).
func init() { zone.NewGlobal() }

// The vibe-coach LLM call is deferred to the approve→summary transition
// so its Summary/Prompts/Verdict reflect the user's final skip set.
// enterSummary is the single entry point that decides whether a refresh
// is needed; these tests pin the routing decisions without requiring a
// live LLM.

const deferTestDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1 @@
+a
`

func newDeferTestDraft(t *testing.T) *review.Draft {
	t.Helper()
	return &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: deferTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictComment, Summary: "stub"},
	}
}

// With no aiConfig (test default), enterSummary lands directly in
// phaseSummary without scheduling an LLM call — this is the path tests
// exercise today and must keep working.
func TestEnterSummaryWithoutAIConfigGoesDirectlyToSummary(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.adoptDraft(newDeferTestDraft(t))
	if ro.phase != phaseApprove {
		t.Fatalf("preconditions: phase %v, want phaseApprove", ro.phase)
	}
	cmd := ro.enterSummary()
	if cmd != nil {
		t.Errorf("expected nil cmd (no aiConfig → no LLM call), got non-nil")
	}
	if ro.phase != phaseSummary {
		t.Errorf("phase %v, want phaseSummary", ro.phase)
	}
}

// enterSummary always syncs the user's skips onto the draft before
// deciding what to do. This is what makes the LLM see the post-skip
// finding set when vibe-coach runs lazily — and what guarantees the
// rendered body / verdict reconciliation use the same set.
func TestEnterSummarySyncsSkipsBeforeRouting(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.adoptDraft(newDeferTestDraft(t))
	if len(ro.cards) != 1 {
		t.Fatalf("preconditions: cards %d, want 1", len(ro.cards))
	}
	ro.cards[0].state = cardSkipped
	if len(ro.draft.UserSkipPostKeys) != 0 {
		t.Fatalf("preconditions: draft.UserSkipPostKeys should still be nil before enterSummary; got %d entries",
			len(ro.draft.UserSkipPostKeys))
	}
	ro.enterSummary()
	if len(ro.draft.UserSkipPostKeys) != 1 {
		t.Errorf("expected exactly 1 skip synced onto the draft, got %d", len(ro.draft.UserSkipPostKeys))
	}
}

// Re-entry to summary with an unchanged skip set must reuse the cached
// VibeCoach rather than schedule a fresh LLM call. This is the common
// case: user backs out of summary to look at a card and re-enters
// without changing anything.
func TestEnterSummaryReusesCachedVibeCoachOnIdenticalSkipSet(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	d := newDeferTestDraft(t)
	ro.adoptDraft(d)
	// First entry caches the hash + draft.VibeCoach. With nil aiConfig
	// adoptDraft + enterSummary land us straight in phaseSummary.
	ro.enterSummary()
	if ro.phase != phaseSummary {
		t.Fatalf("first enterSummary phase %v, want phaseSummary", ro.phase)
	}
	hashBefore := ro.lastCoachHash
	// Second entry with no skip changes — should still land directly
	// in phaseSummary, no LLM cmd issued.
	cmd := ro.enterSummary()
	if cmd != nil {
		t.Errorf("unchanged skip set should not re-issue vibe-coach")
	}
	if ro.lastCoachHash != hashBefore {
		t.Errorf("lastCoachHash should be stable across same-skip re-entries; before=%q after=%q", hashBefore, ro.lastCoachHash)
	}
}

// vibeCoachDoneMsg with a stale hash must be ignored. Without this
// guard, a slow first call completing after the user has changed skips
// would overwrite the result of a newer (post-skip) call.
func TestVibeCoachDoneMsgStaleHashIsIgnored(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	d := newDeferTestDraft(t)
	ro.adoptDraft(d)
	ro.coachInFlight = true
	ro.lastCoachHash = "current-hash"
	// Pretend the user has since skipped something and the draft
	// reflects that.
	ro.draft.UserSkipPostKeys = map[string]struct{}{"some-key": {}}
	// The receiving handler reads the *current* skip-set hash, not
	// lastCoachHash, so we compute it the way the handler does.
	curHash := skipSetHash(ro.draft.UserSkipPostKeys)
	if curHash == "stale" {
		t.Fatal("test setup error: hash collision")
	}
	stale := &review.VibeCoachResult{Verdict: review.VibeVerdictApprove, Summary: "stale"}
	out, _ := ro.Update(vibeCoachDoneMsg{result: stale, atSkipHash: "stale"})
	ro = out.(*reviewOverlay)
	// Stale completion → re-issue (which sets coachInFlight again or
	// lands directly in phaseSummary if no aiConfig). Either way the
	// stale Summary should not have clobbered the existing one.
	if ro.draft.VibeCoach != nil && ro.draft.VibeCoach.Summary == "stale" {
		t.Errorf("stale completion clobbered draft.VibeCoach; summary now %q", ro.draft.VibeCoach.Summary)
	}
}

// Non-stale vibeCoachDoneMsg installs the result on the draft, clears
// coachInFlight, and routes to phaseSummary.
func TestVibeCoachDoneMsgInstallsFreshResult(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	d := newDeferTestDraft(t)
	ro.adoptDraft(d)
	ro.phase = phaseGeneratingSummary
	ro.coachInFlight = true
	curHash := skipSetHash(ro.draft.UserSkipPostKeys)
	fresh := &review.VibeCoachResult{Verdict: review.VibeVerdictApprove, Summary: "fresh"}
	out, _ := ro.Update(vibeCoachDoneMsg{result: fresh, atSkipHash: curHash})
	ro = out.(*reviewOverlay)
	if ro.coachInFlight {
		t.Errorf("coachInFlight should be cleared on done-msg")
	}
	if ro.draft.VibeCoach == nil || ro.draft.VibeCoach.Summary != "fresh" {
		t.Errorf("draft.VibeCoach not installed; got %+v", ro.draft.VibeCoach)
	}
	if ro.phase != phaseSummary {
		t.Errorf("phase %v, want phaseSummary", ro.phase)
	}
	if ro.lastCoachHash != curHash {
		t.Errorf("lastCoachHash %q, want %q", ro.lastCoachHash, curHash)
	}
}

// Peruse mode: pressing y on a finding card is a no-op. The card state
// must NOT flip to posted; the help line flashes a hint about peruse.
func TestPeruseModeBlocksPost(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.peruse = true
	ro.adoptDraft(newDeferTestDraft(t))
	if ro.phase != phaseApprove {
		t.Fatalf("preconditions: phase %v, want phaseApprove", ro.phase)
	}
	if len(ro.cards) != 1 {
		t.Fatalf("preconditions: cards %d, want 1", len(ro.cards))
	}
	out, cmd := ro.actPostCurrent()
	ro = out.(*reviewOverlay)
	if cmd != nil {
		t.Errorf("expected no cmd from peruse-blocked post, got non-nil")
	}
	if ro.cards[0].state == cardPosted {
		t.Errorf("peruse should not mark the card as posted")
	}
	if !strings.Contains(ro.peruseHint, "peruse") {
		t.Errorf("expected peruse hint, got %q", ro.peruseHint)
	}
}

// Peruse mode: pressing s/n on a finding card is a no-op. The card
// state must NOT flip to skipped; the help line flashes a hint.
func TestPeruseModeBlocksSkip(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.peruse = true
	ro.adoptDraft(newDeferTestDraft(t))
	out, cmd := ro.actSkipCurrent()
	ro = out.(*reviewOverlay)
	if cmd != nil {
		t.Errorf("expected no cmd from peruse-blocked skip, got non-nil")
	}
	if ro.cards[0].state == cardSkipped {
		t.Errorf("peruse should not mark the card as skipped")
	}
	if !strings.Contains(ro.peruseHint, "peruse") {
		t.Errorf("expected peruse hint, got %q", ro.peruseHint)
	}
}

// Peruse mode: pressing y on the summary phase is a no-op. The user
// should never accidentally post a peruse-only review.
func TestPeruseModeBlocksPostSummary(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.peruse = true
	ro.adoptDraft(newDeferTestDraft(t))
	out, cmd := ro.actPostSummary()
	ro = out.(*reviewOverlay)
	if cmd != nil {
		t.Errorf("expected no cmd from peruse-blocked post-summary, got non-nil")
	}
	if !strings.Contains(ro.peruseHint, "peruse") {
		t.Errorf("expected peruse hint, got %q", ro.peruseHint)
	}
}

// Peruse-mode title carries a "PERUSE" prefix so the user can never
// mistake which mode they're in.
func TestPeruseModeTitlePrefix(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.peruse = true
	for _, p := range []overlayPhase{phaseRunning, phaseApprove, phaseGeneratingSummary, phaseSummary, phasePosted} {
		ro.phase = p
		if !strings.HasPrefix(ro.titleForPhase(), "PERUSE") {
			t.Errorf("phase %v: title missing PERUSE prefix: %q", p, ro.titleForPhase())
		}
	}
}

// Peruse-mode help text mentions "no posting" so the user knows their
// keys are intentionally disabled.
func TestPeruseModeHelpMentionsNoPosting(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.peruse = true
	for _, p := range []overlayPhase{phaseRunning, phaseApprove, phaseSummary, phaseConfirmApprove} {
		ro.phase = p
		h := ro.helpForPhase()
		if !strings.Contains(h, "peruse") {
			t.Errorf("phase %v: help line missing peruse marker: %q", p, h)
		}
	}
}

// Generating-summary phase has its own help text so the user knows
// they're waiting on the LLM, not stuck.
func TestGeneratingSummaryHelpText(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	ro.phase = phaseGeneratingSummary
	h := ro.helpForPhase()
	if !strings.Contains(h, "refining summary") {
		t.Errorf("phase generating-summary: help line missing 'refining summary' marker: %q", h)
	}
	if !strings.Contains(h, "q abort") {
		t.Errorf("phase generating-summary: help line missing abort affordance: %q", h)
	}
}

// skipSetHash is stable across map iteration order and round-trips
// the empty-set marker.
func TestSkipSetHashStable(t *testing.T) {
	if h := skipSetHash(nil); h != "" {
		t.Errorf("nil skip set should hash to empty string, got %q", h)
	}
	if h := skipSetHash(map[string]struct{}{}); h != "" {
		t.Errorf("empty skip set should hash to empty string, got %q", h)
	}
	a := skipSetHash(map[string]struct{}{"x": {}, "y": {}, "z": {}})
	b := skipSetHash(map[string]struct{}{"z": {}, "y": {}, "x": {}})
	if a == "" || a != b {
		t.Errorf("hash should be order-independent and non-empty, got a=%q b=%q", a, b)
	}
	c := skipSetHash(map[string]struct{}{"x": {}, "y": {}})
	if c == a {
		t.Errorf("different skip sets should hash differently, got %q == %q", c, a)
	}
}

// Ensure typed assertion of tea.Cmd usage compiles when reading the
// peruse / done-msg messages; doc-only guard so the test file's
// imports stay used if other tests are reworked.
var _ tea.Cmd = func() tea.Msg { return nil }
