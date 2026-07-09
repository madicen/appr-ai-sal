package detail

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

func (m *Model) handleMouse(msg tea.MouseMsg, wheel bool) (tea.Model, tea.Cmd) {
	if m.host.MouseYAdjust() != 0 {
		msg.Y += m.host.MouseYAdjust()
	}
	if wheel {
		// Route wheel by which pane bounds contain the cursor.
		switch {
		case zoneInBounds(zones.PaneTree, msg):
			util.WheelScrollViewport(&m.treeView, msg)
		case !m.controlsHidden && zoneInBounds(zones.PaneControls, msg):
			util.WheelScrollViewport(&m.controlsView, msg)
		case zoneInBounds(zones.PaneDiff, msg) || m.mouseYInChromeBody(msg):
			util.WheelScrollViewport(&m.diffView, msg)
		}
		return m, nil
	}

	// Drag state machine: once a seam drag is armed, every motion
	// updates the pane width and a release ends the drag. We consume
	// the event so it never falls through to the normal click logic
	// (which would otherwise interpret a drag-release on a tree row as
	// a row click).
	if m.paneDragActive() {
		switch msg.Action {
		case tea.MouseActionMotion:
			if m.updatePaneDrag(msg) {
				m.refreshDetailViews()
			}
			return m, nil
		case tea.MouseActionRelease:
			m.endPaneDrag()
			m.refreshDetailViews()
			return m, nil
		}
		// Other events while a drag is pending (e.g. a stray press
		// without a matching release) — drop them so we don't double-
		// arm or fire a click underneath the divider.
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// Seam press takes precedence over every other press hit-test:
	// boundary columns are 1-cell wide, so a click that lands on the
	// border between (say) the tree pane and the diff pane should arm
	// a drag rather than fall through to the tree row beneath it.
	if target := m.seamAtPoint(msg.X, msg.Y); target != dividerNone {
		m.startPaneDrag(msg, target)
		m.refreshDetailViews()
		return m, nil
	}

	m.debugLogDetailMouse(msg)

	// Reopen approval / description toggle / finish (they take precedence over
	// pane focus changes since they are header chips).
	if z := zone.Get(zones.ReopenApproval); z != nil && z.InBounds(msg) {
		return m, m.host.ReopenApproval()
	}
	if z := zone.Get(zones.DescriptionToggle); z != nil && z.InBounds(msg) {
		// Toggle Description ↔ Diff so the chip stays a one-click affordance
		// — clicking it again from the Description view jumps back to the
		// last diff selection.
		if m.centerView == centerDescription {
			m.centerView = centerDiff
		} else {
			m.centerView = centerDescription
		}
		m.focusedPane = paneDiff
		m.refreshDetailViews()
		return m, m.ensureCenterDataLoaded()
	}
	if z := zone.Get(zones.OverviewDescription); z != nil && z.InBounds(msg) {
		m.centerView = centerDescription
		m.focusedPane = paneTree
		m.refreshDetailViews()
		return m, m.ensureCenterDataLoaded()
	}
	if z := zone.Get(zones.OverviewChecks); z != nil && z.InBounds(msg) {
		m.centerView = centerChecks
		m.focusedPane = paneTree
		m.refreshDetailViews()
		return m, m.ensureCenterDataLoaded()
	}
	if z := zone.Get(zones.OverviewDiscussion); z != nil && z.InBounds(msg) {
		m.centerView = centerDiscussion
		m.focusedPane = paneTree
		m.refreshDetailViews()
		return m, m.ensureCenterDataLoaded()
	}
	if z := zone.Get(zones.OpenInBrowser); z != nil && z.InBounds(msg) {
		if m.currentPR() != nil {
			if u := strings.TrimSpace(m.currentPR().URL); u != "" {
				return m, util.OpenInBrowserCmd(u)
			}
		}
		return m, nil
	}

	// Controls panel buttons (highest precedence inside the controls
	// pane so a click on a row doesn't fall through to a pane-focus
	// change without firing the action).
	if !m.controlsHidden {
		if cmd, handled := m.controlsHandleClick(msg); handled {
			return m, cmd
		}
	}

	// Tree row clicks (zone per row, then viewport body for padded filler rows).
	// File rows update selection + reset diff scroll; folder rows toggle
	// collapsed state for that subtree. A tree-row click also flips the
	// centre pane back to the diff view if the user was on an overview
	// pane (Description / Checks / Discussion) — clicking a file is the
	// "go look at this file's diff" intent.
	if hit, ok := m.treeRowFromMouse(msg); ok {
		m.focusedPane = paneTree
		m.treeIdx = hit.viewLine
		m.centerView = centerDiff
		if hit.isFolder {
			m.toggleFolderCollapse(m.treeViewRows[hit.viewLine].fullPath)
			return m, nil
		}
		fi := m.treeViewRows[hit.viewLine].fileIndex
		if fi >= 0 && fi < len(m.treeRows) {
			m.selectedFilePath = m.treeRows[fi].Path
			m.diffView.SetYOffset(0)
		}
		m.scrollToSelectedFile = true
		m.refreshDetailViews()
		return m, nil
	}
	// Pane focus on click (for keyboard ergonomics).
	switch {
	case zoneInBounds(zones.PaneTree, msg):
		m.focusedPane = paneTree
		m.refreshDetailViews()
	case !m.controlsHidden && zoneInBounds(zones.PaneControls, msg):
		m.focusedPane = paneControls
		m.refreshDetailViews()
	case zoneInBounds(zones.PaneDiff, msg):
		m.focusedPane = paneDiff
		m.refreshDetailViews()
	}
	return m, nil
}

// controlsHandleClick fires the action for any "Review controls" zone the
// click falls inside. Returns (cmd, true) when handled. Strictness rows
// and toggles are local mutations (no command); profile cycling is also
// local; agent build buttons + Start Review return tea.Cmds.
func (m *Model) controlsHandleClick(msg tea.MouseMsg) (tea.Cmd, bool) {
	switch {
	case zoneInBounds(zones.ControlsStrictCriticalOnly, msg):
		m.setStrictness(aiconfig.ReviewCriticalOnly)
		return nil, true
	case zoneInBounds(zones.ControlsStrictLenient, msg):
		m.setStrictness(aiconfig.ReviewLenient)
		return nil, true
	case zoneInBounds(zones.ControlsStrictBalanced, msg):
		m.setStrictness(aiconfig.ReviewBalanced)
		return nil, true
	case zoneInBounds(zones.ControlsStrictStrict, msg):
		m.setStrictness(aiconfig.ReviewStrict)
		return nil, true
	case zoneInBounds(zones.ControlsProfileDD, msg):
		// Open the profile dropdown panel; the dropdown trusts the zone
		// hit, so forwarding the press opens it regardless of coordinates.
		return m.host.ForwardControlsProfileDropdown(msg), true
	case zoneInBounds(zones.ControlsProfileEdit, msg):
		return m.host.OpenSettings(settings.StartAI), true
	case zoneInBounds(zones.ControlsRepoAgents, msg):
		// Clicking the row is a navigation gesture — open the tab
		// focused on the current PR's repo. Regeneration is reserved
		// for the explicit "build" path (ctrl+b), so click-to-view
		// matches click-to-view-anything-else in the controls pane and
		// doesn't surprise the user with an expensive LLM run.
		return m.host.OpenRepoAgentsForCurrentPR(false), true
	case zoneInBounds(zones.ControlsTechAgents, msg):
		// Same as above: tech experts open in navigate-only mode.
		// ctrl+t is also navigate; the repo-agents tab's own UI is the
		// regen entry point once the user is there.
		return m.host.OpenRepoAgentsForCurrentPR(false), true
	case zoneInBounds(zones.ControlsLangAgents, msg):
		return m.host.OpenLangAgents(), true
	case zoneInBounds(zones.ControlsToggleParallel, msg):
		// Parallel specialists is a repoconfig knob (persists across
		// runs), but flipping it inline matches the muscle memory the
		// other Run-options toggles set: click to flip, see the change
		// immediately, run with it. We load → toggle → save → refresh.
		// If an env var (APPR_AI_SAL_PARALLEL_SPECIALISTS) is overriding
		// the disk value at runtime, the visual will keep showing the
		// env-forced state — that's consistent with the env taking
		// precedence everywhere else.
		if err := m.toggleParallelSpecialists(); err != nil {
			// Fall back to opening settings on save failure so the user
			// still has a way to flip the bit (and can see the error
			// surface from the settings save flow).
			return m.host.OpenSettings(settings.StartRepoContext), true
		}
		return nil, true
	case zoneInBounds(zones.ControlsToggleParallelPRAgents, msg):
		// Same repoconfig-knob flow as Parallel specialists above: load →
		// toggle → save → refresh, falling back to the settings tab on a
		// save failure so the user still has a path to flip the bit.
		if err := m.toggleParallelPRAgents(); err != nil {
			return m.host.OpenSettings(settings.StartRepoContext), true
		}
		return nil, true
	case zoneInBounds(zones.ControlsToggleDryRun, msg):
		m.host.SetDryRun(!m.host.DryRun())
		m.refreshDetailViews()
		return nil, true
	case zoneInBounds(zones.ControlsToggleStartMinimized, msg):
		m.startReviewMinimized = !m.startReviewMinimized
		m.refreshDetailViews()
		return nil, true
	case zoneInBounds(zones.ControlsStartReview, msg):
		return m.host.StartReviewOverlay(), true
	}
	return nil, false
}

func (m *Model) setStrictness(level aiconfig.ReviewStrictness) {
	m.host.SetStrictness(level)
	m.refreshDetailViews()
}

// toggleParallelSpecialists flips repoconfig.ParallelSpecialists on disk
// (the same value the Settings → Repo context tab edits) and refreshes the
// detail views so the new state shows immediately. The toggle in the
// controls panel reads its label via repoParallelExecutionFlags, which
// re-reads the config every render, so the next refresh picks up the new
// value automatically.
//
// Note: APPR_AI_SAL_PARALLEL_SPECIALISTS, when set, is applied on top of the
// loaded value at runtime, so an env-forced state will continue to win
// visually. We still write the user's choice through to disk so it sticks
// once the env var is unset.
func (m *Model) toggleParallelSpecialists() error {
	cfg, err := repoconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = repoconfig.Default()
	}
	cfg.ParallelSpecialists = !cfg.ParallelSpecialists
	if err := repoconfig.Save(cfg, ""); err != nil {
		return err
	}
	m.refreshDetailViews()
	return nil
}

