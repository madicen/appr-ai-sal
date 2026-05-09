package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Pane titles sit above viewport content; bubblezone row Y matches scanned lines.
// Wrapped titles add extra lines and shift every row zone vs where users click.
func TestDetailPaneTitleStyledTruncationStaysOneLine(t *testing.T) {
	innerW := 28
	title := "Files · " + boldStyle.Render("focused (tab to switch)")
	truncated := ansi.Truncate(title, innerW, "…")
	h := lipgloss.Height(detailPaneTitleStyle.Width(innerW).Render(truncated))
	if h != 1 {
		t.Fatalf("pane title wrapped to %d lines (want 1); mouse hit boxes drift", h)
	}
}
