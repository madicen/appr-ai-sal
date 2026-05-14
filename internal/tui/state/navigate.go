package state

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// SettingsStart mirrors settings.StartSection without importing the
// settings package (which would create a cycle: settings imports state).
// The integer values must stay in sync with settings.StartSection so root
// can convert in either direction.
type SettingsStart int

const (
	// SettingsStartReview opens settings on the Review tab.
	SettingsStartReview SettingsStart = iota
	// SettingsStartAI opens settings on the AI provider tab.
	SettingsStartAI
	// SettingsStartRepoContext opens settings on the Repo context tab.
	SettingsStartRepoContext
	// SettingsStartTheme opens settings on the Theme tab.
	SettingsStartTheme
)

// NavigateKind identifies a top-level navigation event. Every cross-tab
// transition (open detail, back to list, save settings, etc.) flows
// through a single NavigateMsg with one of these kinds — replacing the
// per-tab DoneMsg variants that used to litter the package.
type NavigateKind int

const (
	// NavBack closes the current tab and returns to the previous mode.
	NavBack NavigateKind = iota
	// NavToList navigates to the PR queue.
	NavToList
	// NavToDetail opens the two-pane PR detail screen for Target.PR.
	NavToDetail
	// NavToURLInput shows the "open by URL" prompt.
	NavToURLInput
	// NavToSettings opens the settings tab; SettingsStart selects which
	// sub-tab is focused.
	NavToSettings
	// NavToRepoAgents opens the per-repo agent management tab. When
	// OwnerRepo is set, the tab focuses that repo immediately.
	NavToRepoAgents
	// NavToLangAgents opens the per-language brief management tab.
	NavToLangAgents
	// NavParseURL is emitted by the URL input tab; root parses Target.URL
	// and loads the resulting PR.
	NavParseURL
	// NavQuit shuts down the TUI cleanly.
	NavQuit
)

// NavigateTarget bundles every parameter any NavigateKind needs. Most
// fields are optional and only consulted by the relevant kind; root's
// handleNavigate switch ignores anything it doesn't need.
type NavigateTarget struct {
	Kind NavigateKind

	// PR is the PR to open in NavToDetail.
	PR *gh.PR

	// URL is the raw user input parsed by NavParseURL.
	URL string

	// SettingsStart selects which settings sub-tab is focused after
	// NavToSettings.
	SettingsStart SettingsStart

	// OwnerRepo is the "owner/repo" slug for NavToRepoAgents when opened
	// from a PR (so the tab can pre-focus that repo).
	OwnerRepo string
	// PRNumber is the PR number for NavToRepoAgents (used to scope the
	// regenerate-all confirmation prompt).
	PRNumber int

	// Cfg is the saved AI config returned by the settings tab on save.
	Cfg *aiconfig.Config

	// Err, when non-nil, indicates the navigation should surface an
	// error overlay instead of changing modes (replaces DoneMsg.Err).
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
// instead of constructing a closure inline — same shape as jj-tui.
func (t NavigateTarget) Cmd() tea.Cmd {
	return func() tea.Msg { return NavigateMsg{Target: t} }
}
