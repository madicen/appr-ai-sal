package model

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// treeHit describes the result of a tree-pane click: which view-row index
// (into treeViewRows) the click landed on, and whether that row is a
// folder header (versus a file).
type treeHit struct {
	viewLine int  // index into m.treeViewRows
	isFolder bool // true if the row is a folder header (toggle-on-click)
}

// treeRowFromMouse resolves a tree row from a click: first per-row file or
// folder zones (exact bounds), then the viewport body zone so padded blank
// rows below the last item still map to the last row (see bubbles/viewport
// lipgloss Height padding). Each tree view row is exactly one physical
// line, so click-Y maps 1:1 to view-row index when the body fallback runs.
func (m *Model) treeRowFromMouse(msg tea.MouseMsg) (treeHit, bool) {
	for i := range m.treeViewRows {
		if m.treeViewRows[i].isFile {
			fi := m.treeViewRows[i].fileIndex
			if z := zone.Get(zones.TreeFile(fi)); z != nil && z.InBounds(msg) {
				return treeHit{viewLine: i, isFolder: false}, true
			}
		} else {
			if z := zone.Get(zones.TreeFolder(i)); z != nil && z.InBounds(msg) {
				return treeHit{viewLine: i, isFolder: true}, true
			}
		}
	}
	zb := zone.Get(zones.PaneTreeBody)
	if zb == nil || !zb.InBounds(msg) || len(m.treeViewRows) == 0 {
		return treeHit{}, false
	}
	_, ry := zb.Pos(msg)
	if ry < 0 {
		return treeHit{}, false
	}
	top := m.treeView.YOffset
	contentLine := top + ry
	if contentLine < 0 {
		return treeHit{}, false
	}
	if contentLine >= len(m.treeViewRows) {
		contentLine = len(m.treeViewRows) - 1
	}
	vr := m.treeViewRows[contentLine]
	return treeHit{viewLine: contentLine, isFolder: !vr.isFile}, true
}
