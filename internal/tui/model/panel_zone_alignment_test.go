package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// TestRenderedViewHeightFitsTerminal guards against the bug where the
// status bar's hint text was longer than m.width on narrow / mid
// terminals. lipgloss.Height(renderStatus()) reported 1, so
// chromeBodyHeight only budgeted 1 row for status, but the terminal
// auto-wrapped the over-long line at write time. The screen then
// scrolled up by 1, dropping the header and shifting every panel row
// up by one — bubblezone's recorded StartY/EndY still pointed at the
// pre-scroll positions, so clicks on the visible search / URL inputs
// landed 1 row below their target.
//
// The fix is to pre-wrap the hint at the StatusBar's content width so
// the rendered height is honest, and the View() output never exceeds
// m.height in logical rows.
//
// We sweep over a range of widths so the regression is caught even as
// future hint additions push the threshold around.
func TestRenderedViewHeightFitsTerminal(t *testing.T) {
	for _, w := range []int{100, 120, 160, 180, 200, 207, 240} {
		t.Run("width="+itoa(w), func(t *testing.T) {
			zone.NewGlobal()
			m := New(Options{})
			m.Update(tea.WindowSizeMsg{Width: w, Height: 46})
			m.prsLoaded = true

			out := m.View()
			lines := strings.Split(out, "\n")
			if got := len(lines); got > m.height {
				t.Fatalf("View() produced %d logical rows; must not exceed terminal height %d (status hint wrapping not budgeted?)", got, m.height)
			}
			// Each rendered row's printable width must also fit so the
			// terminal doesn't visually wrap.
			for i, line := range lines {
				if pw := lipgloss.Width(line); pw > w {
					t.Fatalf("row %d width=%d exceeds terminal width=%d (line will visually wrap and scroll the screen):\n%q", i, pw, w, line)
				}
			}
		})
	}
}

// TestPanelZonesRemainAlignedAfterDetailRoundTrip locks in the bug
// where opening a PR and then escaping back to the list shifted the
// search / URL click areas down by one row. The mechanism: opening
// the PR populates m.prLanguages, which makes
// renderBuildLangAgentsHint emit a longer "(missing!)" / "(stale)"
// suffix on the next list-mode render. The hint then wraps to one
// more row in the status bar — but the bubbles/list height was cached
// from before the round-trip, so View() output overflows m.height by
// one row, the standard renderer drops the top (header) line, every
// visible row shifts up by one, and bubblezone's recorded zone Y
// values now point one row below the visible inputs.
//
// The fix is for handleDetailKey's esc/q branch to call m.relayout()
// before returning to modeList so the list's height is recomputed
// against the new (potentially taller) status bar. This test exercises
// the full path: open a detail view, escape back, then assert View()
// still fits in m.height and the visible search row still hits the
// SearchField zone.
func TestPanelZonesRemainAlignedAfterDetailRoundTrip(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 46})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "Add caching layer", Repository: "acme/api", Owner: "acme", Repo: "api", Author: "alice"},
		{Number: 2, Title: "Refactor auth", Repository: "acme/web", Owner: "acme", Repo: "web", Author: "bob"},
	}})

	// Simulate opening a PR's detail view: ParseDiff seeds
	// m.parsedDiff, recordPRLanguages populates m.prLanguages so
	// langAgentsFreshness will subsequently classify the selected
	// list row as Missing/Stale (instead of Unknown), lengthening
	// the status hint on return.
	pr := &gh.PR{Number: 1, Title: "Add caching layer", Repository: "acme/api", Owner: "acme", Repo: "api", Author: "alice"}
	m.Update(data.PRDetailMsg{PR: pr, Diff: "diff --git a/foo.go b/foo.go\n--- /dev/null\n+++ b/foo.go\n@@ -0,0 +1 @@\n+package foo\n"})

	if m.mode != modeDetail {
		t.Fatalf("opening PR should put us in modeDetail; got %v", m.mode)
	}

	// Escape back to list — this is the moment the bug manifests if
	// relayout isn't called.
	out, _ := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(*Model)
	if m.mode != modeList {
		t.Fatalf("esc from detail should return to modeList; got %v", m.mode)
	}

	view := m.View()
	lines := strings.Split(view, "\n")
	if got := len(lines); got > m.height {
		t.Fatalf("post-roundtrip View() produced %d rows; must not exceed terminal height %d (relayout missing on detail→list?)", got, m.height)
	}

	// The visible search row must still fall inside the SearchField
	// zone — i.e. clicking on the placeholder text triggers focus.
	row := -1
	for i, line := range lines {
		if strings.Contains(line, "filter by title") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("search placeholder not found in post-roundtrip View()")
	}
	click := tea.MouseMsg{X: 30, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	newModel, _ := m.handleMouse(click)
	nm := newModel.(*Model)
	if nm.listFocus != focusSearch {
		t.Fatalf("post-roundtrip click on visible search row %d did not focus search input (got listFocus=%v); zones drifted from visible content", row, nm.listFocus)
	}
}

