package review

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/madicen/appr-ai-sal/internal/demo"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/tuitest"
)

// TestFlowReviewDryRunPost is the review-overlay end-to-end flow (Phase 5 item
// 11): it drives the real review overlay tea.Model in demo/dry-run mode
// through run-complete → post summary, and asserts the dry-run path reaches
// the "Review complete" receipt without any real GitHub post.
//
// Rather than replay demo.SyntheticReviewProgress's ~28s scripted timeline, it
// feeds the terminal "done" progress event directly (carrying the exact Draft
// FinalReviewDraft builds), so the overlay adopts the finished review instantly
// and deterministically.
func TestFlowReviewDryRunPost(t *testing.T) {
	tuitest.ForceMonochrome(t)
	ro := New(120, 40, true /*dryRun*/, false, false, nil, true /*demo*/)
	ro.SetSpecialists(review.ActiveSpecialists(false))
	tm := teatest.NewTestModel(t, ro, teatest.WithInitialTermSize(120, 40))

	draft := demo.FinalReviewDraft(demoRef(), nil)
	usage := demo.RunUsageTotals()
	// data.ProgressMsg is review.Progress; Stage="done" + Final triggers
	// AdoptDraft, which lands the overlay on the post-summary screen.
	tm.Send(data.ProgressMsg{Stage: "done", Final: draft, Usage: &usage})
	waitReview(t, tm, "post the review summary")

	// y posts the summary. In dry-run this renders the payload preview and
	// advances to the "Review complete" receipt instead of hitting GitHub.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	waitReview(t, tm, "Review complete")

	if err := tm.Quit(); err != nil {
		t.Fatalf("quit program: %v", err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func waitReview(t *testing.T, tm *teatest.TestModel, sub string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(sub))
	}, teatest.WithDuration(5*time.Second))
}
