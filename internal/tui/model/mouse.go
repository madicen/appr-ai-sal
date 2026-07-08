package model

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// handleStatusBarMouse dispatches a left-click on a bottom status-bar hint
// to the same action its key triggers, so a mouse-only user can drive the
// whole app from the status bar. It runs at the root before mode/tab
// routing so the universally-present quit segment works in every mode —
// including the settings / repo-agents / lang-agents tabs that otherwise
// own the event stream. handled=false means the click missed every status
// zone and the caller should fall through to normal routing.
//
// Only the currently-rendered mode marks its status zones (see
// statusSegs), so a zone for a different mode can never match here.
func (m *Model) handleStatusBarMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil, false
	}
	// Quit is the only segment present in every mode.
	if zoneInBounds(zones.StatusQuit, msg) {
		util.FlushMouse()
		return m, tea.Quit, true
	}
	// Help / palette are the global hints present on both the list and
	// detail status rows; handle them here before the per-mode switch.
	if zoneInBounds(zones.StatusHelp, msg) {
		return m, m.openHelpOverlay(), true
	}
	if zoneInBounds(zones.StatusPalette, msg) {
		return m, m.openCommandPalette(), true
	}
	switch m.mode {
	case modeList:
		switch {
		case zoneInBounds(zones.StatusSearch, msg):
			return m, m.focusSearchInput(), true
		case zoneInBounds(zones.StatusURL, msg):
			return m, m.focusURLInput(), true
		case zoneInBounds(zones.StatusFilter, msg):
			return m, m.cycleFilterCmd(), true
		case zoneInBounds(zones.StatusOpenBrowser, msg):
			if it, ok := m.list.SelectedItem().(prItem); ok {
				if u := strings.TrimSpace(it.pr.URL); u != "" {
					return m, util.OpenInBrowserCmd(u), true
				}
			}
			return m, nil, true
		case zoneInBounds(zones.StatusSettingsAI, msg):
			return m, m.openSettings(settings.StartAI), true
		case zoneInBounds(zones.StatusRepoCtx, msg):
			return m, m.openSettings(settings.StartRepoContext), true
		case zoneInBounds(zones.StatusRepoAgents, msg):
			return m, m.openRepoAgents("", false), true
		case zoneInBounds(zones.StatusLangAgents, msg):
			return m, m.openLangAgents(), true
		case zoneInBounds(zones.StatusBuildAgents, msg):
			if it, ok := m.list.SelectedItem().(prItem); ok {
				return m, m.openRepoAgents(it.pr.Owner+"/"+it.pr.Repo, true), true
			}
			return m, m.openRepoAgents("", false), true
		case zoneInBounds(zones.StatusRefresh, msg):
			return m, m.refreshPRListCmd(), true
		}
	case modeDetail:
		switch {
		case zoneInBounds(zones.StatusCyclePane, msg):
			m.cyclePane(+1)
			m.refreshDetailViews()
			return m, nil, true
		case zoneInBounds(zones.StatusReview, msg):
			mm, cmd := m.startReviewOverlay()
			return mm, cmd, true
		case zoneInBounds(zones.StatusToggleControls, msg):
			m.detailToggleControls()
			return m, nil, true
		case zoneInBounds(zones.StatusReopenApproval, msg):
			mm, cmd := m.reopenApprovalIfPossible()
			return mm, cmd, true
		case zoneInBounds(zones.StatusOpenBrowser, msg):
			return m, m.detailOpenBrowserCmd(), true
		case zoneInBounds(zones.StatusDescription, msg):
			return m, m.detailToggleDescription(), true
		case zoneInBounds(zones.StatusDiffOnly, msg):
			m.detailToggleDiffOnly()
			return m, nil, true
		case zoneInBounds(zones.StatusBulk, msg):
			return m, m.detailBulkConfirmCmd(), true
		case zoneInBounds(zones.StatusBack, msg):
			m.detailBackToList()
			return m, nil, true
		}
	}
	return m, nil, false
}

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
		// Wheel scroll is only meaningful for the list pane and only
		// when no inline input is focused — otherwise the search /
		// URL field would silently lose its caret position whenever
		// the user spun the wheel over the list.
		if wheel && m.listFocus == focusList && msg.Y >= listTop && msg.Y < listTop+m.list.Height() {
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
			for _, chip := range filterChips {
				if zoneInBounds(chip.zone, msg) {
					m.resetListClickTracking()
					return m, m.setFilterCmd(chip.mode)
				}
			}
			if zoneInBounds(zones.RefreshList, msg) {
				m.resetListClickTracking()
				return m, m.refreshPRListCmd()
			}
			if zoneInBounds(zones.SearchField, msg) {
				m.resetListClickTracking()
				return m, m.focusSearchInput()
			}
			if zoneInBounds(zones.URLField, msg) {
				m.resetListClickTracking()
				return m, m.focusURLInput()
			}
			if gi, ok := m.listGlobalIndexAtClick(msg); ok {
				// A click in the list body also drops keyboard focus
				// back to the list so subsequent ↑/↓/enter actually
				// hit the bubbles list and not a still-focused input.
				m.blurPanelInputs()
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
