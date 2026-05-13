package tui

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// reanchorDiff has a single hunk covering post-image lines 100-104, with
// the distinctive excerpt landing at post-image line 101. A finding
// emitted with Line: 2 is therefore comfortably OUTSIDE any hunk — the
// direct HunkAroundLine path returns nil — but the model's anchor_excerpt
// still uniquely matches line 101 so anchorCardToDiff has a clean
// excerpt-based relocation target.
const reanchorDiff = `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -100,4 +100,5 @@ ctx
 padding line 100
-old line 101 was removed
+the line the model originally pointed at lived here at line 2
 padding line 102
 padding line 103
`

// TestAdoptDraftReanchorsViaAnchorExcerpt locks in the new posture: when
// the finding's Line falls outside every hunk in the parsed diff but the
// model's AnchorExcerpt uniquely matches a different line in the same
// file, adoptDraft mutates Finding.Line to the matched line, records the
// original in approvalCard.anchorRelocatedFrom, and sets the card's hunk
// so the inline post path can fire on the next y.
func TestAdoptDraftReanchorsViaAnchorExcerpt(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	excerpt := "the line the model originally pointed at lived here at line 2"
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: reanchorDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{
					Path:          "x.go",
					Line:          2, // gone from the new diff
					Side:          "RIGHT",
					Severity:      review.SeverityWarning,
					Comment:       "anchor me on excerpt",
					AnchorExcerpt: excerpt,
				},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("cards %d want 1", len(ro.cards))
	}
	c := &ro.cards[0]
	if c.hunk == nil {
		t.Fatalf("expected re-anchor to land the card on a hunk")
	}
	if c.anchorRelocatedFrom != 2 {
		t.Errorf("anchorRelocatedFrom %d want 2", c.anchorRelocatedFrom)
	}
	if c.finding.Finding.Line != 101 {
		t.Errorf("finding.Line %d want 101 (the new post-image line for the excerpt)", c.finding.Finding.Line)
	}
}

// TestAdoptDraftSkipsReanchorWhenLineAlreadyAnchors guards the common
// case: when the finding's original Line already lands on a hunk, we do
// NOT search for excerpt matches — the model nailed it on the first try
// and a silent move would be unsettling and pointless.
func TestAdoptDraftSkipsReanchorWhenLineAlreadyAnchors(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: reanchorDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{
					Path:          "x.go",
					Line:          101, // already on the hunk
					Side:          "RIGHT",
					Severity:      review.SeverityWarning,
					Comment:       "no relocation needed",
					AnchorExcerpt: "the line the model originally pointed at lived here at line 2",
				},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	c := &ro.cards[0]
	if c.anchorRelocatedFrom != 0 {
		t.Errorf("anchorRelocatedFrom should stay 0 when the original line anchors, got %d", c.anchorRelocatedFrom)
	}
	if c.finding.Finding.Line != 101 {
		t.Errorf("finding.Line should be untouched, got %d", c.finding.Finding.Line)
	}
}

// TestAdoptDraftDoesNotReanchorWithoutExcerpt covers the no-AnchorExcerpt
// branch: older runs or backends that strip unknown JSON keys produce
// findings with an empty AnchorExcerpt. We must not invent a relocation
// in that case — the card stays unanchored and the file-level fallback /
// refresh path takes over.
func TestAdoptDraftDoesNotReanchorWithoutExcerpt(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: reanchorDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{
					Path:     "x.go",
					Line:     2,
					Side:     "RIGHT",
					Severity: review.SeverityWarning,
					Comment:  "no excerpt to relocate by",
				},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	c := &ro.cards[0]
	if c.hunk != nil {
		t.Error("no AnchorExcerpt + line not on any hunk → card.hunk must stay nil")
	}
	if c.anchorRelocatedFrom != 0 {
		t.Errorf("anchorRelocatedFrom should stay 0, got %d", c.anchorRelocatedFrom)
	}
}

// TestApplyPRRefreshReanchorsViaAnchorExcerpt mirrors the adoptDraft test
// for the refresh path. After R, applyPRRefresh re-runs anchorCardToDiff
// against the new files, and a stale card whose Line is gone but whose
// AnchorExcerpt still matches must be silently moved (with the banner
// hint) rather than left in cardError.
func TestApplyPRRefreshReanchorsViaAnchorExcerpt(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	excerpt := "the line the model originally pointed at lived here at line 2"
	// Start with an OLD diff whose hunk covers line 2 so the card
	// anchors fine before the refresh. Use a long excerpt at line 2 so
	// the post-refresh search has something to match against the new
	// diff.
	startDiff := `diff --git a/x.go b/x.go
--- /dev/null
+++ b/x.go
@@ -0,0 +1,3 @@
+package x
+the line the model originally pointed at lived here at line 2
+func F() {}
`
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "old1234", Owner: "o", Repo: "r"},
		Diff: startDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{
					Path:          "x.go",
					Line:          2,
					Side:          "RIGHT",
					Severity:      review.SeverityWarning,
					Comment:       "should follow the excerpt across the force-push",
					AnchorExcerpt: excerpt,
				},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	if ro.cards[0].hunk == nil {
		t.Fatalf("test setup: card should anchor on the old diff at line 2")
	}
	// Simulate a force-push that re-paginated the file dramatically: the
	// excerpt now lives at line 101 in a hunk that doesn't cover line 2.
	// The TUI runs applyPRRefresh with the new diff string.
	freshPR := &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "new5678", Owner: "o", Repo: "r"}
	ro.applyPRRefresh(freshPR, reanchorDiff)

	c := &ro.cards[0]
	if c.hunk == nil {
		t.Fatalf("refresh should have re-anchored the card via AnchorExcerpt; hunk is nil")
	}
	if c.anchorRelocatedFrom != 2 {
		t.Errorf("anchorRelocatedFrom %d want 2", c.anchorRelocatedFrom)
	}
	if c.finding.Finding.Line != 101 {
		t.Errorf("finding.Line %d want 101 after relocation", c.finding.Finding.Line)
	}
	if c.state != cardPending {
		t.Errorf("re-anchored card should be back to cardPending, got %v", c.state)
	}
}

// TestRenderApprovalBodyShowsAnchorBanner verifies the user-visible
// signal: when a card was auto-relocated, the approval body emits a
// "Anchor auto-corrected from N → M" banner so the reviewer can spot-check
// the new line before pressing y.
func TestRenderApprovalBodyShowsAnchorBanner(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	excerpt := "the line the model originally pointed at lived here at line 2"
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: reanchorDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{
					Path:          "x.go",
					Line:          2,
					Side:          "RIGHT",
					Severity:      review.SeverityWarning,
					Comment:       "anchor banner check",
					AnchorExcerpt: excerpt,
				},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.adoptDraft(d)
	body := ro.renderApprovalBody()
	if !strings.Contains(body, "Anchor auto-corrected") {
		t.Errorf("expected anchor-relocation banner in approval body, got:\n%s", body)
	}
	if !strings.Contains(body, "line 2") || !strings.Contains(body, "→ 101") {
		t.Errorf("banner should name both the old (2) and new (101) lines, got:\n%s", body)
	}
}
