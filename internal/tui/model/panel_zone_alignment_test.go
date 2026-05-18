package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

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
