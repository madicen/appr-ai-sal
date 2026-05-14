// Package zones owns every bubblezone marker ID used by the root TUI.
//
// Centralising the IDs prevents drift between the place an interactive
// region is registered (zone.Mark) and the place its bounds are queried
// (zone.Get). Sub-tabs (settings, repoagents, langagents) keep their own
// zone packages — only the cross-tab IDs that root, list, detail, and
// review overlays need live here.
package zones

import "fmt"

// TreeFile marks a row in the changed-files tree on the PR detail page.
// Indexed by row number so each row gets a unique zone.
func TreeFile(i int) string {
	return fmt.Sprintf("zone:tree:file:%d", i)
}

// bubblezone IDs for clickable regions.
const (
	FilterToggle = "zone:filter:explicit"
	// RefreshList is the clickable "refresh" chip on the list view's
	// filter strip. Mirrors the keyboard "R" binding so users who don't
	// read the status bar still have an obvious way to re-fetch the
	// review queue from GitHub.
	RefreshList                  = "zone:list:refresh"
	ConfirmYes                   = "zone:confirm:yes"
	ConfirmNo                    = "zone:confirm:no"
	ErrorDismiss                 = "zone:error:dismiss"
	ErrorCopy                    = "zone:error:copy"
	DryDismiss                   = "zone:dry:dismiss"
	StagedPost                   = "zone:staged:post"
	StagedSkip                   = "zone:staged:skip"
	StagedQuit                   = "zone:staged:quit"
	StagedPrev                   = "zone:staged:prev"
	StagedNext                   = "zone:staged:next"
	StagedFinish                 = "zone:staged:finish"
	StagedSummaryYes             = "zone:staged:summary:yes"
	StagedSummaryNo              = "zone:staged:summary:no"
	StagedSummaryApproveOnly     = "zone:staged:summary:approve-only"
	// StagedRefresh is the clickable "Refresh PR" button rendered
	// alongside an inline-comment or summary post error so the user can
	// re-fetch the PR + diff and retry without leaving the overlay.
	StagedRefresh        = "zone:staged:refresh"
	PostedOK             = "zone:posted:ok"
	RepoContextToggle    = "zone:review:repo-context"
	PaneTree             = "zone:pane:tree"
	PaneDiff             = "zone:pane:diff"
	// PaneControls is the right-hand "Review controls" pane on the PR
	// detail view: strictness, profile picker, agent state, toggles,
	// Start Review button.
	PaneControls = "zone:pane:controls"
	// Pane viewport interiors (below each pane title) — used to map
	// clicks on lipgloss-padded blank rows at the bottom of a viewport
	// to the last row.
	PaneTreeBody     = "zone:pane:tree:body"
	PaneDiffBody     = "zone:pane:diff:body"
	PaneControlsBody = "zone:pane:controls:body"
	ReopenApproval   = "zone:detail:reopen"
	DescriptionToggle = "zone:detail:description"
	// BuildRepoAgents is the clickable chip in the PR detail mini-header
	// that opens the repo-agents tab focused on the current PR's repo
	// and immediately kicks off "Regenerate all" for that repo.
	BuildRepoAgents = "zone:detail:build-repo-agents"
	// BuildLangAgents is the clickable chip in the PR detail
	// mini-header that opens the lang-agents tab scoped to the PR's
	// touched languages, the twin of BuildRepoAgents. Sits beside it so
	// the reviewer can see both "repo context" and "language
	// conventions" health at a glance.
	BuildLangAgents = "zone:detail:build-lang-agents"
	// OpenInBrowser is the clickable chip in the PR detail mini-header
	// that opens the current PR's GitHub URL in the user's default
	// browser.
	OpenInBrowser = "zone:detail:open-in-browser"

	// Controls panel zones — clickable rows / buttons inside the
	// right-hand "Review controls" pane.
	ControlsStrictCriticalOnly  = "zone:controls:strict:critical_only"
	ControlsStrictLenient       = "zone:controls:strict:lenient"
	ControlsStrictBalanced      = "zone:controls:strict:balanced"
	ControlsStrictStrict        = "zone:controls:strict:strict"
	ControlsProfilePrev         = "zone:controls:profile:prev"
	ControlsProfileNext         = "zone:controls:profile:next"
	ControlsProfileEdit         = "zone:controls:profile:edit"
	ControlsRepoAgents          = "zone:controls:agents:repo"
	ControlsTechAgents          = "zone:controls:agents:tech"
	ControlsLangAgents          = "zone:controls:agents:lang"
	ControlsToggleParallel      = "zone:controls:toggle:parallel"
	ControlsToggleDryRun        = "zone:controls:toggle:dry-run"
	ControlsTogglePeruse        = "zone:controls:toggle:peruse"
	ControlsStartReview         = "zone:controls:start"
	ControlsStartReviewPeruse   = "zone:controls:start-peruse"
)