func (m *Model) toggleParallelPRAgents() error {
	cfg, err := repoconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = repoconfig.Default()
	}
	cfg.ParallelPRAgents = !cfg.ParallelPRAgents
	if err := repoconfig.Save(cfg, ""); err != nil {
		return err
	}
	m.refreshDetailViews()
	return nil
}

// detailBackToList returns from the PR detail view to the list. The
// relayout call is load-bearing — see the inline note.
func (m *Model) detailBackToList() {
	m.centerView = centerDiff
	m.diffOnly = false
	m.host.BackToList()
	// Re-sync the bubbles list height to the current chrome
	// budget. Opening the PR populated m.prLanguages, which
	// flips the selected list row's lang-agents freshness from
	// Unknown to Missing/Stale and lengthens the status hint.
	// The hint then wraps to one extra row in renderStatus, so
	// chromeBodyHeight shrinks by one — but the list was sized
	// before the round-trip and would otherwise produce one row
	// too many, overflowing m.height. The renderer would then
	// drop the header line, every visible row would shift up by
	// one, and bubblezone's recorded zone Y values would point
	// one row below the visible search / URL inputs (the click
	// would land in the panel's bottom border instead). See
	// TestPanelZonesRemainAlignedAfterDetailRoundTrip.
	m.relayout()
}

