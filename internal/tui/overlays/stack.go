// Package overlays owns the modal dialogs the root TUI stacks on top of
// the active screen: the bulk-post confirm, the error overlay, the dry-run
// preview, and the posted-success overlay.
//
// The "stack" is the bubble-overlay layer wired up by root; this package
// just owns the modal types and the single DismissMsg they emit. Every
// modal returns overlays.DismissMsg when the user dismisses it; the root
// pops the top overlay and dispatches on the popped overlay's concrete
// type (and DismissMsg.Result) so one message type drives the whole modal
// lifecycle instead of a bespoke per-modal message each.
package overlays

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// DismissMsg is the single message every modal overlay emits when the
// user dismisses it (esc/enter/q/space or clicking a dismiss zone).
//
// Result carries the modal's per-dismiss payload when it has one — e.g.
// BulkConfirmOverlay sets Result to a BulkPostAnswer. Modals that only
// need acknowledgement (error, dry-run, posted) leave it nil. The root
// distinguishes which modal was dismissed by the concrete type of the
// popped overlay, not by the message type, so adding a new modal never
// requires a new dismiss message.
type DismissMsg struct {
	Result any
}

// BulkPostAnswer is the DismissMsg.Result payload emitted by
// BulkConfirmOverlay: whether the user confirmed the bulk post.
type BulkPostAnswer struct {
	Confirm bool
}

// dismiss is the shared helper every modal uses to emit DismissMsg.
func dismiss(result any) tea.Cmd {
	return func() tea.Msg { return DismissMsg{Result: result} }
}

// ModalChrome is the shared border + padding for every modal so they all
// frame their content the same way. Exported because the review overlay
// also wraps itself in this chrome when not opened as a modal stack
// member.
var ModalChrome = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("#5D2D91")).
	Padding(1, 2)

// ModalFrameSized returns a copy of ModalChrome sized to outerWidth (with
// a sane lower/upper bound) so lipgloss layout matches bubblezone.Scan
// (both walk the same string).
func ModalFrameSized(outerWidth int) lipgloss.Style {
	return ModalChrome.Copy().Width(util.Clamp(outerWidth, 40, 120))
}

// bulkYesKeys / bulkNoKeys are the keyboard answers for the bulk-post
// confirm overlay. Exposed at package level so the modal Update can match
// against the same bindings every frame.
var (
	bulkYesKeys = key.NewBinding(key.WithKeys("y", "Y", "enter"))
	bulkNoKeys  = key.NewBinding(key.WithKeys("n", "N", "esc", "q"))
)
