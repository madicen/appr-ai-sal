package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

const threadReplyDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1,2 @@
+a
+b
`

func overlayWithOneCard(t *testing.T) *Model {
	t.Helper()
	ro := New(120, 44, false, false, false, nil, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: threadReplyDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "still off"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.AdoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("setup: cards %d want 1", len(ro.cards))
	}
	return ro
}

// TestExistingCommentsRoutesMatchingCardToReply confirms that an unresolved
// thread on the card's anchor tags the card with the thread id so posting
// replies in-thread.
func TestExistingCommentsRoutesMatchingCardToReply(t *testing.T) {
	ro := overlayWithOneCard(t)
	msg := data.ExistingPRCommentsMsg{
		Viewer: "octocat",
		Threads: []gh.ReviewThread{{
			ID: "PRRT_match",
			Comments: []gh.ReviewThreadComment{
				{Author: "alice", Body: "please fix", Path: "a.go", Line: 1, Side: "RIGHT"},
			},
		}},
	}
	if _, _ = ro.Update(msg); ro.cards[0].threadReplyID != "PRRT_match" {
		t.Fatalf("matching card should route to reply thread, got %q", ro.cards[0].threadReplyID)
	}
	// Posting should now dispatch a command (the reply), not a pre-flight error.
	focusAgentTabForTest(t, ro, review.SpecDocs)
	if _, cmd := ro.actPostCurrent(); cmd == nil {
		t.Fatal("posting a reply-routed card should return a tea.Cmd")
	}
	if ro.cards[0].state == cardError {
		t.Fatal("reply-routed card must not fall into the off-hunk error path")
	}
}

// TestExistingCommentsNonMatchingStaysTopLevel confirms a thread on a different
// anchor leaves the card top-level (no thread id).
func TestExistingCommentsNonMatchingStaysTopLevel(t *testing.T) {
	ro := overlayWithOneCard(t)
	msg := data.ExistingPRCommentsMsg{
		Viewer: "octocat",
		Threads: []gh.ReviewThread{{
			ID: "PRRT_other",
			Comments: []gh.ReviewThreadComment{
				{Author: "alice", Body: "please fix", Path: "other.go", Line: 9, Side: "RIGHT"},
			},
		}},
	}
	if _, _ = ro.Update(msg); ro.cards[0].threadReplyID != "" {
		t.Fatalf("non-matching card must stay top-level, got %q", ro.cards[0].threadReplyID)
	}
}

// TestExistingCommentsFirstReviewNoThreads confirms that with no threads every
// card posts top-level (first-review backward compatibility).
func TestExistingCommentsFirstReviewNoThreads(t *testing.T) {
	ro := overlayWithOneCard(t)
	if _, _ = ro.Update(data.ExistingPRCommentsMsg{Viewer: "octocat"}); ro.cards[0].threadReplyID != "" {
		t.Fatalf("no threads should leave card top-level, got %q", ro.cards[0].threadReplyID)
	}
}
