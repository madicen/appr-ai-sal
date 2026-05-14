// Package overlays owns the modal dialogs the root TUI stacks on top of
// the active screen: the bulk-post confirm, the error overlay, the dry-run
// preview, and the posted-success overlay.
//
// The "stack" is the bubble-overlay layer wired up by root; this package
// just owns the modal types and the dismiss messages they emit. Each
// modal returns a typed message (BulkPostAnswerMsg, ErrorOverlayDismissMsg,
// etc.) so root can drive the lifecycle without inspecting the model.
package overlays

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

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