// TestClickOnVisibleInputRowFocusesInput is the full-circle test for
// the user-reported bug ("the mouse areas for search and url are one
// row too low"). For each interactive panel zone:
//
//  1. Render the model's full View().
//  2. Find the row in the rendered string that contains the input's
//     visible content (placeholder text, chip label).
//  3. Synthesize a left-click at that visible row's screen coordinates.
//  4. Dispatch through handleMouse and assert the click was treated
//     as a hit on the right zone (focus flipped, filter chip
//     activated, etc.).
//
// If the bubblezone bounds drift away from the visible content, the
// click in step 3 lands outside the zone and the assertion fails.
func TestClickOnVisibleInputRowFocusesInput(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 46})
	m.prsLoaded = true

	out := m.View()
	lines := strings.Split(out, "\n")

	findRow := func(needle string) int {
		for i, line := range lines {
			if strings.Contains(line, needle) {
				return i
			}
		}
		return -1
	}

	t.Run("search-input", func(t *testing.T) {
		row := findRow("filter by title")
		if row < 0 {
			t.Fatalf("search placeholder not found in rendered output")
		}
		// Click in the middle of the placeholder text. If the zone
		// is mis-aligned by a row (the original bug) this click
		// lands outside the SearchField zone and m.listFocus stays
		// at focusList.
		click := tea.MouseMsg{X: 50, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
		newModel, _ := m.handleMouse(click)
		nm := newModel.(*Model)
		if nm.listFocus != focusSearch {
			t.Fatalf("click on visible search row %d did not focus search input (got listFocus=%v); zone is mis-aligned with visible row", row, nm.listFocus)
		}
	})

	// Reset for next sub-test.
	m = New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 46})
	m.prsLoaded = true
	out = m.View()
	lines = strings.Split(out, "\n")

	t.Run("url-input", func(t *testing.T) {
		row := findRow("https://github.com/owner/repo/pull/123")
		if row < 0 {
			t.Fatalf("URL placeholder not found in rendered output")
		}
		click := tea.MouseMsg{X: 150, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
		newModel, _ := m.handleMouse(click)
		nm := newModel.(*Model)
		if nm.listFocus != focusURL {
			t.Fatalf("click on visible URL row %d did not focus URL input (got listFocus=%v); zone is mis-aligned with visible row", row, nm.listFocus)
		}
	})
}

// TestPanelZonesMatchVisibleRows asserts that the bubblezone bounds
// recorded for the search / URL / filter chips line up with the row
// at which their content visually appears in the rendered View.
// This is the user-facing bug we want to lock in: a click at a zone's
// recorded (StartY, StartX) must land on the same screen cell the
// user sees.
func TestPanelZonesMatchVisibleRows(t *testing.T) {
	for _, w := range []int{120, 160, 200, 207} {
		t.Run("width="+itoa(w), func(t *testing.T) {
			zone.NewGlobal()
			m := New(Options{})
			m.Update(tea.WindowSizeMsg{Width: w, Height: 46})
			m.prsLoaded = true

			out := m.View()
			lines := strings.Split(out, "\n")

			checks := []struct {
				id     string
				expect string
			}{
				{zones.FilterTeams, "teams+you"},
				{zones.FilterExplicit, "explicit only"},
				{zones.FilterAuthored, "my PRs"},
				{zones.SearchField, "filter by title"},
				{zones.URLField, "https://github.com/owner/repo/pull/123"},
				{zones.RefreshList, "refresh (R)"},
			}
			for _, c := range checks {
				waitBubbleZone(t, c.id)
				z := zone.Get(c.id)
				if z == nil {
					t.Fatalf("zone %q not registered", c.id)
				}
				if z.StartY < 0 || z.StartY >= len(lines) {
					t.Fatalf("zone %q StartY=%d out of bounds (lines=%d)", c.id, z.StartY, len(lines))
				}
				row := lines[z.StartY]
				if !strings.Contains(row, c.expect) {
					t.Fatalf("zone %q recorded at row %d but row content %q doesn't contain %q (zone misaligned with visible row)", c.id, z.StartY, row, c.expect)
				}
			}
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
