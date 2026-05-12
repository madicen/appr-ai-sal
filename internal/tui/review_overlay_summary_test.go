package tui

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestReviewOverlaySummaryPhaseShowsFullMarkdownBody(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false, nil)
	base := &review.Draft{PR: &gh.PR{HeadSHA: "abc"}, Diff: ""}
	ro.adoptDraft(base)
	// Long merge summary so RenderBody() exceeds the old 24-line preview cap.
	large := strings.Repeat("Review context line.\n", 60)
	ro.draft = &review.Draft{
		PR: base.PR,
		VibeCoach: &review.VibeCoachResult{
			Verdict: review.VibeVerdictApprove,
			Summary: large,
		},
	}
	ro.phase = phaseSummary
	ro.rebuildBody()
	out := ro.renderSummaryBody()
	if strings.Contains(out, "more lines") {
		t.Fatal("summary preview should not truncate with a line budget cap")
	}
	// Glamour word-wraps the markdown so a single phrase can break across
	// lines; assert on a short substring that won't get split, and on a high
	// occurrence count so we know the full body — not just the first chunk —
	// reached the preview.
	if !strings.Contains(out, "Review") {
		t.Fatal("expected body text to appear in preview")
	}
	if got := strings.Count(out, "Review"); got < 50 {
		t.Fatalf("expected ~60 occurrences of body text in preview, got %d", got)
	}
}
