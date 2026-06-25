package repoagents

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	bubbledropdown "github.com/madicen/bubble-dropdown"
)

// newRepoDropdown builds the active-repository dropdown from the current repo
// list. The component has no runtime SetOptions, so it is recreated whenever
// the list or selection changes (see refreshRepoDropdown).
func (m *Model) newRepoDropdown() *bubbledropdown.Dropdown {
	idx := m.repoIdx
	if idx < 0 || idx >= len(m.repos) {
		idx = 0
	}
	opts := make([]string, len(m.repos))
	copy(opts, m.repos)
	d := bubbledropdown.New(
		bubbledropdown.WithOptions(opts),
		bubbledropdown.WithInitialIndex(idx),
		bubbledropdown.WithPlaceholder("select repo"),
	)
	// Match the existing bubblezone integration: trigger hit-testing runs
	// through the global manager scanned at the root.
	d.SetZoneManager(zone.DefaultManager)
	return d
}

// refreshRepoDropdown keeps the dropdown's options/selection in sync with the
// repo list while the panel is closed (an open panel implies no change).
func (m *Model) refreshRepoDropdown() {
	if len(m.repos) == 0 {
		m.repoDD = nil
		return
	}
	if m.repoDD != nil && m.repoDD.Open() {
		return
	}
	m.repoDD = m.newRepoDropdown()
}

// repoDropdownOpen reports whether the repo dropdown panel is open.
func (m *Model) repoDropdownOpen() bool {
	return m.repoDD != nil && m.repoDD.Open()
}

// forwardToRepoDropdown routes msg to the dropdown, translating mouse
// coordinates into body-local space, and applies any resulting selection
// change by switching the active repo.
func (m *Model) forwardToRepoDropdown(msg tea.Msg) tea.Cmd {
	if m.repoDD == nil {
		return nil
	}
	if mm, ok := msg.(tea.MouseMsg); ok {
		mm.Y -= m.contentTop
		msg = mm
	}
	prev := m.repoDD.SelectedIndex()
	updated, cmd := m.repoDD.Update(msg)
	m.repoDD = updated
	if sel := m.repoDD.SelectedIndex(); sel != prev {
		if applyCmd := m.selectRepoByIndex(sel); applyCmd != nil {
			cmd = tea.Batch(cmd, applyCmd)
		}
	}
	return cmd
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
}
