package state

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// NavigateKind identifies a top-level navigation event emitted by a tab.
//
// Today every cross-tab transition that flows through NavigateMsg is a
// "close this tab and go back" gesture, so NavBack is the only kind. The
// forward transitions (open detail, open settings, …) are driven by the
// root's direct opener methods (openSettings, openRepoAgents, …) rather
// than through NavigateMsg, so no other kinds are wired up. Additional
// kinds should be added here only when a producer AND the root consumer
// land together — no half-wired enum values.
type NavigateKind int

const (
	// NavBack closes the current tab and returns to the previous mode.
	NavBack NavigateKind = iota
)

// NavigateTarget bundles every parameter NavBack needs. All fields are
// optional; root's handleNavigate ignores anything the transition doesn't
// use.
type NavigateTarget struct {
	Kind NavigateKind

	// Cfg is the saved AI config returned by the settings tab on save.
	Cfg *aiconfig.Config

	// Err, when non-nil, indicates the navigation should surface an
	// error overlay instead of changing modes.
	Err error

	// Cancelled is set by tabs that exit via the cancel/escape path so
	// root can distinguish "user backed out" from "user committed".
	Cancelled bool
}

// NavigateMsg is the canonical Bubble Tea message type the root TUI uses
// to drive every cross-tab transition.
type NavigateMsg struct {
	Target NavigateTarget
}

// Cmd returns a tea.Cmd that emits a NavigateMsg wrapping t. Sub-models
// build a NavigateTarget and call .Cmd() in their Update return slot
// instead of constructing a closure inline.
func (t NavigateTarget) Cmd() tea.Cmd {
	return func() tea.Msg { return NavigateMsg{Target: t} }
}
