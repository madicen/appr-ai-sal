package review

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
)

const filelevelTestDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1,2 @@
+a
+b
`

// newFilelevelOverlayWithErroredCard builds an overlay in phaseApprove
// with a single finding whose line is off-hunk so the card is locked into
// cardError after actPostCurrent's local pre-flight. That's the canonical
// state where the F key applies.
func newFilelevelOverlayWithErroredCard(t *testing.T) *Model {
	t.Helper()
	ro := New(120, 44, false, false, false, nil, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: filelevelTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 99, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "off-hunk"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.AdoptDraft(d)
	if len(ro.cards) != 1 {
		t.Fatalf("setup: cards %d want 1", len(ro.cards))
	}
	if _, _ = ro.actPostCurrent(); ro.cards[0].state != cardError {
		t.Fatalf("setup: card state %v want cardError after pre-flight", ro.cards[0].state)
	}
	return ro
}

// TestActPostCurrentFileLevel_FromErrorStateSetsFlagAndReturnsCmd is the
// canonical happy path: the reviewer pressed F on a cardError, the
// overlay flips card.fileLevelPost to true and returns the
// data.PostSingleFindingFileLevelCmd tea.Cmd so Bubble Tea can dispatch it.
func TestActPostCurrentFileLevel_FromErrorStateSetsFlagAndReturnsCmd(t *testing.T) {
	ro := newFilelevelOverlayWithErroredCard(t)
	_, cmd := ro.actPostCurrentFileLevel()
	if cmd == nil {
		t.Fatal("actPostCurrentFileLevel should return a tea.Cmd to dispatch the file-level post")
	}
	if !ro.cards[0].fileLevelPost {
		t.Error("card.fileLevelPost should be true after the file-level fallback fires")
	}
}

// TestActPostCurrentFileLevel_PendingWithHunkIsNoOp prevents an own-goal:
// when a card is pending AND has a valid hunk, F must be a no-op. Letting
// it fire would silently downgrade a perfectly-anchored finding to a
// file-level comment, which is worse than the inline post the user
// presumably came in to make.
func TestActPostCurrentFileLevel_PendingWithHunkIsNoOp(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: filelevelTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "anchored fine"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.AdoptDraft(d)
	if ro.cards[0].hunk == nil {
		t.Fatal("setup: line 1 should be on a hunk in filelevelTestDiff")
	}
	_, cmd := ro.actPostCurrentFileLevel()
	if cmd != nil {
		t.Error("F on a pending+anchored card should be a no-op (no tea.Cmd)")
	}
	if ro.cards[0].fileLevelPost {
		t.Error("F on a pending+anchored card must not flip fileLevelPost")
	}
}

// TestActPostCurrentFileLevel_DryRunEmitsDryRunPayload exercises the
// dry-run path end-to-end via the returned tea.Cmd: it should evaluate
// to a data.DryRunPayloadMsg whose Payload mentions subject_type=file so the
// human reviewing the dry-run can see what the API call will look like.
func TestActPostCurrentFileLevel_DryRunEmitsDryRunPayload(t *testing.T) {
	ro := New(120, 44, true /* dryRun */, false, false, nil, false)
	d := &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: filelevelTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Findings: []review.Finding{
				{Path: "a.go", Line: 99, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "dry-run path"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
	ro.AdoptDraft(d)
	// In dry-run, actPostCurrent's pre-flight is bypassed and the inline
	// command is issued. Force-flip to cardError to mimic the post-error
	// state F operates on.
	ro.cards[0].state = cardError
	_, cmd := ro.actPostCurrentFileLevel()
	if cmd == nil {
		t.Fatal("dry-run F should still emit a tea.Cmd that yields a data.DryRunPayloadMsg")
	}
	msg := cmd()
	dr, ok := msg.(data.DryRunPayloadMsg)
	if !ok {
		t.Fatalf("dry-run F should return data.DryRunPayloadMsg, got %T", msg)
	}
	if !strings.Contains(dr.Title, "file-level") {
		t.Errorf("dry-run title should mention file-level, got %q", dr.Title)
	}
	if !strings.Contains(dr.Payload, `"subject_type": "file"`) {
		t.Errorf("dry-run payload should include subject_type=file, got:\n%s", dr.Payload)
	}
}

// TestReviewCommentBodyForFileLevel_AddsLineHint pins the body-shaping
// rule: the file-level comment body includes a one-liner naming the
// line the model originally meant, so the GitHub reader can still cross-
// reference the finding even though the comment isn't anchored inline.
func TestReviewCommentBodyForFileLevel_AddsLineHint(t *testing.T) {
	body := review.ReviewCommentBodyForFileLevel(review.SpecDocs, review.Finding{
		Path: "a.go", Line: 42, Side: "RIGHT",
		Severity: review.SeverityWarning, Comment: "the inline narrative",
	})
	if !strings.Contains(body, "Intended for line 42") {
		t.Errorf("expected file-level body to name the intended line, got:\n%s", body)
	}
	if !strings.Contains(body, "the inline narrative") {
		t.Errorf("file-level body should still carry the finding's Comment text, got:\n%s", body)
	}
}

// TestReviewCommentBodyForFileLevel_OmitsSuggestionBlock locks in the
// "no ```suggestion on file-level" rule: GitHub's one-click apply only
// works on line-anchored comments, so leaving the suggestion block in
// would render as inert code the reader might copy-paste against the
// wrong line.
func TestReviewCommentBodyForFileLevel_OmitsSuggestionBlock(t *testing.T) {
	body := review.ReviewCommentBodyForFileLevel(review.SpecDocs, review.Finding{
		Path: "a.go", Line: 42, Side: "RIGHT",
		Severity:   review.SeverityWarning,
		Comment:    "fix me",
		Suggestion: "fixed line",
	})
	if strings.Contains(body, "```suggestion") {
		t.Errorf("file-level body must drop the suggestion block, got:\n%s", body)
	}
}

// TestActPostCurrentFileLevel_KeyFRouting verifies the keystroke wiring:
// in phaseApprove with a cardError, pressing F triggers the file-level
// command branch (not the existing lowercase-f "finish early" branch
// which would walk past the unposted finding).
func TestActPostCurrentFileLevel_KeyFRouting(t *testing.T) {
	ro := newFilelevelOverlayWithErroredCard(t)
	keyF := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}}
	_, cmd := ro.Update(keyF)
	if cmd == nil {
		t.Fatal("F should dispatch the file-level post cmd")
	}
	if !ro.cards[0].fileLevelPost {
		t.Error("F should flip card.fileLevelPost on a cardError card")
	}
}
