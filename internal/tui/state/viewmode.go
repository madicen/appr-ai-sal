// Package state holds shared types that cross every TUI tab boundary:
// the active view mode and the canonical NavigateMsg envelope every
// cross-screen transition flows through.
//
// Nothing in this package depends on a particular tab — it's a leaf so
// any tab can import it without risking a cycle back into root.
package state

// ViewMode identifies which top-level screen the root TUI is currently
// painting. It is the single source of truth for the active screen: the
// root model stores it directly (see model.mode, a type alias for this)
// and keys its tab registry (map[ViewMode]Tab) off it.
type ViewMode int

const (
	// ModeList is the default screen: the PR queue with filter chips.
	ModeList ViewMode = iota
	// ModeDetail is the two-pane PR view (file tree + diff).
	ModeDetail
	// ModeSettings is the AI/repo/theme settings tab.
	ModeSettings
	// ModeRepoAgents is the per-repo expert agent management tab.
	ModeRepoAgents
	// ModeLangAgents is the per-language convention brief management tab.
	ModeLangAgents
)
