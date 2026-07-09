package model

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
)

// TestPanelFitsWithinTerminalWidth covers the regression where the
// rendered panel was wider than m.width and the right border ended
// up clipped off the screen. Two ways for that to happen historically:
//
//  1. bubbles/textinput's default "> " Prompt widened each input two
//     cells past its Width budget.
//  2. lipgloss's Style.Width does NOT include the border, so passing
//     `m.width - margin` produced a block `m.width - margin + border`
//     cells wide — one cell of border past the right edge.
//
// The assertion is intentionally strict: every rendered row must be
// exactly m.width cells wide AND end with the appropriate border
// glyph. Either side failing would let the bug slip in unnoticed.
func TestPanelFitsWithinTerminalWidth(t *testing.T) {
	for _, w := range []int{80, 100, 120, 160, 180, 200} {
		t.Run("width="+strconv.Itoa(w), func(t *testing.T) {
			zone.NewGlobal()
			m := New(Options{})
			m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
			m.prsLoaded = true

			panel := renderListPanel(m)
			rendered := zone.Scan(panel)
			lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
			if len(lines) < 4 {
				t.Fatalf("expected at least 4 rendered rows (top + body + bottom), got %d:\n%s", len(lines), rendered)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != w {
					t.Fatalf("row %d width = %d, want %d (terminal width):\n%q", i, got, w, line)
				}
			}
			topRight := strings.TrimRight(lines[0], " ")
			if !strings.HasSuffix(topRight, "╮") {
				t.Fatalf("top row missing right corner at width %d:\n%s", w, lines[0])
			}
			bottomRight := strings.TrimRight(lines[len(lines)-1], " ")
			if !strings.HasSuffix(bottomRight, "╯") {
				t.Fatalf("bottom row missing right corner at width %d:\n%s", w, lines[len(lines)-1])
			}
			for i, line := range lines[1 : len(lines)-1] {
				trimmed := strings.TrimRight(line, " ")
				if !strings.HasSuffix(trimmed, "│") {
					t.Fatalf("body row %d missing right border at width %d:\n%s", i+1, w, line)
				}
			}
		})
	}
}
