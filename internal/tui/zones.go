package tui

import "fmt"

// zoneTreeFile marks a row in the changed-files tree on the PR detail page.
func zoneTreeFile(i int) string {
	return fmt.Sprintf("zone:tree:file:%d", i)
}

// bubblezone IDs for clickable regions.
const (
	ZoneFilterToggle             = "zone:filter:explicit"
	// ZoneRefreshList is the clickable "refresh" chip on the list view's
	// filter strip. Mirrors the keyboard "R" binding so users who don't
	// read the status bar still have an obvious way to re-fetch the
	// review queue from GitHub.
	ZoneRefreshList = "zone:list:refresh"
	ZoneConfirmYes               = "zone:confirm:yes"
	ZoneConfirmNo                = "zone:confirm:no"
	ZoneErrorDismiss             = "zone:error:dismiss"
	ZoneErrorCopy                = "zone:error:copy"
	ZoneDryDismiss               = "zone:dry:dismiss"
	ZoneStagedPost               = "zone:staged:post"
	ZoneStagedSkip               = "zone:staged:skip"
	ZoneStagedQuit               = "zone:staged:quit"
	ZoneStagedPrev               = "zone:staged:prev"
	ZoneStagedNext               = "zone:staged:next"
	ZoneStagedFinish             = "zone:staged:finish"
	ZoneStagedSummaryYes         = "zone:staged:summary:yes"
	ZoneStagedSummaryNo          = "zone:staged:summary:no"
	ZoneStagedSummaryApproveOnly = "zone:staged:summary:approve-only"
	// ZoneStagedRefresh is the clickable "Refresh PR" button rendered
	// alongside an inline-comment or summary post error so the user can
	// re-fetch the PR + diff and retry without leaving the overlay.
	ZoneStagedRefresh = "zone:staged:refresh"
	ZonePostedOK                 = "zone:posted:ok"
	ZoneRepoContextToggle        = "zone:review:repo-context"
	ZonePaneTree                 = "zone:pane:tree"
	ZonePaneDiff                 = "zone:pane:diff"
	// Pane viewport interiors (below each pane title) — used to map clicks on
	// lipgloss-padded blank rows at the bottom of a viewport to the last row.
	ZonePaneTreeBody      = "zone:pane:tree:body"
	ZonePaneDiffBody      = "zone:pane:diff:body"
	ZoneReopenApproval    = "zone:detail:reopen"
	ZoneDescriptionToggle = "zone:detail:description"
	// ZoneBuildRepoAgents is the clickable chip in the PR detail mini-header
	// that opens the repo-agents tab focused on the current PR's repo and
	// immediately kicks off "Regenerate all" for that repo.
	ZoneBuildRepoAgents = "zone:detail:build-repo-agents"
	// ZoneBuildLangAgents is the clickable chip in the PR detail mini-header
	// that opens the lang-agents tab scoped to the PR's touched languages,
	// the twin of ZoneBuildRepoAgents. Sits beside it so the reviewer can
	// see both "repo context" and "language conventions" health at a glance.
	ZoneBuildLangAgents = "zone:detail:build-lang-agents"
	// ZoneOpenInBrowser is the clickable chip in the PR detail mini-header
	// that opens the current PR's GitHub URL in the user's default browser.
	ZoneOpenInBrowser = "zone:detail:open-in-browser"
)
