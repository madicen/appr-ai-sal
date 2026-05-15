package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

const refreshTestOldDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1,2 @@
+a
+b
`

// refreshTestNewDiff covers two cases for the same PR after a force-push:
//   - line 1 still exists (the finding originally pointing at a.go:1 stays
//     anchored to the new diff).
//   - the previously-anchored line in c.go has been deleted entirely (the
//     finding pointing at c.go:5 should fail to re-anchor).
const refreshTestNewDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1,2 @@
+a
+b
diff --git a/c.go b/c.go
--- a/c.go
+++ b/c.go
@@ -1,3 +1,3 @@
 unchanged-1
-removed-line
+added-line
 unchanged-2
`

// TestActPostCurrent_LocalPreflight_NoHunkSetsCardError verifies that when a
// finding's anchor line isn't on a hunk in the current parsed diff, the
// overlay rejects the post locally instead of letting GitHub return the
// opaque "pull_request_review_thread.line could not be resolved" 422.
func TestActPostCurrent_LocalPreflight_NoHunkSetsCardError(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: refreshTestOldDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 99, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "out of range"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.AdoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("cards %d want 1", len(ro.cards))
	}
	if ro.cards[0].hunk != nil {
		t.Fatal("test setup invariant: line 99 must not be on any hunk in the old diff")
	}
	if _, _ = ro.actPostCurrent(); ro.cards[0].state != cardError {
		t.Errorf("want cardError after local pre-flight, got %v", ro.cards[0].state)
	}
	if ro.cards[0].err == nil || !strings.Contains(ro.cards[0].err.Error(), "R to refresh") {
		t.Errorf("error message should hint at refresh, got %v", ro.cards[0].err)
	}
	if !strings.Contains(ro.cards[0].err.Error(), "F to post as a file-level comment") {
		t.Errorf("error message should offer the file-level fallback, got %v", ro.cards[0].err)
	}
}

// TestApplyPRRefresh_ReanchorsAndReportsStaleFindings exercises the recovery
// path: after the user presses R, applyPRRefresh must update the head SHA,
// re-parse the new diff, re-anchor still-valid findings, and flag any
// findings that no longer have a hunk on the new diff.
func TestApplyPRRefresh_ReanchorsAndReportsStaleFindings(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "old1234", Owner: "o", Repo: "r"},
		Diff: refreshTestNewDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "stays anchored"},
				// c.go:7 isn't part of any hunk in refreshTestNewDiff (the
				// only hunk covers lines 1-3), so this finding will fail to
				// re-anchor after refresh.
				{Path: "c.go", Line: 7, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "no hunk now"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.AdoptDraft(d)
	if len(ro.cards) != 2 {
		t.Fatalf("cards %d want 2", len(ro.cards))
	}
	// Pretend we hit a 422 on the second card before refresh.
	ro.cards[1].state = cardError
	ro.cards[1].err = &gh.HeadDriftError{Was: "old1234", Now: "new5678"}
	ro.refreshing = true

	freshPR := &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "new5678", Owner: "o", Repo: "r"}
	ro.applyPRRefresh(freshPR, refreshTestNewDiff)

	if ro.refreshing {
		t.Error("applyPRRefresh should clear m.refreshing")
	}
	if ro.draft.PR.HeadSHA != "new5678" {
		t.Errorf("draft.PR.HeadSHA %q want new5678", ro.draft.PR.HeadSHA)
	}
	if ro.cards[0].hunk == nil {
		t.Error("a.go:1 should still anchor to a hunk on the new diff")
	}
	if ro.cards[0].state != cardPending {
		t.Errorf("first card state %v want cardPending after refresh", ro.cards[0].state)
	}
	if ro.cards[1].hunk != nil {
		t.Error("c.go:7 must not anchor to a hunk on the new diff")
	}
	if ro.cards[1].err != nil {
		t.Errorf("refresh should clear stale per-card error, got %v", ro.cards[1].err)
	}
	if !strings.Contains(ro.refreshNote, "no longer anchor") {
		t.Errorf("refreshNote should report unanchored findings, got %q", ro.refreshNote)
	}
	if !strings.Contains(ro.refreshNote, "old1234"[:7]) {
		t.Errorf("refreshNote should mention old SHA prefix, got %q", ro.refreshNote)
	}
}
