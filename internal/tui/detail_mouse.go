package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
)

func treeViewportScrollLines(m *Model) int {
	if m.treeScrollLines > 0 {
		return m.treeScrollLines
	}
	// Fallback before first refreshDetailViews. With the redundant in-pane
	// "Changed files" header dropped, the viewport line count equals the row
	// count exactly.
	return max(1, len(m.treeRows))
}

// treeRowFromMouse resolves a file row from a click: first bubblezone row marks,
// then the viewport body zone so padded blank rows below the last file still map
// to a row (see bubbles/viewport lipgloss Height padding).
//
// Row N now sits at viewport line N (no in-pane header), so the body fallback
// maps line index → row index 1:1 with no off-by-one.
func (m *Model) treeRowFromMouse(msg tea.MouseMsg) (int, bool) {
	for i := range m.treeRows {
		if z := zone.Get(zoneTreeFile(i)); z != nil && z.InBounds(msg) {
			return i, true
		}
	}
	zb := zone.Get(ZonePaneTreeBody)
	if zb == nil || !zb.InBounds(msg) || len(m.treeRows) == 0 {
		return -1, false
	}
	_, ry := zb.Pos(msg)
	if ry < 0 {
		return -1, false
	}
	top := m.treeView.YOffset
	contentLine := top + ry
	if contentLine < 0 {
		return -1, false
	}
	if contentLine >= len(m.treeRows) {
		contentLine = len(m.treeRows) - 1
	}
	return contentLine, true
}
