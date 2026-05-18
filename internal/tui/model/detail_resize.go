package model

import (
	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

// detailPaneRowYRange returns the inclusive [top, bottom] Y range that
// the three-pane row occupies on the PR detail screen. Mirrors the
// vertical math relayout / renderPRDetailBody use:
//
//	chromeTop = height(header)
//	bodyH    = chromeBodyHeight()                 (excludes header + status)
//	miniH    = height(mini-header inside chrome)
//	paneTop  = chromeTop + miniH
//	paneBot  = chromeTop + bodyH - 1
//
// Returns ok=false when the model has no useful geometry (zero-size
// terminal, or not in modeDetail) so callers can early-return.
func (m *Model) detailPaneRowYRange() (top, bottom int, ok bool) {
	if m.width == 0 || m.height == 0 || m.mode != modeDetail {
		return 0, 0, false
	}
	chromeTop := lipgloss.Height(m.renderHeader())
	bodyH := m.chromeBodyHeight()
	miniH := lipgloss.Height(m.renderDetailMiniHeader())
	paneTop := chromeTop + miniH
	paneBot := chromeTop + bodyH - 1
	if paneBot < paneTop {
		return 0, 0, false
	}
	return paneTop, paneBot, true
}

// detailSeamColumns returns the column boundaries of the in-flight pane
// layout: the left edge of the diff pane (treeOuter) and the left edge
// of the controls pane (treeOuter + diffOuter). When the controls pane
// is hidden, controlsLeft == m.width and the second seam is unreachable.
func (m *Model) detailSeamColumns() (treeOuter, controlsLeft int) {
	phs := prDetailPanel.GetHorizontalFrameSize()
	treeOuter = m.treeView.Width + phs
	if m.controlsHidden {
		return treeOuter, m.width
	}
	ctlOuter := m.controlsView.Width + phs
	diffOuter := m.width - treeOuter - ctlOuter
	return treeOuter, treeOuter + diffOuter
}

// seamAtPoint resolves a (mouse) cell coordinate to a divider target.
// The seam is the 2-column-wide region straddling each pane boundary:
// the rightmost column of the left pane (its right border, treeOuter-1)
// and the leftmost column of the right pane (its left border, treeOuter).
//
// Returns dividerNone when the point is outside the pane row, when the
// detail view is in diffOnly mode (no seams; full-width diff), or when
// the click falls in pane interior rather than on a boundary.
func (m *Model) seamAtPoint(x, y int) dividerTarget {
	if m.diffOnly {
		return dividerNone
	}
	top, bottom, ok := m.detailPaneRowYRange()
	if !ok || y < top || y > bottom {
		return dividerNone
	}
	treeOuter, controlsLeft := m.detailSeamColumns()
	switch {
	case x == treeOuter-1 || x == treeOuter:
		return dividerTreeDiff
	case !m.controlsHidden && (x == controlsLeft-1 || x == controlsLeft):
		return dividerDiffControls
	}
	return dividerNone
}

// startPaneDrag arms a drag against the given seam, anchoring the
// origin so motion events can compute absolute widths from the press
// point. Anchoring at press time (rather than accumulating per-event
// deltas) avoids rounding drift on terminals that batch motion reports.
func (m *Model) startPaneDrag(msg tea.MouseMsg, target dividerTarget) {
	m.paneDrag = paneDrag{
		target:          target,
		originX:         msg.X,
		originTreeW:     m.treePaneWidth,
		originControlsW: m.controlsPaneWidth,
	}
}

// updatePaneDrag applies a motion event to the active drag, clamping
// the resulting widths so neither flanking pane drops below its
// minimum and so the diff never gets squeezed below
// controlsAutoHideMinDiffWidth (matching the auto-hide threshold).
//
// Returns false when no drag is active or the drag was already over —
// callers can short-circuit redundant relayout work in that case.
func (m *Model) updatePaneDrag(msg tea.MouseMsg) bool {
	if m.paneDrag.target == dividerNone {
		return false
	}
	delta := msg.X - m.paneDrag.originX
	phs := prDetailPanel.GetHorizontalFrameSize()
	switch m.paneDrag.target {
	case dividerTreeDiff:
		// Drag right widens the tree pane; left narrows it. The diff
		// absorbs whatever's left over (computed by relayout) so we
		// just need to clamp the tree width directly.
		want := m.paneDrag.originTreeW + delta
		// Upper bound: leave room for diff (>= min) + controls (if shown).
		ctlReserve := 0
		if !m.controlsHidden {
			ctlReserve = m.controlsPaneWidth + phs
		}
		maxTree := m.width - phs - ctlReserve - controlsAutoHideMinDiffWidth
		if maxTree < minTreePaneWidth {
			maxTree = minTreePaneWidth
		}
		m.treePaneWidth = clampInt(want, minTreePaneWidth, maxTree)
	case dividerDiffControls:
		// Drag right shrinks the controls pane (diff grows); left
		// grows it (diff shrinks). Anchor on originControlsW.
		want := m.paneDrag.originControlsW - delta
		treeReserve := m.treePaneWidth + phs
		maxCtl := m.width - phs - treeReserve - controlsAutoHideMinDiffWidth
		if maxCtl < minControlsPaneWidth {
			maxCtl = minControlsPaneWidth
		}
		m.controlsPaneWidth = clampInt(want, minControlsPaneWidth, maxCtl)
	default:
		return false
	}
	return true
}

// endPaneDrag clears the active drag. Idempotent — safe to call from
// the catch-all release branch even when no drag is in flight.
func (m *Model) endPaneDrag() {
	m.paneDrag = paneDrag{target: dividerNone}
}

// paneDragActive reports whether a seam drag is currently in flight.
// Used by the renderer to swap in the accent border style on the panes
// flanking the active seam.
func (m *Model) paneDragActive() bool {
	return m.paneDrag.target != dividerNone
}