// detailToggleDescription swaps the centre pane between Description and
// Diff (the `g` shortcut / "description" status hint / mini-header chip).
func (m *Model) detailToggleDescription() tea.Cmd {
	if m.centerView == centerDescription {
		m.centerView = centerDiff
	} else {
		m.centerView = centerDescription
	}
	m.refreshDetailViews()
	return m.ensureCenterDataLoaded()
}

// detailToggleDiffOnly flips the full-width diff layout (the `d` shortcut
// / "diff-only" status hint).
func (m *Model) detailToggleDiffOnly() {
	m.diffOnly = !m.diffOnly
	m.refreshDetailViews()
}

// detailToggleControls hides/shows the right-hand Review controls pane.
// When the user hides it explicitly, remember that preference so a window
// resize that would otherwise auto-show it stays hidden.
func (m *Model) detailToggleControls() {
	m.controlsUserHidden = !m.controlsUserHidden
	if m.focusedPane == paneControls {
		m.focusedPane = paneDiff
	}
	m.refreshDetailViews()
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Phase 5 item 8: while the review-history reply prompt is open it owns
	// every keystroke (the reply field must receive them all).
	if m.replyingTo != "" {
		return m.handleReplyKey(msg)
	}
	// Phase 5 item 4: while the in-diff search prompt is open it owns every
	// keystroke (the query field must receive them all) — route before any of
	// the normal detail bindings can steal a key.
	if m.diffSearching {
		return m.handleDiffSearchKey(msg)
	}
	// Phase 5 item 8: the review-history pane owns j/k/r/esc while it's shown.
	if m.centerView == centerHistory && m.focusedPane == paneDiff {
		if cmd, handled := m.handleHistoryKey(msg); handled {
			return m, cmd
		}
	}
	// Match against the central keymap (m.keys). Space / enter deliberately
	// do NOT return in every branch: when the focused pane isn't the tree
	// they fall out of the switch to the viewport page-scroll below, exactly
	// as the pre-migration raw-string switch did.
	km := m.keys
	// Phase 5 item 4/8 diff-pane bindings. Only active when the diff pane is
	// focused and showing the diff so n/p/`/`/t don't shadow other panes.
	if m.focusedPane == paneDiff && (m.diffOnly || m.centerView == centerDiff) {
		switch {
		case key.Matches(msg, km.DetailDiffFind):
			return m, m.beginDiffSearch()
		case key.Matches(msg, km.DetailDiffNext):
			m.jumpDiffForward()
			return m, nil
		case key.Matches(msg, km.DetailDiffPrev):
			m.jumpDiffBackward()
			return m, nil
		case key.Matches(msg, km.DetailThreads):
			return m, m.toggleThreads()
		case key.Matches(msg, km.DetailReviewHistory):
			return m, m.openReviewHistory()
		}
	}
	switch {
	case key.Matches(msg, km.DetailBack):
		m.detailBackToList()
		return m, nil
	case key.Matches(msg, km.DetailDescription):
		// Description is now an overview row, but `g` keeps its old job
		// as a one-key shortcut — toggle Description ↔ Diff so muscle
		// memory carries over.
		return m, m.detailToggleDescription()
	case key.Matches(msg, km.DetailCyclePane):
		m.cyclePane(+1)
		m.refreshDetailViews()
		return m, nil
	case key.Matches(msg, km.DetailCyclePaneB):
		m.cyclePane(-1)
		m.refreshDetailViews()
		return m, nil
	case key.Matches(msg, km.DetailDiffOnly):
		m.detailToggleDiffOnly()
		return m, nil
	case key.Matches(msg, km.DetailReview):
		return m, m.host.StartReviewOverlay()
	case key.Matches(msg, km.DetailToggleControls):
		m.detailToggleControls()
		return m, nil
	case key.Matches(msg, km.DetailTech):
		// Tech experts share storage with repo agents (sibling json file
		// in the same per-repo cache dir), so the build path is the
		// same: open the repo-agents tab focused on the current PR's
		// repo and let the user pick the Techs section.
		return m, m.host.OpenRepoAgentsForCurrentPR(false)
	case key.Matches(msg, km.DetailReopenApproval):
		return m, m.host.ReopenApproval()
	case key.Matches(msg, km.DetailBulk):
		return m, m.host.BulkConfirm()
	case key.Matches(msg, km.DetailNavDown):
		m.detailNavigate(+1)
		m.refreshDetailViews()
		return m, m.ensureCenterDataLoaded()
	case key.Matches(msg, km.DetailNavUp):
		m.detailNavigate(-1)
		m.refreshDetailViews()
		return m, m.ensureCenterDataLoaded()
	case key.Matches(msg, km.DetailFold):
		// Space on a folder row toggles its collapsed state; on a file
		// row it's consumed (no-op) so it doesn't accidentally page-scroll
		// the tree pane. Other panes still page-scroll on space via the
		// viewport fallthrough below.
		if m.focusedPane == paneTree {
			if m.treeIdx >= 0 && m.treeIdx < len(m.treeViewRows) {
				vr := m.treeViewRows[m.treeIdx]
				if !vr.isFile {
					m.toggleFolderCollapse(vr.fullPath)
				}
			}
			return m, nil
		}
	case key.Matches(msg, km.DetailEnter):
		// Enter on a folder row toggles collapse, on a file row it's a
		// no-op (selection is already updated by j/k or click).
		if m.focusedPane == paneTree && m.treeIdx >= 0 && m.treeIdx < len(m.treeViewRows) {
			vr := m.treeViewRows[m.treeIdx]
			if !vr.isFile {
				m.toggleFolderCollapse(vr.fullPath)
			}
			return m, nil
		}
	case key.Matches(msg, km.DetailHalfDown):
		m.diffView.ScrollDown(max(1, m.diffView.Height/2))
		return m, nil
	case key.Matches(msg, km.DetailHalfUp):
		m.diffView.ScrollUp(max(1, m.diffView.Height/2))
		return m, nil
	case key.Matches(msg, km.SettingsAI):
		return m, m.host.OpenSettings(settings.StartAI)
	case key.Matches(msg, km.SettingsReview):
		return m, m.host.OpenSettings(settings.StartReview)
	case key.Matches(msg, km.RepoCtx):
		return m, m.host.OpenSettings(settings.StartRepoContext)
	case key.Matches(msg, km.RepoAgents):
		// Pre-focus on the current PR's repo so the tab opens on the row
		// that matters for this PR rather than the alphabetical first repo.
		return m, m.host.OpenRepoAgentsForCurrentPR(false)
	case key.Matches(msg, km.LangAgents):
		return m, m.host.OpenLangAgents()
	case key.Matches(msg, km.BuildAgents):
		// "Build/refresh repo agents for this PR's repo" — focus on the
		// PR's repo and immediately fire Regenerate all. This is the
		// one-key path the user asked for: from a PR, build the per-repo
		// agents that will be injected into the next review.
		return m, m.host.OpenRepoAgentsForCurrentPR(true)
	case key.Matches(msg, km.Browser):
		return m, m.host.OpenBrowser()
	case key.Matches(msg, km.CopyURL):
		return m, m.host.CopyURL()
	}
	// fallthrough: pane scroll for the focused pane
	switch m.focusedPane {
	case paneDiff:
		var cmd tea.Cmd
		m.diffView, cmd = m.diffView.Update(msg)
		return m, cmd
	case paneTree:
		var cmd tea.Cmd
		m.treeView, cmd = m.treeView.Update(msg)
		return m, cmd
	case paneControls:
		var cmd tea.Cmd
		m.controlsView, cmd = m.controlsView.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) cyclePane(dir int) {
	count := paneCount
	if m.controlsHidden {
		count = 2
	}
	cur := int(m.focusedPane)
	cur = (cur + dir + count) % count
	m.focusedPane = pane(cur)
}

func (m *Model) detailNavigate(dir int) {
	switch m.focusedPane {
	case paneTree:
		m.detailNavigateLeftColumn(dir)
	case paneDiff:
		if dir > 0 {
			m.diffView.ScrollDown(1)
		} else {
			m.diffView.ScrollUp(1)
		}
	case paneControls:
		if dir > 0 {
			m.controlsView.ScrollDown(1)
		} else {
			m.controlsView.ScrollUp(1)
		}
	}
}

// detailNavigateLeftColumn walks the unified left-column row list:
//
//	[Description, Checks, Discussion, <tree row 0>, <tree row 1>, …]
//
// The first three rows correspond to the overview selector at the top of
// the pane and drive m.centerView; the remaining rows are tree rows and
// drive m.treeIdx + m.selectedFilePath. j/k crosses the boundary so the
// cursor naturally transitions from "browsing PR overview" to "browsing
// the diff for a specific file".
func (m *Model) detailNavigateLeftColumn(dir int) {
	idx := leftColumnIndexFor(m.centerView, m.treeIdx)
	total := overviewItemCount + len(m.treeViewRows)
	if total == 0 {
		return
	}
	idx = clampInt(idx+dir, 0, total-1)
	m.applyLeftColumnIndex(idx)
}

// leftColumnIndexFor returns the unified-list cursor implied by the current
// (centerView, treeIdx). When the user is on an overview pane the cursor is
// 0/1/2; otherwise it is overviewItemCount + treeIdx.
func leftColumnIndexFor(v centerView, treeIdx int) int {
	if k, ok := centerToOverviewKind(v); ok {
		return int(k)
	}
	if treeIdx < 0 {
		return overviewItemCount
	}
	return overviewItemCount + treeIdx
}

// applyLeftColumnIndex commits a unified-list cursor index back to
// centerView / treeIdx / selectedFilePath. If the new index points at a
// tree file row, selectedFilePath is updated so the centre pane re-renders
// against the right diff.
func (m *Model) applyLeftColumnIndex(idx int) {
	if idx < overviewItemCount {
		m.centerView = overviewKindToCenter(overviewItemKind(idx))
		return
	}
	m.centerView = centerDiff
	if len(m.treeViewRows) == 0 {
		m.treeIdx = 0
		return
	}
	m.treeIdx = clampInt(idx-overviewItemCount, 0, len(m.treeViewRows)-1)
	vr := m.treeViewRows[m.treeIdx]
	if vr.isFile && vr.fileIndex >= 0 && vr.fileIndex < len(m.treeRows) {
		m.selectedFilePath = m.treeRows[vr.fileIndex].Path
		m.diffView.SetYOffset(0)
	}
	m.scrollToSelectedFile = true
}

func (m *Model) ensureCenterDataLoaded() tea.Cmd {
	if m.currentPR() == nil {
		return nil
	}
	ref := gh.Ref{Owner: m.currentPR().Owner, Repo: m.currentPR().Repo, Number: m.currentPR().Number}
	switch m.centerView {
	case centerChecks:
		if m.checks != nil || m.checksLoading || m.checksErr != nil {
			return nil
		}
		m.checksLoading = true
		return data.LoadChecksCmd(ref, m.host.Demo())
	case centerDiscussion:
		if m.discussion != nil || m.discussionLoading || m.discussionErr != nil {
			return nil
		}
		m.discussionLoading = true
		return data.LoadDiscussionCmd(ref, m.host.Demo())
	}
	return nil
}

// resetOverviewData clears the cached checks / discussion data so the next
// PR-detail open re-fetches against the new ref. Called from the
// PRDetailMsg handler in the root model.
func (m *Model) resetOverviewData() {
	m.checks = nil
	m.checksLoading = false
	m.checksErr = nil
	m.discussion = nil
	m.discussionLoading = false
	m.discussionErr = nil
}

// recomputeTreeView rebuilds the hierarchical view rows + index maps from
// the current treeRows + collapsedFolders. Called whenever treeRows or
// the collapse state changes. After rebuilding it tries to keep treeIdx
// pointing at the currently selected file's row so cursor position
// doesn't jump on toggle.
func (m *Model) recomputeTreeView() {
	if m.collapsedFolders == nil {
		m.collapsedFolders = map[string]bool{}
	}
	view, fileToLine, lineToFile := buildTreeView(m.treeRows, m.collapsedFolders)
	m.treeViewRows = view
	m.treeFileToLine = fileToLine
	m.treeLineToFile = lineToFile

	// Re-anchor the cursor onto the row matching m.selectedFilePath when
	// possible; falls back to clamping into bounds if the selected file is
	// hidden (e.g. its parent folder was just collapsed).
	if m.selectedFilePath != "" {
		for i, fr := range m.treeRows {
			if fr.Path == m.selectedFilePath {
				if i < len(fileToLine) && fileToLine[i] >= 0 {
					m.treeIdx = fileToLine[i]
				}
				break
			}
		}
	}
	if m.treeIdx >= len(m.treeViewRows) {
		m.treeIdx = max(0, len(m.treeViewRows)-1)
	}
	if m.treeIdx < 0 {
		m.treeIdx = 0
	}
}

// applyScrollToSelectedFile, when m.scrollToSelectedFile is set, adjusts
// the tree viewport's YOffset so m.treeIdx (the cursor row) sits inside
// the visible window. Mirrors jj-tui's GraphResult.FileIndexToLineIndex
// + scrollToSelectedFile gate so wheel-scroll never fights cursor moves.
//
// Strategy: if the cursor is above the visible window scroll up to put
// it on the top line; if below, scroll down to put it on the bottom
// line. Otherwise leave YOffset alone.
func (m *Model) applyScrollToSelectedFile() {
	if !m.scrollToSelectedFile {
		return
	}
	m.scrollToSelectedFile = false
	if m.treeView.Height <= 0 || len(m.treeViewRows) == 0 {
		return
	}
	target := m.treeIdx
	if target < 0 || target >= len(m.treeViewRows) {
		return
	}
	top := m.treeView.YOffset
	bottom := top + m.treeView.Height - 1
	switch {
	case target < top:
		m.treeView.SetYOffset(target)
	case target > bottom:
		m.treeView.SetYOffset(target - m.treeView.Height + 1)
	}
}

// toggleFolderCollapse flips the collapsed state of fullPath, rebuilds
// the view rows, and triggers a refresh + scroll-to-selected so the
// resulting layout keeps the toggled folder visible. fullPath is the
// cumulative directory path stored on a folder treeViewRow (no trailing
// slash).
func (m *Model) toggleFolderCollapse(fullPath string) {
	if fullPath == "" {
		return
	}
	if m.collapsedFolders == nil {
		m.collapsedFolders = map[string]bool{}
	}
	if m.collapsedFolders[fullPath] {
		delete(m.collapsedFolders, fullPath)
	} else {
		m.collapsedFolders[fullPath] = true
	}
	m.recomputeTreeView()
	// After collapse, re-anchor cursor onto the toggled folder's row
	// (recomputeTreeView only re-anchored to the selected file).
	for i, vr := range m.treeViewRows {
		if !vr.isFile && vr.fullPath == fullPath {
			m.treeIdx = i
			break
		}
	}
	m.scrollToSelectedFile = true
	m.refreshDetailViews()
}

func (m *Model) refreshDetailViews() {
	w, h := m.width, m.host.Height()
	if w == 0 {
		w = m.host.Width()
	}
	if w == 0 || h == 0 {
		return
	}
	m.relayout()

	if m.currentPR() == nil {
		m.diffView.SetContent(styles.DimStyle.Render("No PR loaded."))
		return
	}

	// Centre pane content — switches on centerView. While diffOnly is
	// active we render the diff regardless of centerView so the
	// full-width diff stays consistent.
	view := m.centerView
	if m.diffOnly {
		view = centerDiff
	}
	var centerContent string
	switch view {
	case centerDescription:
		centerContent = renderDescriptionPane(m.currentPR().Body, m.diffView.Width)
	case centerChecks:
		centerContent = renderChecksPane(m.checks, m.checksLoading, m.checksErr, m.diffView.Width)
	case centerDiscussion:
		centerContent = renderDiscussionPane(m.discussion, m.discussionLoading, m.discussionErr, m.diffView.Width)
	case centerHistory:
		centerContent = m.renderHistoryPane(m.diffView.Width)
	default:
		var selFile *review.FileDiff
		if m.selectedFilePath != "" {
			selFile = review.FindFile(m.parsedDiff(), m.selectedFilePath)
		}
		var comments []gh.PullReviewComment
		if m.showThreads {
			comments = m.prComments
		}
		centerContent = renderDiffPaneHL(selFile, m.draft(), m.focusedPane == paneDiff, m.diffView.Width, m.highlighter(), comments)
	}
	wrapped := util.WrapForViewport(centerContent, m.diffView.Width)
	m.diffView.SetContent(wrapped)
	// Phase 5 item 4: rebuild the diff navigation indexes from the finished,
	// wrapped content so n/p (finding tags) and `/` (search) target the right
	// rows regardless of how lines wrapped. Only meaningful on the diff view.
	if view == centerDiff {
		m.diffContentLines = strings.Split(wrapped, "\n")
		m.diffAnchors = diffview.BuildAnchorIndex(diffAnchorRows(m.diffContentLines))
		if m.diffSearchQuery != "" {
			m.diffSearch = diffview.BuildSearchIndex(m.diffContentLines, m.diffSearchQuery)
		} else {
			m.diffSearch = diffview.SearchIndex{}
		}
	} else {
		m.diffContentLines = nil
		m.diffAnchors = diffview.AnchorIndex{}
		m.diffSearch = diffview.SearchIndex{}
	}

	// Tree pane: do not run util.WrapForViewport here — renderTreePane already fits
	// each row to contentCols; wrapping would split bubblezone row markers across
	// lines and break mouse hit-testing.
	treeContent := renderTreePane(m.treeViewRows, m.treeRows, m.collapsedFolders, m.treeIdx, m.treeView.Width, m.focusedPane == paneTree && m.centerView == centerDiff)
	m.treeScrollLines = util.ViewportLineCount(treeContent)
	m.treeView.SetContent(treeContent)
	m.applyScrollToSelectedFile()

	// Controls pane: only repopulate when actually visible — relayout
	// shrinks the viewport to 1x1 when hidden, so wasted work is small
	// either way but the zone marks would otherwise leak into a hidden
	// region and confuse the bubblezone scan.
	if !m.controlsHidden {
		m.controlsView.SetContent(m.renderControlsPane(m.controlsView.Width))
	}
}

// renderDetailMiniHeader paints the strip above the three-pane PR detail
// body. It carries PR-wide stats (file count, +/-, review badges) and the
// global action chips (open-in-browser, reopen-approval, view indicator).
// The view indicator shows which centre-pane view is active so users with
// keyboards-only workflows always know where they are even when the
// overview selector is offscreen.
func (m *Model) renderDetailMiniHeader() string {
	if m.currentPR() == nil {
		return ""
	}
	totalA, totalD := 0, 0
	for _, f := range m.parsedDiff() {
		totalA += f.Additions
		totalD += f.Deletions
	}
	files := len(m.parsedDiff())
	parts := []string{
		styles.DimStyle.Render(fmt.Sprintf("%d file(s)", files)),
		fmt.Sprintf("%s/%s",
			styles.OkStyle.Render(fmt.Sprintf("+%d", totalA)),
			styles.ErrStyle.Render(fmt.Sprintf("-%d", totalD))),
	}
	if badge := m.host.ReviewStateBadge(m.currentPR().ReviewState); badge != "" {
		parts = append(parts, badge)
	}
	if hint := m.host.ViewerActionBadge(m.currentPR().ReviewState); hint != "" {
		parts = append(parts, hint)
	}
	parts = append(parts, m.renderCenterViewChip())
	// Repo / lang agent chips moved into the right-hand "Review controls"
	// pane so the mini-header stays focused on PR meta. The same
	// freshness state is rendered there with explicit row labels.
	if strings.TrimSpace(m.currentPR().URL) != "" {
		parts = append(parts, zone.Mark(zones.OpenInBrowser, styles.DimStyle.Render(" open in browser (O) ")))
	}
	if m.draft() != nil {
		parts = append(parts, zone.Mark(zones.ReopenApproval, styles.OkStyle.Render(" reopen approval (a) ")))
	}
	line := strings.Join(parts, "  ·  ")
	return styles.AppPadding.Render(line)
}

// renderLeftColumnOverviewLeader builds the overview-selector strip that
// sits between the left column's title bar and the file tree viewport.
// outerW matches the tree pane's outer width (panel border + padding +
// viewport content); we strip the panel chrome to get the inner width
// the rows are sized to.
func (m *Model) renderLeftColumnOverviewLeader(outerW int) string {
	innerW := max(1, outerW-prDetailPanel.GetHorizontalFrameSize())
	if innerW < 8 {
		return ""
	}
	badges := m.overviewBadgesForModel()
	selKind, active := centerToOverviewKind(m.centerView)
	focused := m.focusedPane == paneTree && active
	out := renderOverviewBox(&badges, selKind, active, focused, innerW)
	return strings.TrimRight(out, "\n")
}

// leftColumnOverviewLeaderHeight reports the visible height of the
// overview selector strip for the given outer column width. relayout()
// uses this to size the tree viewport so the overview rows aren't eaten
// by the bottom of the pane.
func (m *Model) leftColumnOverviewLeaderHeight(outerW int) int {
	leader := m.renderLeftColumnOverviewLeader(outerW)
	if leader == "" {
		return 0
	}
	return lipgloss.Height(leader)
}

// renderCenterViewChip renders the "view: …" indicator. It doubles as the
// click target for the legacy DescriptionToggle zone so muscle-memory
// users can flip Description ↔ Diff from the mini-header.
func (m *Model) renderCenterViewChip() string {
	label := "view: Diff"
	switch m.centerView {
	case centerDescription:
		label = "view: Description (g)"
	case centerChecks:
		label = "view: Checks"
	case centerDiscussion:
		label = "view: Discussion"
	}
	style := styles.DimStyle
	if m.centerView != centerDiff {
		style = styles.BoldStyle
	}
	return zone.Mark(zones.DescriptionToggle, style.Render(" "+label+" "))
}

func (m *Model) renderPRDetailBody(bodyH int) string {
	mini := m.renderDetailMiniHeader()
	miniH := lipgloss.Height(mini)
	paneH := bodyH - miniH

	if m.diffOnly {
		framed := m.framePane("Diff (full width — d to restore)", &m.diffView, m.width, paneH, paneFocusFor(paneDiff, m.focusedPane), false, zones.PaneDiffBody)
		framed = zone.Mark(zones.PaneDiff, framed)
		return lipgloss.JoinVertical(lipgloss.Left, mini, framed)
	}

	phs := prDetailPanel.GetHorizontalFrameSize()
	treeOuter := m.treeView.Width + phs

	var ctlOuter int
	if !m.controlsHidden {
		ctlOuter = m.controlsView.Width + phs
	}
	diffOuter := m.width - treeOuter - ctlOuter

	// Accent the panes flanking the active drag seam so the user can
	// see which boundary they grabbed. Drag-inactive renders fall back
	// to the standard PanelBorder colour for every pane.
	treeAccent := m.paneDrag.target == dividerTreeDiff
	diffAccent := m.paneDrag.target == dividerTreeDiff || m.paneDrag.target == dividerDiffControls
	ctlAccent := m.paneDrag.target == dividerDiffControls

	overview := m.renderLeftColumnOverviewLeader(treeOuter)
	leftTitle := leftPaneTitle(m.focusedPane)
	tree := m.framePaneWithLeader(leftTitle, overview, &m.treeView, treeOuter, paneH, paneFocusFor(paneTree, m.focusedPane), treeAccent, zones.PaneTreeBody)
	tree = zone.Mark(zones.PaneTree, tree)

	diff := m.framePane(m.diffPaneTitle(), &m.diffView, diffOuter, paneH, paneFocusFor(paneDiff, m.focusedPane), diffAccent, zones.PaneDiffBody)
	diff = zone.Mark(zones.PaneDiff, diff)

	if m.controlsHidden {
		row := lipgloss.JoinHorizontal(lipgloss.Top, tree, diff)
		return lipgloss.JoinVertical(lipgloss.Left, mini, row)
	}

	controls := m.framePane(controlsPaneTitle(m.focusedPane), &m.controlsView, ctlOuter, paneH, paneFocusFor(paneControls, m.focusedPane), ctlAccent, zones.PaneControlsBody)
	controls = zone.Mark(zones.PaneControls, controls)

	row := lipgloss.JoinHorizontal(lipgloss.Top, tree, diff, controls)
	return lipgloss.JoinVertical(lipgloss.Left, mini, row)
}

// leftPaneTitle is the title text for the left-hand pane that hosts the
// PR-overview selector and the file tree. Mirrors controlsPaneTitle's
// focus-hint pattern so all three panes have a consistent header look.
func leftPaneTitle(focused pane) string {
	return "PR · Files · " + focusHint(paneTree, focused)
}

// controlsPaneTitle is the title text for the right-hand "Review controls"
// pane. Bolded when focused (mirrors focusHint).
func controlsPaneTitle(focused pane) string {
	if focused == paneControls {
		return "Review · " + focusHint(paneControls, focused)
	}
	return "Review · c hide · " + focusHint(paneControls, focused)
}

func paneFocusFor(p, focused pane) bool { return p == focused }

func focusHint(p, focused pane) string {
	if p == focused {
		return styles.BoldStyle.Render("focused (tab to switch)")
	}
	return styles.DimStyle.Render("tab")
}

func (m *Model) diffPaneTitle() string {
	if m.centerView == centerHistory {
		return "Review history · esc back"
	}
	if m.selectedFilePath == "" {
		return "Diff"
	}
	// Full string; framePane ANSI-aware truncation fits inner width (avoid byte-based trunc).
	return "Diff · " + m.selectedFilePath
}

// measureDetailPaneTitle matches framePane title rendering height for a given inner width.
func measureDetailPaneTitle(innerW int, title string, focused bool) int {
	innerW = max(1, innerW)
	titleOneLine := ansi.Truncate(title, innerW, "…")
	rendered := styles.DetailPaneTitleStyle.Width(innerW).Render(titleOneLine)
	if focused {
		rendered = styles.DetailPaneTitleStyle.Bold(true).Width(innerW).Render(titleOneLine)
	}
	return lipgloss.Height(rendered)
}

func (m *Model) framePane(title string, vp *viewport.Model, outerW, outerH int, focused, accent bool, viewportZone string) string {
	return m.framePaneWithLeader(title, "", vp, outerW, outerH, focused, accent, viewportZone)
}

// framePaneWithLeader is framePane plus an optional `leader` string drawn
// between the title strip and the scrollable viewport. The leader is for
// inline pane chrome that doesn't scroll (e.g. the PR-overview selector
// above the file tree). leader is treated as already sized to innerW;
// callers are expected to pre-truncate / pad it. An empty leader behaves
// exactly like framePane.
//
// accent=true swaps in styles.LeftPanelAccent so the pane reads as
// "active" — used by the drag-resize handler to highlight the panes
// flanking the seam currently being dragged.
func (m *Model) framePaneWithLeader(title, leader string, vp *viewport.Model, outerW, outerH int, focused, accent bool, viewportZone string) string {
	frame := prDetailPanel
	if accent {
		frame = styles.LeftPanelAccent
	}
	innerW := max(1, outerW-frame.GetHorizontalFrameSize())
	titleOneLine := ansi.Truncate(title, innerW, "…")
	rendered := styles.DetailPaneTitleStyle.Width(innerW).Render(titleOneLine)
	if focused {
		rendered = styles.DetailPaneTitleStyle.Bold(true).Width(innerW).Render(titleOneLine)
	}
	vpStr := vp.View()
	if viewportZone != "" {
		vpStr = zone.Mark(viewportZone, vpStr)
	}
	var col string
	if strings.TrimSpace(leader) != "" {
		col = lipgloss.JoinVertical(lipgloss.Left, rendered, leader, vpStr)
	} else {
		col = lipgloss.JoinVertical(lipgloss.Left, rendered, vpStr)
	}
	// IMPORTANT: in lipgloss, Style.Width/Height set INTERIOR content
	// dimensions (excluding border + padding) while MaxWidth/MaxHeight cap
	// the TOTAL rendered dimensions. Setting Height(outerH) inflates the
	// interior to outerH rows so the total becomes outerH + frame; MaxHeight
	// then clips the bottom 2 rows — which are the bottom padding row and
	// the bottom border. The bug is invisible on middle panes (the next
	// pane's left border hides the missing right border in JoinHorizontal)
	// but obvious on the rightmost pane and on every pane's bottom edge.
	//
	// The viewport content is already sized to exactly fill the interior
	// (title + vp.View() == outerH - vertical frame), so dropping Width /
	// Height entirely lets lipgloss size from content while MaxWidth /
	// MaxHeight guard against accidental overflow.
	return frame.MaxWidth(outerW).MaxHeight(outerH).Render(col)
}
