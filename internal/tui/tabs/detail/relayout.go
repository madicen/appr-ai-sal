package detail

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) relayout() {
	w := m.width
	if w == 0 {
		w = m.host.Width()
	}
	if w == 0 || m.host.Height() == 0 {
		return
	}
	bodyH := m.bodyH
	if bodyH == 0 {
		bodyH = m.host.ChromeBodyHeight()
	}
	headerLine := lipgloss.Height(m.renderDetailMiniHeader())

	phs := prDetailPanel.GetHorizontalFrameSize()
	pvs := prDetailPanel.GetVerticalFrameSize()
	outerPaneH := max(1, bodyH-headerLine)
	if m.diffOnly {
		innerW := max(1, w-phs)
		titleH := measureDetailPaneTitle(innerW, "Diff (full width — d to restore)", m.focusedPane == paneDiff)
		vpH := max(1, outerPaneH-pvs-titleH)
		m.diffView.Width = max(8, w-phs)
		m.diffView.Height = vpH
		m.treeView.Width = 1
		m.treeView.Height = 1
		m.controlsView.Width = 1
		m.controlsView.Height = 1
		return
	}
	treeW := m.treePaneWidth + phs
	controlsOuterW := m.controlsPaneWidth + phs
	showControls := !m.controlsUserHidden
	if showControls {
		diffIfShown := w - treeW - controlsOuterW
		if diffIfShown < controlsAutoHideMinDiffWidth {
			showControls = false
		}
	}
	m.controlsHidden = !showControls
	if !showControls && m.focusedPane == paneControls {
		m.focusedPane = paneDiff
	}

	diffOuterW := w - treeW
	if showControls {
		diffOuterW = w - treeW - controlsOuterW
	}
	if diffOuterW < 12 {
		treeW = 12
		diffOuterW = w - treeW
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
	diffOuter := w - treeOuter - ctlOuter
	innerTreeW := max(1, treeOuter-phs)
	innerDiffW := max(1, diffOuter-phs)
	treeTitle := leftPaneTitle(m.focusedPane)
	treeTitleH := measureDetailPaneTitle(innerTreeW, treeTitle, paneFocusFor(paneTree, m.focusedPane))
	diffTitleH := measureDetailPaneTitle(innerDiffW, m.diffPaneTitle(), paneFocusFor(paneDiff, m.focusedPane))
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
}

func (m *Model) mouseYInChromeBody(msg tea.MouseMsg) bool {
	top := lipgloss.Height(m.host.RenderHeader())
	bottom := m.host.Height() - lipgloss.Height(m.host.RenderStatus())
	return msg.Y >= top && msg.Y < bottom
}
