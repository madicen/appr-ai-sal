package settings

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/tui/state"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// handleMouse maps a left-click to an action via a click table (see
// zones.DispatchClick). The tab strip and Save/Cancel are always live; the
// remaining targets are scoped to the active panel so a stale zone from
// another panel (bubblezone's manager is global) can't shadow the hit.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	h := []zones.ClickHandler{
		{Zone: ZoneSettingsTabReview, Do: func() tea.Cmd { m.setPanelTab(0); return nil }},
		{Zone: ZoneSettingsTabRepoCtx, Do: func() tea.Cmd { m.setPanelTab(1); return nil }},
		{Zone: ZoneSettingsTabTheme, Do: func() tea.Cmd { m.setPanelTab(2); return nil }},
		{Zone: ZoneSettingsSave, Do: m.submitCurrentSave},
		{Zone: ZoneSettingsCancel, Do: cancelCmd},
	}
	switch m.panelTab {
	case 0:
		h = append(h, m.reviewPanelHandlers(msg)...)
	case 1:
		h = append(h, m.repoPanelHandlers()...)
	case 2:
		h = append(h, m.themePanelHandlers(msg)...)
	}
	return zones.DispatchClick(msg, h)
}

// reviewPanelHandlers are the click targets on the Review & AI panel.
func (m *Model) reviewPanelHandlers(msg tea.MouseMsg) []zones.ClickHandler {
	return []zones.ClickHandler{
		{Zone: ZoneStrictnessDD, Do: m.openDropdownCmd(ddStrictness, msg)},
		{Zone: ZoneProfileDD, Do: m.openDropdownCmd(ddProfile, msg)},
		{Zone: ZoneProviderDD, Do: m.openDropdownCmd(ddProvider, msg)},
		{Zone: ZoneProfileSetActive, Do: m.profileActionCmd(m.activateSelectedProfile)},
		{Zone: ZoneProfileAdd, Do: m.addNewProfile},
		{Zone: ZoneProfileDelete, Do: m.profileActionCmd(m.deleteSelectedProfile)},
		{Zone: ZoneAIFieldName, Do: m.aiFieldCmd(fieldProfileName)},
		{Zone: ZoneAIFieldBaseURL, Do: m.aiFieldCmd(fieldBaseURL)},
		{Zone: ZoneAIFieldModel, Do: m.aiFieldCmd(fieldModel)},
		{Zone: ZoneAIFieldAPIKey, Do: m.aiFieldCmd(fieldAPIKey)},
		{Zone: ZoneAIFieldTimeout, Do: m.aiFieldCmd(fieldTimeout)},
	}
}

// repoPanelHandlers are the click targets on the Repo context panel.
func (m *Model) repoPanelHandlers() []zones.ClickHandler {
	return []zones.ClickHandler{
		{Zone: ZoneRepoToggleIncludePR, Do: m.repoToggleCmd(&m.repoIncludePR, repoFieldIncludePR)},
		{Zone: ZoneRepoToggleCulture, Do: m.repoToggleCmd(&m.repoCultureSum, repoFieldCultureSum)},
		{Zone: ZoneRepoToggleCtxVs, Do: m.repoToggleCmd(&m.repoCtxVsChange, repoFieldCtxVsChange)},
		{Zone: ZoneRepoToggleExpert, Do: m.repoToggleCmd(&m.repoExpertPanel, repoFieldExpertPanel)},
		{Zone: ZoneRepoToggleParallelSpecs, Do: m.repoToggleCmd(&m.repoParallelSpecs, repoFieldParallelSpecs)},
		{Zone: ZoneRepoToggleParallelPRAgents, Do: m.repoToggleCmd(&m.repoParallelPRAgents, repoFieldParallelPRAgents)},
		{Zone: ZoneRepoToggleParallelExperts, Do: m.repoToggleCmd(&m.repoParallelExperts, repoFieldParallelExperts)},
		{Zone: ZoneRepoFieldRoots, Do: m.repoFieldCmd(repoFieldRoots)},
		{Zone: ZoneRepoFieldMaxBytes, Do: m.repoFieldCmd(repoFieldMaxBytes)},
		{Zone: ZoneRepoFieldTTL, Do: m.repoFieldCmd(repoFieldTTL)},
		{Zone: ZoneRepoFieldPRHistLimit, Do: m.repoFieldCmd(repoFieldPRHistLimit)},
		{Zone: ZoneRepoFieldExpertPRs, Do: m.repoFieldCmd(repoFieldExpertPRs)},
		{Zone: ZoneRepoFieldExpertMaxB, Do: m.repoFieldCmd(repoFieldExpertMaxB)},
		{Zone: ZoneRepoFieldExpertTTL, Do: m.repoFieldCmd(repoFieldExpertTTL)},
	}
}

// themePanelHandlers are the click targets on the Theme panel. A swatch opens
// its modal on press, so the press is delivered to whichever swatch row was
// hit (the SwatchPicker opens on release; zones fire then).
func (m *Model) themePanelHandlers(msg tea.MouseMsg) []zones.ClickHandler {
	h := []zones.ClickHandler{
		{Zone: ZoneThemeReset, Do: func() tea.Cmd { m.theme.resetAll(); return nil }},
	}
	if m.theme != nil {
		for i := range m.theme.swatches {
			h = append(h, zones.ClickHandler{Zone: m.theme.swatches[i].zoneID, Do: func() tea.Cmd { return m.forwardSwatchPress(i, msg) }})
		}
	}
	return h
}

func cancelCmd() tea.Cmd {
	return state.NavigateTarget{Kind: state.NavBack, Cancelled: true}.Cmd()
}

// submitCurrentSave routes Save to the panel-specific submit handler.
func (m *Model) submitCurrentSave() tea.Cmd {
	switch m.panelTab {
	case 1:
		return m.submitRepoSave()
	case 2:
		return m.submitThemeSave()
	default:
		return m.submitSave()
	}
}

// The *Cmd builders below produce the click actions for the repetitive field
// rows so the table above stays one line per zone.
func (m *Model) aiFieldCmd(field int) func() tea.Cmd {
	return func() tea.Cmd { m.focusAIField(field); return textinput.Blink }
}

func (m *Model) repoToggleCmd(b *bool, field int) func() tea.Cmd {
	return func() tea.Cmd { *b = !*b; m.focusRepoField(field); return nil }
}

func (m *Model) repoFieldCmd(field int) func() tea.Cmd {
	return func() tea.Cmd { m.focusRepoField(field); return m.repoBlinkCmd() }
}

func (m *Model) profileActionCmd(action func() tea.Cmd) func() tea.Cmd {
	return func() tea.Cmd { m.focus = fieldProfilePicker; m.blurInputs(); return action() }
}

func (m *Model) openDropdownCmd(kind int, msg tea.Msg) func() tea.Cmd {
	return func() tea.Cmd {
		m.blurInputs()
		m.focus = fieldForDropdown(kind)
		m.syncDropdownFocus()
		return m.forwardToDropdown(kind, msg)
	}
}

// forwardSwatchPress focuses swatch i and delivers the press to it.
func (m *Model) forwardSwatchPress(i int, msg tea.MouseMsg) tea.Cmd {
	m.theme.swatches[i].swatch.SetFocused(false)
	m.theme.focus = i
	m.theme.swatches[i].swatch.SetFocused(true)
	updated, cmd := m.theme.swatches[i].swatch.Update(msg)
	m.theme.swatches[i].swatch = updated
	return cmd
}
