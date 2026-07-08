package model

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global overlays (Phase 5 items 1 + 6): the `?` help overlay and the
	// ctrl+k command palette are available from the root-native list and
	// detail screens, but NOT while an inline text field owns input (so a
	// user can still type `?` into the search / URL box). globalKeysActive
	// encodes that gate.
	if m.globalKeysActive() {
		switch {
		case key.Matches(msg, m.keys.Palette):
			return m, m.openCommandPalette()
		case key.Matches(msg, m.keys.Help):
			return m, m.openHelpOverlay()
		}
	}
	switch m.mode {
	case modeList:
		return m.handleListKey(msg)
	case modeDetail:
		return m.handleDetailKey(msg)
	}
	return m, nil
}

// globalKeysActive reports whether the root-native screens should intercept
// the global `?` / ctrl+k bindings for the current focus. On the list
// screen they are suppressed while a text input is focused so those keys
// keep typing into the field; on the detail screen there is no text input
// so they are always live.
func (m *Model) globalKeysActive() bool {
	switch m.mode {
	case modeList:
		return m.listFocus == focusList
	case modeDetail:
		return true
	}
	return false
}
