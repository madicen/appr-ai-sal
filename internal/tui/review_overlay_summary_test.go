package tui

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestReviewOverlaySummaryPhaseShowsFullMarkdownBody(t *testing.T) {
	ro := newReviewOverlay(120, 44, false, false, false)
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
	if !strings.Contains(out, "Review context line.") {
		t.Fatal("expected full body text to appear in preview")
	}
}
