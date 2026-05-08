package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"
)

// Bubblezone uses newline-delimited line indices for StartY. You cannot find the
// tree row by searching the full JoinHorizontal line for the basename: the Diff
// column may mention the same path earlier (e.g. "Diff · aa.go"), so the first
// matching line index can be two rows above the Files column row — exactly the
// illusion of a "two-row offset" when reasoning from grep instead of zones.
func TestBubblezoneTreeRowOriginSelfConsistent(t *testing.T) {
	m := detailFixtureModel(t)
	out := m.View()
	waitBubbleZone(t, zoneTreeFile(0))
	z := zone.Get(zoneTreeFile(0))
	if z == nil {
		t.Fatal("zoneTreeFile(0) nil")
	}
	lines := strings.Split(out, "\n")
	if z.StartY < 0 || z.StartY >= len(lines) {
		t.Fatalf("invalid StartY=%d len(lines)=%d", z.StartY, len(lines))
	}
	base := filepath.Base(m.treeRows[0].Path)
	if !strings.Contains(ansi.Strip(lines[z.StartY]), base) {
		t.Fatalf("tree zone row should contain %q; line=%q", base, ansi.Strip(lines[z.StartY]))
	}
	msg := tea.MouseMsg{X: z.StartX, Y: z.StartY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	if !z.InBounds(msg) {
		t.Fatalf("zone origin must be inside zone: %+v msg=%+v", z, msg)
	}
}

// Click exactly at (StartX, StartY) of the first tree row zone — must select row 0.
func TestSyntheticMouseAtTreeRowZoneOriginSelectsFirstFile(t *testing.T) {
	m := detailFixtureModel(t)
	m.treeIdx = len(m.treeRows) - 1
	m.selectedFilePath = m.treeRows[m.treeIdx].Path
	m.refreshDetailViews()
	_ = m.View()
	waitBubbleZone(t, zoneTreeFile(0))
	z := zone.Get(zoneTreeFile(0))
	msg := tea.MouseMsg{
		X:      z.StartX,
		Y:      z.StartY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	out, _ := m.detailHandleMouse(msg, false)
	m2 := out.(*Model)
	if m2.treeIdx != 0 {
		t.Fatalf("origin click should select first file; treeIdx=%d", m2.treeIdx)
	}
}
