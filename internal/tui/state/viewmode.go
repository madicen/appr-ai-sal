// Package state holds shared types that cross every TUI tab boundary:
// the active view mode, the AppState handed to sub-models on construction,
// and the canonical NavigateMsg envelope every cross-screen transition
// flows through.
//
// Nothing in this package depends on a particular tab — it's a leaf so
// any tab can import it without risking a cycle back into root.
package state

// ViewMode identifies which top-level screen the root TUI is currently
// painting. Renamed from the old `mode` enum in tui.go for parity with
// jj-tui's nomenclature and so callers can refer to it across packages
// without an unexported import.
type ViewMode int

const (
	// ModeList is the default screen: the PR queue with filter chips.
	ModeList ViewMode = iota
	// ModeDetail is the two-pane PR view (file tree + diff).
	ModeDetail
	// ModeURLInput is the prompt for "open this PR by URL or owner/repo#N".
	ModeURLInput
	// ModeSettings is the AI/repo/theme settings tab.
	ModeSettings
	// ModeRepoAgents is the per-repo expert agent management tab.
	ModeRepoAgents
	// ModeLangAgents is the per-language convention brief management tab.
	ModeLangAgents
)
