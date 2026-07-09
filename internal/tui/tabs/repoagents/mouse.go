package repoagents

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	ta "github.com/madicen/appr-ai-sal/internal/review/techagents"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// handleMouse maps a left-click to an action via an ordered click table (see
// zones.DispatchClick). Modal edit state short-circuits everything else;
// otherwise the table is flat — only one panel's zones are ever registered
// at a time, so unrendered zones simply miss (matching the original
// fall-through if-chain). Wheel / non-press events return nil.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.editing {
		return zones.DispatchClick(msg, []zones.ClickHandler{
			{Zone: ZoneEditSave, Do: m.commitEdit},
			{Zone: ZoneEditCancel, Do: func() tea.Cmd { m.editing = false; m.editKind = editKindNone; m.editArea.Blur(); return nil }},
		})
	}
	h := []zones.ClickHandler{
		{Zone: ZoneClose, Do: func() tea.Cmd { return state.NavigateTarget{Kind: state.NavBack}.Cmd() }},
		{Zone: ZoneRepoDD, Do: func() tea.Cmd { return m.forwardToRepoDropdown(msg) }},
		{Zone: ZoneAddRepoSave, Do: m.commitAddRepo},
		{Zone: ZoneAddRepoCancel, Do: func() tea.Cmd { m.addingRepo = false; m.addInput.Blur(); return nil }},
		{Zone: ZoneAddRepoField, Do: func() tea.Cmd { m.addInput.Focus(); return textinput.Blink }},
		{Zone: ZoneAddRepoOpen, Do: func() tea.Cmd { m.openAddRepo(); return nil }},
		{Zone: ZoneRemoveRepo, Do: m.removeCurrentRepoCmd},
		{Zone: ZoneAddTechSave, Do: m.commitAddTech},
		{Zone: ZoneAddTechCancel, Do: func() tea.Cmd { m.cancelAddTech(); return nil }},
		{Zone: ZoneAddTechName, Do: func() tea.Cmd { m.focusTechName(); return textinput.Blink }},
		{Zone: ZoneAddTechSeed, Do: func() tea.Cmd { m.focusTechSeed(); return textinput.Blink }},
		{Zone: ZoneAddTechOpen, Do: func() tea.Cmd { m.openAddTech(); return nil }},
		{Zone: ZoneSuggestTech, Do: m.startSuggestTechs},
		{Zone: ZoneGenApproved, Do: m.generateApprovedCmd},
		{Zone: ZoneDismissSuggest, Do: func() tea.Cmd { m.dismissSuggestions(); return nil }},
		{Zone: ZoneRegenAll, Do: m.regenerateAllForCurrentRepo},
	}
	h = append(h, zones.ForEachRowZone(m.candidates, func(c ta.Candidate) []zones.ClickHandler {
		t := ta.CanonicalTech(c.Tech)
		return []zones.ClickHandler{
			{Zone: zoneCandApprove(t), Do: func() tea.Cmd { m.setCandidateApproval(t, true); return nil }},
			{Zone: zoneCandDeny(t), Do: func() tea.Cmd { m.setCandidateApproval(t, false); return nil }},
		}
	})...)
	h = append(h, zones.ForEachRowZone(ra.Specialists, func(name string) []zones.ClickHandler {
		return []zones.ClickHandler{
			{Zone: zoneAgentRegen(name), Do: func() tea.Cmd { return m.startRegenerate(name) }},
			{Zone: zoneAgentEdit(name), Do: func() tea.Cmd { m.startEdit(name); return nil }},
			{Zone: zoneAgentDelete(name), Do: func() tea.Cmd { return m.startDelete(name) }},
		}
	})...)
	if cur := m.currentTechs(); cur != nil {
		h = append(h, zones.ForEachRowZone(cur.SortedTechs(), func(t string) []zones.ClickHandler {
			return []zones.ClickHandler{
				{Zone: zoneTechRegen(t), Do: func() tea.Cmd { return m.startRegenerateTech(t) }},
				{Zone: zoneTechEditBrief(t), Do: func() tea.Cmd { m.startEditTech(t); return nil }},
				{Zone: zoneTechDelete(t), Do: func() tea.Cmd { return m.startDeleteTech(t) }},
			}
		})...)
	}
	return zones.DispatchClick(msg, h)
}

func (m *Model) removeCurrentRepoCmd() tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		return nil
	}
	return func() tea.Msg {
		err := ra.DeleteRepo(owner, repo)
		return repoRemovedMsg{Key: repoKey{owner, repo}, Err: err}
	}
}
