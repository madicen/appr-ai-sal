package settings

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
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
	if z := zone.Get(ZoneSettingsSave); z != nil && z.InBounds(msg) {
		if m.panelTab == 1 {
			return m.submitRepoSave()
		}
		return m.submitSave()
	}
	if z := zone.Get(ZoneSettingsCancel); z != nil && z.InBounds(msg) {
		return func() tea.Msg { return DoneMsg{Cancelled: true} }
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
