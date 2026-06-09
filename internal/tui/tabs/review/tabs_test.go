package review

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

func init() { zone.NewGlobal() }

// focusAgentTabForTest moves the overlay's focus to the named agent's tab
// so finding-level keys (y/n/F) and the renderAgentTab path are exercised.
func focusAgentTabForTest(t *testing.T, ro *Model, agent string) {
	t.Helper()
	for i, tb := range ro.tabs {
		if tb.kind == tabAgent && tb.agent == agent {
			ro.focusTab(i)
			return
		}
	}
	t.Fatalf("no agent tab for %q", agent)
}

const tabsTestDiff = `diff --git a/a.go b/a.go
--- /dev/null
+++ b/a.go
@@ -0,0 +1 @@
+a
diff --git a/b.go b/b.go
--- /dev/null
+++ b/b.go
@@ -0,0 +1 @@
+b
`

func tabsTestDraft() *review.Draft {
	return &review.Draft{
		PR:   &gh.PR{Repository: "o/r", Number: 1, HeadSHA: "abc", Owner: "o", Repo: "r"},
		Diff: tabsTestDiff,
		Specialists: []review.SpecialistResult{
			{Specialist: review.SpecDocs, Summary: "docs notes", Findings: []review.Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c1"},
			}},
			{Specialist: review.SpecSecurity, Summary: "security notes", Findings: []review.Finding{
				{Path: "b.go", Line: 1, Side: "RIGHT", Severity: review.SeverityWarning, Comment: "c2"},
			}},
		},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges, Summary: "verdict"},
	}
}

// New overlays expose a tab bar: overview, one per output agent (5
// specialists + arbiter + vibe), then summary.
func TestNewOverlayBuildsTabBar(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	want := 1 + len(outputAgentOrder()) + 1
	if len(ro.tabs) != want {
		t.Fatalf("tabs %d want %d", len(ro.tabs), want)
	}
	if ro.tabs[0].kind != tabOverview {
		t.Fatalf("first tab kind %v want overview", ro.tabs[0].kind)
	}
	if ro.tabs[len(ro.tabs)-1].kind != tabSummary {
		t.Fatalf("last tab kind %v want summary", ro.tabs[len(ro.tabs)-1].kind)
	}
	if ro.phase != phaseRunning {
		t.Fatalf("fresh overlay phase %v want phaseRunning", ro.phase)
	}
}

// On completion the overlay auto-focuses the summary tab when the user is
// still on the overview tab.
func TestAdoptDraftAutoFocusesSummaryFromOverview(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	if !ro.onSummaryTab() {
		t.Fatalf("expected summary tab focused after completion, activeTab=%d", ro.activeTab)
	}
	if ro.phase != phaseSummary {
		t.Fatalf("phase %v want phaseSummary", ro.phase)
	}
}

// Switching to an agent tab focuses that agent's first finding and lets
// the reviewer post it; posting marks the card and advances within the
// agent.
func TestAgentTabPostsFinding(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.AdoptDraft(tabsTestDraft())
	focusAgentTabForTest(t, ro, review.SpecDocs)
	if ro.phase != phaseApprove {
		t.Fatalf("phase %v want phaseApprove on agent tab", ro.phase)
	}
	if got := ro.activeAgent(); got != review.SpecDocs {
		t.Fatalf("activeAgent %q want %q", got, review.SpecDocs)
	}
	idxs := ro.agentCardIndices(review.SpecDocs)
	if len(idxs) != 1 || ro.idx != idxs[0] {
		t.Fatalf("focused idx %d want %v", ro.idx, idxs)
	}
	// Simulate a successful post arriving from the root model.
	_, _ = ro.Update(tea.WindowSizeMsg{Width: 120, Height: 44})
	ro.cards[ro.idx].state = cardPosted
	if onPR, posted, _ := ro.agentCardTally(review.SpecDocs); posted != 1 || onPR != 0 {
		t.Fatalf("docs tally posted=%d onPR=%d want posted=1 onPR=0", posted, onPR)
	}
	body := ansi.Strip(ro.renderAgentTab(review.SpecDocs))
	if !strings.Contains(body, "docs notes") {
		t.Fatalf("agent tab should show the agent summary, got:\n%s", body)
	}
}

// Every agent tab shows a summary even when the agent produced no
// findings (e.g. the vibe coach), so the reviewer can always see what it
// did under the hood.
func TestAgentTabShowsSummaryForFindinglessAgent(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	// Mark the vibe-coach row done with a summary via the progress merge.
	ro.mergeProgress(review.Progress{Stage: "vibe-coach", Detail: "start"})
	ro.mergeProgress(review.Progress{Stage: "vibe-coach", Detail: "done", Vibe: &review.VibeCoachResult{
		Verdict: review.VibeVerdictApprove,
		Summary: "looks good overall",
	}})
	body := ansi.Strip(ro.renderAgentTab(review.SpecVibeCoach))
	if !strings.Contains(body, "looks good overall") {
		t.Fatalf("vibe-coach tab should show its summary, got:\n%s", body)
	}
	if !strings.Contains(body, "Approve") {
		t.Fatalf("vibe-coach tab should show its merge recommendation, got:\n%s", body)
	}
}

// Tab navigation keys move the active tab.
func TestTabNavigationKeys(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	if ro.activeTab != 0 {
		t.Fatalf("activeTab %d want 0", ro.activeTab)
	}
	out, _ := ro.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	ro = out.(*Model)
	if ro.activeTab != 1 {
		t.Fatalf("after tab, activeTab %d want 1", ro.activeTab)
	}
	out, _ = ro.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	ro = out.(*Model)
	if ro.activeTab != 0 {
		t.Fatalf("after shift+tab, activeTab %d want 0", ro.activeTab)
	}
}
