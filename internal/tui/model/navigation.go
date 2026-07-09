package model

import (
	tea "github.com/charmbracelet/bubbletea"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

// handleNavigate is the single dispatch point for cross-tab transitions.
// Tabs emit state.NavigateMsg{Target: ...}; today every such transition is
// a NavBack ("close this tab") gesture. Root drops the active tab from the
// registry, restores tabPrevMode, and runs the per-tab teardown (config
// adoption for settings, freshness-cache invalidation for the agent tabs).
func (m *Model) handleNavigate(t state.NavigateTarget) (tea.Model, tea.Cmd) {
	if t.Kind != state.NavBack {
		return m, nil
	}

	closing := m.mode
	delete(m.tabs, closing)
	m.mode = m.tabPrevMode

	switch closing {
	case modeSettings:
		if t.Cancelled {
			return m, nil
		}
		if t.Err != nil {
			return m, m.pushErrorOverlay(t.Err)
		}
		if t.Cfg != nil {
			m.opts.AIConfig = t.Cfg.Clone()
		}
		m.relayout()
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, nil

	case modeRepoAgents:
		// Any specialist for any repo could have been regenerated, added,
		// or deleted while the tab was open; the safest invalidation is
		// to drop the whole cache so the chip / status hint re-read on
		// the next render.
		m.invalidateRepoAgentsFreshness()
		m.relayout()
		if t.Err != nil {
			return m, m.pushErrorOverlay(t.Err)
		}
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, nil

	case modeLangAgents:
		m.invalidateLangAgentsFreshness()
		m.relayout()
		if t.Err != nil {
			return m, m.pushErrorOverlay(t.Err)
		}
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, nil
	}
	return m, nil
}

// pushErrorOverlay pushes the scrollable error modal for err and follows
// it with a synthetic WindowSizeMsg so the freshly-pushed layer sizes to
// the current terminal. It replaces the copy-pasted six-line push snippet
// that used to live inline at every error site.
func (m *Model) pushErrorOverlay(err error) tea.Cmd {
	em := overlays.NewErrorOverlay(err.Error(), max(40, m.width-6), max(8, m.height-8))
	return tea.Batch(
		m.overlayStack.Push(em, overlay.DefaultOverlayConfig()),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	)
}
