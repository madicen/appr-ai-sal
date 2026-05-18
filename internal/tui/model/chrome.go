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

	// Size the panel inputs first so the panel height we measure below
	// reflects whatever the inputs will actually render at this width.
	// listPanelInputWidth splits the panel's inner horizontal budget
	// evenly between the two fields after subtracting gutters.
	per := m.listPanelInputWidth()
	m.searchInput.Width = per
	m.urlInput.Width = per

	panelH := m.listPanelHeight()
	m.list.SetSize(m.width-2, max(3, bodyH-panelH))

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
			m.controlsView.Width = 1
			m.controlsView.Height = 1
			break
		}
		treeW := m.treePaneWidth + phs
		controlsOuterW := m.controlsPaneWidth + phs
		// Decide whether to host the controls pane this frame. Auto-hide
		// when the diff would otherwise become unusably narrow; respect
		// the user's explicit hide preference too.
		showControls := !m.controlsUserHidden
		if showControls {
			diffIfShown := m.width - treeW - controlsOuterW
			if diffIfShown < controlsAutoHideMinDiffWidth {
				showControls = false
			}
		}
		m.controlsHidden = !showControls
		if !showControls && m.focusedPane == paneControls {
			m.focusedPane = paneDiff
		}

		// Compute remaining width for the diff and clamp the tree if the
		// terminal is so narrow it can't host even tree+diff comfortably.
		diffOuterW := m.width - treeW
		if showControls {
			diffOuterW = m.width - treeW - controlsOuterW
		}
		if diffOuterW < 12 {
			treeW = 12
			diffOuterW = m.width - treeW
			if showControls {
				diffOuterW -= controlsOuterW
			}
		}
		m.treeView.Width = max(8, treeW-phs)
		treeOuter := m.treeView.Width + phs

		var ctlOuter int
		if showControls {
			m.controlsView.Width = max(8, controlsOuterW-phs)
			ctlOuter = m.controlsView.Width + phs
		} else {
			m.controlsView.Width = 1
			ctlOuter = 0
		}
		diffOuter := m.width - treeOuter - ctlOuter
		innerTreeW := max(1, treeOuter-phs)
		innerDiffW := max(1, diffOuter-phs)
		treeTitle := leftPaneTitle(m.focusedPane)
		treeTitleH := measureDetailPaneTitle(innerTreeW, treeTitle, paneFocusFor(paneTree, m.focusedPane))
		diffTitleH := measureDetailPaneTitle(innerDiffW, m.diffPaneTitle(), paneFocusFor(paneDiff, m.focusedPane))
		// Reserve space for the PR-overview selector that lives above
		// the file tree viewport. measure under the same outer width so
		// we get the same wrapped heights renderPRDetailBody will see.
		overviewH := m.leftColumnOverviewLeaderHeight(treeOuter)
		m.treeView.Height = max(1, outerPaneH-pvs-treeTitleH-overviewH)
		m.diffView.Width = max(8, diffOuter-phs)
		m.diffView.Height = max(1, outerPaneH-pvs-diffTitleH)
		if showControls {
			innerCtlW := max(1, ctlOuter-phs)
			ctlTitleH := measureDetailPaneTitle(innerCtlW, controlsPaneTitle(m.focusedPane), paneFocusFor(paneControls, m.focusedPane))
			m.controlsView.Height = max(1, outerPaneH-pvs-ctlTitleH)
		} else {
			m.controlsView.Height = 1
		}
	default:
		m.treeView.Width = 1
		m.treeView.Height = 1
		m.diffView.Width = 1
		m.diffView.Height = 1
		m.controlsView.Width = 1
		m.controlsView.Height = 1
	}
}

func (m *Model) chromeBodyHeight() int {
	return max(1, m.height-lipgloss.Height(m.renderHeader())-lipgloss.Height(m.renderStatus()))
}

func (m *Model) mouseYInChromeBody(msg tea.MouseMsg) bool {
	top := lipgloss.Height(m.renderHeader())
	bottom := m.height - lipgloss.Height(m.renderStatus())
	return msg.Y >= top && msg.Y < bottom
}
