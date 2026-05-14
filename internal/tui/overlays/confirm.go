package overlays

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// BulkPostAnswerMsg is emitted by BulkConfirmOverlay when the user
// answers yes/no to "post entire review now?".
type BulkPostAnswerMsg struct {
	Confirm bool
}

// BulkConfirmOverlay asks the user to confirm bulk-posting an in-progress
// review's findings + summary. ref is the human-readable PR ref shown in
// the prompt (e.g. "owner/repo#42").
type BulkConfirmOverlay struct {
	ref string
}

// NewBulkConfirmOverlay constructs the confirm modal for the given PR
// ref string.
func NewBulkConfirmOverlay(ref string) BulkConfirmOverlay {
	return BulkConfirmOverlay{ref: ref}
}

func (m BulkConfirmOverlay) Init() tea.Cmd { return nil }

func (m BulkConfirmOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, bulkYesKeys):
			return m, func() tea.Msg { return BulkPostAnswerMsg{Confirm: true} }
		case key.Matches(msg, bulkNoKeys):
			return m, func() tea.Msg { return BulkPostAnswerMsg{Confirm: false} }
		case msg.String() == "ctrl+c":
			return m, func() tea.Msg { return BulkPostAnswerMsg{Confirm: false} }
		}
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if z := zone.Get(zones.ConfirmYes); z != nil && z.InBounds(msg) {
			return m, func() tea.Msg { return BulkPostAnswerMsg{Confirm: true} }
		}
		if z := zone.Get(zones.ConfirmNo); z != nil && z.InBounds(msg) {
			return m, func() tea.Msg { return BulkPostAnswerMsg{Confirm: false} }
		}
	}
	return m, nil
}

func (m BulkConfirmOverlay) View() string {
	body := "Post entire review (all comments + summary)\nto " + m.ref + "?\n\n" +
		zone.Mark(zones.ConfirmYes, styles.OkStyle.Render(" Yes ")) + "  " +
		zone.Mark(zones.ConfirmNo, styles.ErrStyle.Render(" No "))
	return ModalFrameSized(56).Render(body)
}
