package model

import (
	tea "github.com/charmbracelet/bubbletea"

	detailtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/detail"
)

// ensureDetailTab registers the PR detail Tab in the root registry when the
// user enters modeDetail (F5 item 4).
func (m *Model) ensureDetailTab() {
	if m.tabs == nil {
		m.tabs = map[mode]Tab{}
	}
	if m.tabs[modeDetail] == nil {
		m.tabs[modeDetail] = newTab(detailtab.New(m))
	}
}

// DetailHandleKey implements detail.Host.
func (m *Model) DetailHandleKey(msg tea.KeyMsg) tea.Cmd {
	_, cmd := m.handleDetailKey(msg)
	return cmd
}

// DetailHandleMouse implements detail.Host.
func (m *Model) DetailHandleMouse(msg tea.MouseMsg, wheel bool) (tea.Cmd, bool) {
	_, cmd := m.detailHandleMouse(msg, wheel)
	return cmd, cmd != nil
}

// DetailViewBody implements detail.Host — the three-pane PR body below the mini-header.
func (m *Model) DetailViewBody() string {
	return m.renderPRDetailBody(m.chromeBodyHeight())
}

// DetailRelayout implements detail.Host.
func (m *Model) DetailRelayout() { m.relayout() }

// DetailRefreshViews implements detail.Host.
func (m *Model) DetailRefreshViews() { m.refreshDetailViews() }

// DetailResize implements detail.Host.
func (m *Model) DetailResize(w, bodyH int) {
	if w > 0 {
		m.width = w
	}
	if bodyH > 0 {
		// bodyH is applied via relayout from WindowSizeMsg path; no-op when 0.
	}
	m.relayout()
}

// DetailSetContentOrigin implements detail.Host (detail panes are root-laid-out).
func (m *Model) DetailSetContentOrigin(_ int) {}
