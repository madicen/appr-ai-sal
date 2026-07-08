package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/demo"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/tuitest"
)

// Golden-file render tests (Phase 5 item 11) for the review overlay's big
// render functions in view.go / summary.go. Every test forces a monochrome
// Ascii profile + a fixed modal size and drives the overlay off the demo
// package's stable offline fixtures, so the output is deterministic. The
// overlay is populated via AdoptDraft against demo.FinalReviewDraft — the
// exact Draft the demo pipeline emits — so no scripted delays or LLM calls
// are involved. Run `go test ./internal/tui/tabs/review -update` to refresh.

const (
	goldenModalW = 120
	goldenModalH = 40
)

func demoRef() gh.Ref {
	return gh.Ref{Owner: "madicen", Repo: "appr-ai-sal", Number: 742}
}

// newDemoOverlay builds a review overlay in the post-run state: the demo
// draft adopted, five specialists active (tech excluded, matching a real run
// with no technology briefs), dry-run on so no golden ever depends on a live
// post path.
func newDemoOverlay(t *testing.T) *Model {
	t.Helper()
	ro := New(goldenModalW, goldenModalH, true /*dryRun*/, false, false, nil, true /*demo*/)
	ro.SetSpecialists(review.ActiveSpecialists(false))
	ro.AdoptDraft(demo.FinalReviewDraft(demoRef(), nil))
	return ro
}

// agentTabIndex returns the tab index for the named agent, or -1.
func agentTabIndex(m *Model, name string) int {
	for i, tb := range m.tabs {
		if tb.kind == tabAgent && tb.agent == name {
			return i
		}
	}
	return -1
}

func TestGoldenReviewSummaryBody(t *testing.T) {
	tuitest.ForceMonochrome(t)
	ro := newDemoOverlay(t)
	// Attach the canned usage totals so the golden also covers the R1 usage
	// line; the values are fixed so they don't churn the golden.
	u := demo.RunUsageTotals()
	ro.runUsage = &u
	ro.rebuildBody()
	tuitest.AssertGolden(t, "review_summary_body", ro.renderSummaryBody())
}

func TestGoldenReviewAgentSecurity(t *testing.T) {
	tuitest.ForceMonochrome(t)
	ro := newDemoOverlay(t)
	i := agentTabIndex(ro, review.SpecSecurity)
	if i < 0 {
		t.Fatalf("security agent tab not found")
	}
	ro.focusTab(i)
	tuitest.AssertGolden(t, "review_agent_security", ro.renderAgentTab(review.SpecSecurity))
}

func TestGoldenReviewAgentDesign(t *testing.T) {
	tuitest.ForceMonochrome(t)
	ro := newDemoOverlay(t)
	i := agentTabIndex(ro, review.SpecDesign)
	if i < 0 {
		t.Fatalf("design agent tab not found")
	}
	ro.focusTab(i)
	tuitest.AssertGolden(t, "review_agent_design", ro.renderAgentTab(review.SpecDesign))
}

// TestGoldenReviewOverlayViewSummary goldens the composite View() — title +
// tab strip + severity counts + body + help — as the user actually sees the
// post-summary screen.
func TestGoldenReviewOverlayViewSummary(t *testing.T) {
	tuitest.ForceMonochrome(t)
	ro := newDemoOverlay(t)
	tuitest.AssertGolden(t, "review_view_summary", ro.View())
}
