package model

import "github.com/madicen/appr-ai-sal/internal/aiconfig"

// Options configures the root TUI model.
type Options struct {
	DryRun   bool
	AIConfig *aiconfig.Config
	// DebugMouse logs each left-click in PR detail to stderr with bubblezone
	// bounds (set via APPR_AI_SAL_DEBUG_MOUSE=1). For diagnosing hit-box drift.
	DebugMouse bool
	// MouseYAdjust is added to tea.MouseMsg.Y for PR detail only (see
	// APPR_AI_SAL_MOUSE_Y_ADJUST). Use when the terminal reports cell Y offset
	// from bubblezone line indices (e.g. integrated terminals).
	MouseYAdjust int
	// Demo enables a self-contained "VHS recording" mode: the data layer
	// returns canned PRs / diffs / review-progress streams instead of
	// shelling out to gh or invoking the AI provider, and the agent tabs
	// run a fake Complete that mimics inference latency. Wired by the
	// --demo CLI flag in cmd/appr-ai-sal/main.go.
	Demo bool
}
