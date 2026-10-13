package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestReviewOverlaySummaryPhaseShowsFullMarkdownBody(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	base := &review.Draft{PR: &gh.PR{HeadSHA: "abc"}, Diff: ""}
	ro.AdoptDraft(base)
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
	// Section cards are collapsed by default; expand the headline to see the
	// full body.
	ro.syncSummarySections()
	ro.summaryExpanded["headline"] = true
	out := ro.renderSummaryBody()
	if strings.Contains(out, "more lines") {
		t.Fatal("summary preview should not truncate with a line budget cap")
	}
	// Raw markdown is hard-wrapped to the row width; assert on a short
	// substring and a high occurrence count so the full body — not just the
	// first chunk — reached the preview.
	if !strings.Contains(out, "Review") {
		t.Fatal("expected body text to appear in preview")
	}
	if got := strings.Count(out, "Review"); got < 50 {
		t.Fatalf("expected ~60 occurrences of body text in preview, got %d", got)
	}
}
