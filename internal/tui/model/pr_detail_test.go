package model

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Pane titles sit above viewport content; bubblezone row Y matches scanned lines.
// Wrapped titles add extra lines and shift every row zone vs where users click.
func TestDetailPaneTitleStyledTruncationStaysOneLine(t *testing.T) {
	innerW := 28
	title := "Files · " + styles.BoldStyle.Render("focused (tab to switch)")
	truncated := ansi.Truncate(title, innerW, "…")
	h := lipgloss.Height(styles.DetailPaneTitleStyle.Width(innerW).Render(truncated))
	if h != 1 {
		t.Fatalf("pane title wrapped to %d lines (want 1); mouse hit boxes drift", h)
	}
}

// framePane must render the rounded border on all four sides — historically
// using both Style.Height(h) and MaxHeight(h) clipped the bottom border (and
// the right border for the rightmost pane on screen) because Style.Height/
// Style.Width set INTERIOR dimensions while MaxHeight/MaxWidth cap TOTAL
// dimensions; setting both to outerH inflates total to outerH+2 and then
// clips two rows.
func TestDetailRenderedPRDetailBodyHasBottomBorders(t *testing.T) {
	m := detailFixtureModel(t)
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	// The body region sits between header (row 0) and status (last row).
	// Find the last non-empty body line that contains a bottom border arc;
	// every pane's bottom border has '╰' and '╯'.
	foundBottom := false
	for _, ln := range lines {
		if strings.Contains(ln, "╰") && strings.Contains(ln, "╯") {
			foundBottom = true
			break
		}
	}
	if !foundBottom {
		t.Fatalf("no bottom border (╰…╯) row found in rendered detail body — borders are clipped:\n%s", out)
	}
}

// The rightmost pane (controls) must render its right vertical border. With
// the Width/MaxWidth bug, the right border of the rightmost pane is clipped;
// adjacent panes hide their missing right border under the next pane's left
// border in the horizontal join.
func TestDetailRightmostPaneHasRightBorder(t *testing.T) {
	m := detailFixtureModel(t)
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	// Look for any line whose trailing run of non-space characters ends in
	// '│' — that's the right edge of the rightmost framed pane on a body
	// row. We accept trailing whitespace from terminal padding.
	any := false
	for _, ln := range lines {
		trimmed := strings.TrimRight(ln, " ")
		if strings.HasSuffix(trimmed, "│") {
			any = true
			break
		}
	}
	if !any {
		t.Fatalf("no body row ends in a right-border '│' — rightmost pane border is clipped:\n%s", out)
	}
}
