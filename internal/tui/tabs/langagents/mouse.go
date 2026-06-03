package langagents

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

// handleMouse processes a single mouse event for the lang-experts tab.
// Returns a non-nil command when a click maps to an action; nil for
// ignored events (wheel, non-press, misses) so the tab stays inert
// otherwise. Mirrors the g/r/d/esc keys so the tab is fully
// mouse-drivable — clicking a row selects it, the per-row buttons
// generate/refresh/delete, and the footer Close navigates back.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if tea.MouseEvent(msg).IsWheel() {
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if z := zone.Get(ZoneClose); z != nil && z.InBounds(msg) {
		return state.NavigateTarget{Kind: state.NavBack, Cancelled: true}.Cmd()
	}
	for i, r := range m.rows {
		if z := zone.Get(zoneRowGen(r.Language)); z != nil && z.InBounds(msg) {
			m.idx = i
			_, cmd := m.actionGenerate()
			return cmd
		}
		if z := zone.Get(zoneRowDel(r.Language)); z != nil && z.InBounds(msg) {
			m.idx = i
			_, cmd := m.actionDelete()
			return cmd
		}
		if z := zone.Get(zoneRow(r.Language)); z != nil && z.InBounds(msg) {
			m.idx = i
			return nil
		}
	}
	return nil
}
