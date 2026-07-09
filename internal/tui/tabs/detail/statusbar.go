package detail

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// HandleStatusBarMouse handles detail-mode status bar hint clicks.
func (m *Model) HandleStatusBarMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	switch {
	case zoneInBounds(zones.StatusCyclePane, msg):
		m.cyclePane(+1)
		m.refreshDetailViews()
		return nil, true
	case zoneInBounds(zones.StatusReview, msg):
		return m.host.StartReviewOverlay(), true
	case zoneInBounds(zones.StatusToggleControls, msg):
		m.detailToggleControls()
		return nil, true
	case zoneInBounds(zones.StatusReopenApproval, msg):
		return m.host.ReopenApproval(), true
	case zoneInBounds(zones.StatusOpenBrowser, msg):
		return m.host.OpenBrowser(), true
	case zoneInBounds(zones.StatusDescription, msg):
		return m.detailToggleDescription(), true
	case zoneInBounds(zones.StatusDiffOnly, msg):
		m.detailToggleDiffOnly()
		return nil, true
	case zoneInBounds(zones.StatusBulk, msg):
		return m.host.BulkConfirm(), true
	case zoneInBounds(zones.StatusBack, msg):
		m.detailBackToList()
		return nil, true
	}
	return nil, false
}
