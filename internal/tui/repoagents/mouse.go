package repoagents

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
)

// handleMouse processes a single mouse event. Returns a non-nil command when
// a click maps to an action; nil for ignored events (so the parent can fall
// through to wheel scrolling).
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if tea.MouseEvent(msg).IsWheel() {
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}

	if m.editing {
		if z := zone.Get(ZoneEditSave); z != nil && z.InBounds(msg) {
			return m.commitEdit()
		}
		if z := zone.Get(ZoneEditCancel); z != nil && z.InBounds(msg) {
			m.editing = false
			m.editKind = editKindNone
			m.editArea.Blur()
			return nil
		}
		return nil
	}

	if z := zone.Get(ZoneClose); z != nil && z.InBounds(msg) {
		return func() tea.Msg { return DoneMsg{} }
	}
	if z := zone.Get(ZonePrevRepo); z != nil && z.InBounds(msg) {
		m.movePrevRepo()
		return m.maybeLoadAgentsCmd()
	}
	if z := zone.Get(ZoneNextRepo); z != nil && z.InBounds(msg) {
		m.moveNextRepo()
		return m.maybeLoadAgentsCmd()
	}
	if m.addingRepo {
		if z := zone.Get(ZoneAddRepoSave); z != nil && z.InBounds(msg) {
			return m.commitAddRepo()
		}
		if z := zone.Get(ZoneAddRepoCancel); z != nil && z.InBounds(msg) {
			m.addingRepo = false
			m.addInput.Blur()
			return nil
		}
	} else {
		if z := zone.Get(ZoneAddRepoOpen); z != nil && z.InBounds(msg) {
			m.openAddRepo()
			return nil
		}
		if z := zone.Get(ZoneRemoveRepo); z != nil && z.InBounds(msg) {
			return m.removeCurrentRepoCmd()
		}
	}
	if m.addingTech {
		if z := zone.Get(ZoneAddTechSave); z != nil && z.InBounds(msg) {
			return m.commitAddTech()
		}
		if z := zone.Get(ZoneAddTechCancel); z != nil && z.InBounds(msg) {
			m.cancelAddTech()
			return nil
		}
	} else {
		if z := zone.Get(ZoneAddTechOpen); z != nil && z.InBounds(msg) {
			m.openAddTech()
			return nil
		}
	}
	if z := zone.Get(ZoneRegenAll); z != nil && z.InBounds(msg) {
		return m.regenerateAllForCurrentRepo()
	}

	for _, name := range ra.Specialists {
		if z := zone.Get(zoneAgentRegen(name)); z != nil && z.InBounds(msg) {
			return m.startRegenerate(name)
		}
		if z := zone.Get(zoneAgentEdit(name)); z != nil && z.InBounds(msg) {
			m.startEdit(name)
			return nil
		}
		if z := zone.Get(zoneAgentDelete(name)); z != nil && z.InBounds(msg) {
			return m.startDelete(name)
		}
	}

	// Per-tech chips: the configured set is dynamic, so we iterate the
	// current repo's loaded TechAgents rather than a fixed list.
	if cur := m.currentTechs(); cur != nil {
		for _, t := range cur.SortedTechs() {
			if z := zone.Get(zoneTechRegen(t)); z != nil && z.InBounds(msg) {
				return m.startRegenerateTech(t)
			}
			if z := zone.Get(zoneTechEditBrief(t)); z != nil && z.InBounds(msg) {
				m.startEditTech(t)
				return nil
			}
			if z := zone.Get(zoneTechDelete(t)); z != nil && z.InBounds(msg) {
				return m.startDeleteTech(t)
			}
		}
	}
	return nil
}

func (m *Model) removeCurrentRepoCmd() tea.Cmd {
	owner, repo := splitRepoKey(m.currentRepoKey())
	if owner == "" || repo == "" {
		return nil
	}
	return func() tea.Msg {
		err := ra.DeleteRepo(owner, repo)
		return repoRemovedMsg{Owner: owner, Repo: repo, Err: err}
	}
}
