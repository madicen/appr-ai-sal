package settings

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

// handleMouse handles press and wheel. Returns a command when save/cancel/strictness click completes.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if tea.MouseEvent(msg).IsWheel() {
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if z := zone.Get(ZoneSettingsTabReview); z != nil && z.InBounds(msg) {
		m.setPanelTab(0)
		return nil
	}
	if z := zone.Get(ZoneSettingsTabRepoCtx); z != nil && z.InBounds(msg) {
		m.setPanelTab(1)
		return nil
	}
	if z := zone.Get(ZoneSettingsTabTheme); z != nil && z.InBounds(msg) {
		m.setPanelTab(2)
		return nil
	}
	if z := zone.Get(ZoneSettingsSave); z != nil && z.InBounds(msg) {
		switch m.panelTab {
		case 1:
			return m.submitRepoSave()
		case 2:
			return m.submitThemeSave()
		default:
			return m.submitSave()
		}
	}
	if z := zone.Get(ZoneSettingsCancel); z != nil && z.InBounds(msg) {
		return func() tea.Msg { return state.NavigateMsg{Target: state.NavigateTarget{Kind: state.NavBack, Cancelled: true}} }
	}
	if m.panelTab == 2 && m.theme != nil {
		if z := zone.Get(ZoneThemeReset); z != nil && z.InBounds(msg) {
			m.theme.resetAll()
			return nil
		}
		// Forward the press to the swatch whose row is in bounds; the
		// SwatchPicker library opens the modal on press (zones fire on
		// release), so we deliver the original press directly.
		for i, sw := range m.theme.swatches {
			if z := zone.Get(sw.zoneID); z != nil && z.InBounds(msg) {
				m.theme.swatches[i].swatch.SetFocused(false)
				m.theme.focus = i
				m.theme.swatches[i].swatch.SetFocused(true)
				updated, cmd := m.theme.swatches[i].swatch.Update(msg)
				m.theme.swatches[i].swatch = updated
				return cmd
			}
		}
	}
	if z := zone.Get(ZoneStrictCriticalOnly); z != nil && z.InBounds(msg) {
		m.strictIdx = 0
		m.draft.ReviewStrictness = aiconfig.ReviewCriticalOnly
		m.focus = fieldStrictness
		m.blurInputs()
		return nil
	}
	if z := zone.Get(ZoneStrictLenient); z != nil && z.InBounds(msg) {
		m.strictIdx = 1
		m.draft.ReviewStrictness = aiconfig.ReviewLenient
		m.focus = fieldStrictness
		m.blurInputs()
		return nil
	}
	if z := zone.Get(ZoneStrictBalanced); z != nil && z.InBounds(msg) {
		m.strictIdx = 2
		m.draft.ReviewStrictness = aiconfig.ReviewBalanced
		m.focus = fieldStrictness
		m.blurInputs()
		return nil
	}
	if z := zone.Get(ZoneStrictStrict); z != nil && z.InBounds(msg) {
		m.strictIdx = 3
		m.draft.ReviewStrictness = aiconfig.ReviewStrict
		m.focus = fieldStrictness
		m.blurInputs()
		return nil
	}
	if m.panelTab == 0 {
		// Profile row clicks: select that row.
		for i := range m.draft.Profiles {
			if z := zone.Get(ZoneProfileRow(i)); z != nil && z.InBounds(msg) {
				m.commitEditorToSelectedProfile()
				m.selectedProfileIdx = i
				m.loadEditorFromSelectedProfile()
				m.focus = fieldProfilePicker
				m.blurInputs()
				return nil
			}
		}
		if z := zone.Get(ZoneProfileSetActive); z != nil && z.InBounds(msg) {
			m.focus = fieldProfilePicker
			m.blurInputs()
			return m.activateSelectedProfile()
		}
		if z := zone.Get(ZoneProfileAdd); z != nil && z.InBounds(msg) {
			return m.addNewProfile()
		}
		if z := zone.Get(ZoneProfileDelete); z != nil && z.InBounds(msg) {
			m.focus = fieldProfilePicker
			m.blurInputs()
			return m.deleteSelectedProfile()
		}
	}
	if m.panelTab == 1 {
		if z := zone.Get(ZoneRepoToggleIncludePR); z != nil && z.InBounds(msg) {
			m.repoIncludePR = !m.repoIncludePR
			m.focusRepoField(repoFieldIncludePR)
			return nil
		}
		if z := zone.Get(ZoneRepoToggleCulture); z != nil && z.InBounds(msg) {
			m.repoCultureSum = !m.repoCultureSum
			m.focusRepoField(repoFieldCultureSum)
			return nil
		}
		if z := zone.Get(ZoneRepoToggleCtxVs); z != nil && z.InBounds(msg) {
			m.repoCtxVsChange = !m.repoCtxVsChange
			m.focusRepoField(repoFieldCtxVsChange)
			return nil
		}
		if z := zone.Get(ZoneRepoToggleExpert); z != nil && z.InBounds(msg) {
			m.repoExpertPanel = !m.repoExpertPanel
			m.focusRepoField(repoFieldExpertPanel)
			return nil
		}
		if z := zone.Get(ZoneRepoToggleParallelSpecs); z != nil && z.InBounds(msg) {
			m.repoParallelSpecs = !m.repoParallelSpecs
			m.focusRepoField(repoFieldParallelSpecs)
			return nil
		}
		if z := zone.Get(ZoneRepoToggleParallelExperts); z != nil && z.InBounds(msg) {
			m.repoParallelExperts = !m.repoParallelExperts
			m.focusRepoField(repoFieldParallelExperts)
			return nil
		}
	}
	return nil
}
