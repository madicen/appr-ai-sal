package model

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// startReviewOverlay constructs the review overlay, pushes it onto the
// stack, and kicks off the runner. When peruse is true, the overlay's
// peruse flag is set so post / skip actions become no-ops with a flash
// hint — the user can browse findings and the rendered summary without
// committing anything to GitHub.
func (m *Model) relayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	bodyH := m.chromeBodyHeight()
	headerLine := lipgloss.Height(m.renderDetailMiniHeader())

	filterH := lipgloss.Height(renderFilterLine(m.explicitReviewerOnly, !m.prsLoaded))
	m.list.SetSize(m.width-2, max(3, bodyH-filterH))

	switch m.mode {
	case modeDetail:
		phs := prDetailPanel.GetHorizontalFrameSize()
		pvs := prDetailPanel.GetVerticalFrameSize()
		// Outer pane height matches renderPRDetailBody (chrome body minus mini header).
		outerPaneH := max(1, bodyH-headerLine)
		if m.diffOnly {
			innerW := max(1, m.width-phs)
			titleH := measureDetailPaneTitle(innerW, "Diff (full width — d to restore)", m.focusedPane == paneDiff)
			vpH := max(1, outerPaneH-pvs-titleH)
			m.diffView.Width = max(8, m.width-phs)
			m.diffView.Height = vpH
			m.treeView.Width = 1
			m.treeView.Height = 1
			break
		}
		treeW := treePaneWidth + phs
		diffOuterW := m.width - treeW
		if diffOuterW < 12 {
			treeW = 12
			diffOuterW = m.width - treeW
		}
		m.treeView.Width = max(8, treeW-phs)
		treeOuter := m.treeView.Width + phs
		diffOuter := m.width - treeOuter
		innerTreeW := max(1, treeOuter-phs)
		innerDiffW := max(1, diffOuter-phs)
		treeTitle := "Files · " + focusHint(paneTree, m.focusedPane)
		treeTitleH := measureDetailPaneTitle(innerTreeW, treeTitle, paneFocusFor(paneTree, m.focusedPane))
		diffTitleH := measureDetailPaneTitle(innerDiffW, m.diffPaneTitle(), paneFocusFor(paneDiff, m.focusedPane))
		m.treeView.Height = max(1, outerPaneH-pvs-treeTitleH)
		m.diffView.Width = max(8, diffOuter-phs)
		m.diffView.Height = max(1, outerPaneH-pvs-diffTitleH)
	default:
		m.treeView.Width = 1
		m.treeView.Height = 1
		m.diffView.Width = 1
		m.diffView.Height = 1
	}

	m.urlInput.Width = m.width - 6
}

func (m *Model) chromeBodyHeight() int {
	return max(1, m.height-lipgloss.Height(m.renderHeader())-lipgloss.Height(m.renderStatus()))
}

func (m *Model) mouseYInChromeBody(msg tea.MouseMsg) bool {
	top := lipgloss.Height(m.renderHeader())
	bottom := m.height - lipgloss.Height(m.renderStatus())
	return msg.Y >= top && msg.Y < bottom
}
