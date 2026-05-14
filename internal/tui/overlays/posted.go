package overlays

import (
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// PostedOverlayDismissMsg is emitted when the user dismisses the
// "Posted." success modal.
type PostedOverlayDismissMsg struct{}

// PostedOverlay is the brief "Posted." confirmation shown after a review
// is successfully published to GitHub.
type PostedOverlay struct{}

func (m PostedOverlay) Init() tea.Cmd { return nil }

func (m PostedOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "enter", "q", " ":
			return m, func() tea.Msg { return PostedOverlayDismissMsg{} }
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(zones.PostedOK); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return PostedOverlayDismissMsg{} }
			}
		}
	}
	return m, nil
}

func (m PostedOverlay) View() string {
	return ModalFrameSized(56).Render(styles.OkStyle.Render("Posted.") + "\n\n" +
		zone.Mark(zones.PostedOK, styles.DimStyle.Render(" OK ")))
}
