package model

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	wheel := tea.MouseEvent(msg).IsWheel()

	switch m.mode {
	case modeList:
		// List mode has no drag affordances; non-press, non-wheel events
		// are dropped here so the list view doesn't re-render on every
		// motion event the terminal emits while a button is held.
		if !wheel && msg.Action != tea.MouseActionPress {
			return m, nil
		}
		listTop := m.listBodyOriginY()
		if wheel && !m.list.SettingFilter() && msg.Y >= listTop && msg.Y < listTop+m.list.Height() {
			switch msg.Button {
			case tea.MouseButtonWheelDown:
				m.resetListClickTracking()
				m.list.CursorDown()
				return m, nil
			case tea.MouseButtonWheelUp:
				m.resetListClickTracking()
				m.list.CursorUp()
				return m, nil
			}
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(zones.FilterToggle); z != nil && z.InBounds(msg) {
				m.resetListClickTracking()
				m.explicitReviewerOnly = !m.explicitReviewerOnly
				m.updateListTitle()
				return m, m.refreshPRListCmd()
			}
			if z := zone.Get(zones.RefreshList); z != nil && z.InBounds(msg) {
				m.resetListClickTracking()
				return m, m.refreshPRListCmd()
			}
			if gi, ok := m.listGlobalIndexAtClick(msg); ok {
				return m.listHandleItemClick(gi)
			}
		}
		var lcmd tea.Cmd
		m.list, lcmd = m.list.Update(msg)
		return m, lcmd
	case modeDetail:
		// Detail mode routes ALL mouse events (press, motion, release,
		// wheel) into detailHandleMouse so it can drive the seam-drag
		// state machine. Filtering for press-only here would drop the
		// motion + release events the resize handler depends on.
		return m.detailHandleMouse(msg, wheel)
	}
	return m, nil
}

func zoneInBounds(id string, msg tea.MouseMsg) bool {
	z := zone.Get(id)
	return z != nil && z.InBounds(msg)
}
