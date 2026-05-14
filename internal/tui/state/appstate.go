package state

import (
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// AppState bundles the global, mostly-read-only context every tab needs
// to render and decide actions. The root model owns the canonical copy;
// sub-models receive a snapshot at construction time and re-receive it on
// resize so they don't have to import root.
//
// Mutable channels (PRRefreshCh) are pointers/channels that root keeps
// authoritative — tabs only read from them.
type AppState struct {
	// DryRun is true when the user passed --dry-run; tabs use it to
	// suppress destructive actions.
	DryRun bool

	// AIConfig is the active AI provider configuration. Pointer so a
	// settings-tab save propagates without re-issuing the AppState.
	AIConfig *aiconfig.Config

	// DebugMouse mirrors Options.DebugMouse so detail-tab handlers can
	// log click coordinates without re-reading the env var.
	DebugMouse bool

	// MouseYAdjust mirrors Options.MouseYAdjust; detail-tab mouse
	// handlers add it to MouseMsg.Y to compensate for terminal-specific
	// row offsets.
	MouseYAdjust int

	// PRRefreshCh is the channel root uses to fan out
	// "this PR's data was refreshed" events to subscribers (mainly the
	// list tab so it can update freshness chips).
	PRRefreshCh <-chan *gh.PR
}
