package model

import (
	"regexp"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/demo"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/tuitest"
)

// Golden-file render tests (Phase 5 item 11) for the root model's two biggest
// views — the PR list (review queue) and the PR detail page (tree + diff +
// controls). Both are driven off the demo fixtures at a fixed terminal size
// with a monochrome Ascii profile. The only run-to-run variation is the
// relative "updated N ago" timestamp on list rows, which redactTime replaces
// with a stable placeholder before comparison. Run with -update to refresh.

const (
	goldenTermW = 120
	goldenTermH = 40
)

// redactTime replaces humanSince output (relative ages + the >30d date
// fallback) with a fixed token so the goldens don't depend on the wall clock.
var redactTime = regexp.MustCompile(`\d+[mhd] ago|just now|\d{4}-\d{2}-\d{2}`)

func assertModelGolden(t *testing.T, name, got string) {
	t.Helper()
	got = redactTime.ReplaceAllString(tuitest.Normalize(got), "<time>")
	// tuitest.AssertGolden re-normalizes (idempotent) and applies the -update
	// convention. We pre-redact so the stored golden is stable.
	tuitest.AssertGolden(t, name, got)
}

func newDemoRootModel(t *testing.T) *Model {
	t.Helper()
	m := New(Options{Demo: true, DryRun: true})
	m.Update(tea.WindowSizeMsg{Width: goldenTermW, Height: goldenTermH})
	return m
}

func TestGoldenListView(t *testing.T) {
	tuitest.ForceMonochrome(t)
	m := newDemoRootModel(t)
	m.Update(data.PRListMsg{PRs: demo.DemoPullRequests()})
	assertModelGolden(t, "list_view", m.View())
}

func TestGoldenDetailView(t *testing.T) {
	tuitest.ForceMonochrome(t)
	m := newDemoRootModel(t)
	ref := gh.Ref{Owner: "madicen", Repo: "appr-ai-sal", Number: 742}
	pr := demo.LookupPR(ref)
	if pr == nil {
		t.Fatal("demo PR #742 fixture missing")
	}
	m.Update(data.PRDetailMsg{PR: pr, Diff: demo.DemoDiff(ref)})
	assertModelGolden(t, "detail_view", m.View())
}
