package langagents

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/tui/state"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// handleMouse mirrors the g/r/d/esc keys via a click table (see
// zones.DispatchClick): row click selects, per-row buttons
// generate/refresh/delete, Close navigates back.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	h := []zones.ClickHandler{
		{Zone: ZoneClose, Do: func() tea.Cmd { return state.NavigateTarget{Kind: state.NavBack, Cancelled: true}.Cmd() }},
	}
	for i := range m.rows {
		l := m.rows[i].Language
		h = append(h, []zones.ClickHandler{
			{Zone: zoneRowGen(l), Do: func() tea.Cmd { m.idx = i; _, c := m.actionGenerate(); return c }},
			{Zone: zoneRowDel(l), Do: func() tea.Cmd { m.idx = i; _, c := m.actionDelete(); return c }},
			{Zone: zoneRow(l), Do: func() tea.Cmd { m.idx = i; return nil }},
		}...)
	}
	return zones.DispatchClick(msg, h)
}
