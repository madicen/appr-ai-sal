package model

import (
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) relayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	bodyH := m.chromeBodyHeight()

	per := m.listPanelInputWidth()
	m.searchInput.Width = per
	m.urlInput.Width = per

	panelH := m.listPanelHeight()
	m.list.SetSize(m.width-2, max(3, bodyH-panelH))
}

func (m *Model) chromeBodyHeight() int {
	return max(1, m.height-lipgloss.Height(m.renderHeader())-lipgloss.Height(m.renderStatus()))
}
