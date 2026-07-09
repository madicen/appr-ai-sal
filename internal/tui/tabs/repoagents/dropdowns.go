package repoagents

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/tui/util/dropdown"
)

// refreshRepoDropdown keeps the dropdown's options/selection in sync with the
// repo list while the panel is closed (an open panel implies no change). The
// shared dropdown.Host owns creation, zone binding, and recreate-on-change.
func (m *Model) refreshRepoDropdown() {
	if len(m.repos) == 0 {
		m.repoDD.Clear()
		return
	}
	if m.repoDD == nil {
		m.repoDD = dropdown.New("select repo")
		m.repoDD.ContentTop = m.contentTop
		m.repoDD.OnSelect = m.selectRepoByIndex
	}
	idx := m.repoIdx
	if idx < 0 || idx >= len(m.repos) {
		idx = 0
	}
	opts := make([]string, len(m.repos))
	copy(opts, m.repos)
	m.repoDD.Rebuild(opts, idx)
}

// repoDropdownOpen reports whether the repo dropdown panel is open.
func (m *Model) repoDropdownOpen() bool {
	return m.repoDD.Open()
}

// forwardToRepoDropdown routes msg to the dropdown; the Host translates mouse
// coordinates into body-local space and applies a selection change via its
// OnSelect callback (selectRepoByIndex).
func (m *Model) forwardToRepoDropdown(msg tea.Msg) tea.Cmd {
	return m.repoDD.Forward(msg)
}

// selectRepoByIndex switches the active repo to idx and loads its agents.
func (m *Model) selectRepoByIndex(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.repos) || idx == m.repoIdx {
		return nil
	}
	m.repoIdx = idx
	m.resetSuggestions()
	return m.maybeLoadAgentsCmd()
}

// SetContentOrigin records the absolute terminal row where the tab body
// begins (the chrome header height) so an open dropdown's geometric mouse
// hit-test aligns with the on-screen panel.
func (m *Model) SetContentOrigin(top int) {
	if top < 0 {
		top = 0
	}
	m.contentTop = top
	if m.repoDD != nil {
		m.repoDD.ContentTop = top
	}
}
