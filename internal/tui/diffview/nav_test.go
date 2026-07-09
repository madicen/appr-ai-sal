package diffview

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestAnchorIndexNextPrev(t *testing.T) {
	idx := BuildAnchorIndex([]int{12, 3, 7, 7}) // unsorted + duplicate
	if idx.Len() != 3 {
		t.Fatalf("expected 3 distinct anchors, got %d (%v)", idx.Len(), idx.Rows())
	}
	// From the top, Next should walk 3 → 7 → 12 then report none.
	got, ok := idx.Next(0)
	if !ok || got != 3 {
		t.Errorf("Next(0) = %d,%v want 3,true", got, ok)
	}
	got, ok = idx.Next(3)
	if !ok || got != 7 {
		t.Errorf("Next(3) = %d,%v want 7,true", got, ok)
	}
	got, ok = idx.Next(7)
	if !ok || got != 12 {
		t.Errorf("Next(7) = %d,%v want 12,true", got, ok)
	}
	if _, ok := idx.Next(12); ok {
		t.Error("Next(12) should report no further anchor")
	}
	// Prev walks back.
	got, ok = idx.Prev(12)
	if !ok || got != 7 {
		t.Errorf("Prev(12) = %d,%v want 7,true", got, ok)
	}
	got, ok = idx.Prev(7)
	if !ok || got != 3 {
		t.Errorf("Prev(7) = %d,%v want 3,true", got, ok)
	}
	if _, ok := idx.Prev(3); ok {
		t.Error("Prev(3) should report no earlier anchor")
	}
}

func TestAnchorIndexEmpty(t *testing.T) {
	idx := BuildAnchorIndex(nil)
	if idx.Len() != 0 {
		t.Fatalf("empty index Len = %d", idx.Len())
	}
	if _, ok := idx.Next(0); ok {
		t.Error("Next on empty index should be false")
	}
	if _, ok := idx.Prev(100); ok {
		t.Error("Prev on empty index should be false")
	}
}

func TestSearchIndexMatchesAndWraps(t *testing.T) {
	// Row 1 and row 3 contain "TODO" (case-insensitive); one row carries ANSI.
	styled := lipgloss.NewStyle().Bold(true).Render("please fix this todo item")
	lines := []string{
		"first line",
		"second line has a TODO here",
		"third line",
		styled,
		"fifth line",
	}
	idx := BuildSearchIndex(lines, "todo")
	if idx.Count() != 2 {
		t.Fatalf("expected 2 matches (incl. ANSI row), got %d (%v)", idx.Count(), idx.Rows())
	}
	if idx.Query() != "todo" {
		t.Errorf("Query = %q", idx.Query())
	}
	// Next from top → row 1.
	got, ok := idx.Next(0)
	if !ok || got != 1 {
		t.Errorf("Next(0) = %d,%v want 1,true", got, ok)
	}
	// Step past row 1 → row 3.
	got, ok = idx.Next(2)
	if !ok || got != 3 {
		t.Errorf("Next(2) = %d,%v want 3,true", got, ok)
	}
	// Past the last match wraps to the first.
	got, ok = idx.Next(4)
	if !ok || got != 1 {
		t.Errorf("Next(4) should wrap to 1, got %d,%v", got, ok)
	}
	// Prev above the first wraps to the last.
	got, ok = idx.Prev(0)
	if !ok || got != 3 {
		t.Errorf("Prev(0) should wrap to 3, got %d,%v", got, ok)
	}
}

func TestSearchIndexEmptyQuery(t *testing.T) {
	idx := BuildSearchIndex([]string{"anything"}, "   ")
	if idx.Count() != 0 {
		t.Errorf("blank query should match nothing, got %d", idx.Count())
	}
	if _, ok := idx.Next(0); ok {
		t.Error("Next on empty search should be false")
	}
}

func TestMatchLine(t *testing.T) {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render("Error: boom")
	if !MatchLine(styled, "error") {
		t.Error("MatchLine should match through ANSI, case-insensitively")
	}
	if MatchLine("clean line", "error") {
		t.Error("MatchLine should not match absent text")
	}
	if MatchLine("anything", "  ") {
		t.Error("blank query never matches")
	}
}
