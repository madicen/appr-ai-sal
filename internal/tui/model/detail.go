package model

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

func (m *Model) detailHandleMouse(msg tea.MouseMsg, wheel bool) (tea.Model, tea.Cmd) {
	if m.opts.MouseYAdjust != 0 {
		msg.Y += m.opts.MouseYAdjust
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

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	m.debugLogDetailMouse(msg)

	// Reopen approval / description toggle / finish (they take precedence over
	// pane focus changes since they are header chips).
	if z := zone.Get(zones.ReopenApproval); z != nil && z.InBounds(msg) {
		return m.reopenApprovalIfPossible()
	}
	if z := zone.Get(zones.DescriptionToggle); z != nil && z.InBounds(msg) {
		m.descriptionOpen = !m.descriptionOpen
		m.refreshDetailViews()
		return m, nil
	}
	if z := zone.Get(zones.OpenInBrowser); z != nil && z.InBounds(msg) {
		if m.currentPR != nil {
			if u := strings.TrimSpace(m.currentPR.URL); u != "" {
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

	// Tree row clicks (zone per row, then viewport body for padded filler rows)
	if ti, ok := m.treeRowFromMouse(msg); ok {
		m.focusedPane = paneTree
		m.treeIdx = ti
		m.selectedFilePath = m.treeRows[ti].Path
		m.diffView.SetYOffset(0)
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
	case zoneInBounds(zones.ControlsProfilePrev, msg):
		m.cycleAIProfile(-1)
		return nil, true
	case zoneInBounds(zones.ControlsProfileNext, msg):
		m.cycleAIProfile(+1)
		return nil, true
	case zoneInBounds(zones.ControlsProfileEdit, msg):
		return m.openSettings(settings.StartAI), true
	case zoneInBounds(zones.ControlsRepoAgents, msg):
		return m.openRepoAgentsForCurrentPR(true), true
	case zoneInBounds(zones.ControlsTechAgents, msg):
		return m.openRepoAgentsForCurrentPR(true), true
	case zoneInBounds(zones.ControlsLangAgents, msg):
		return m.openLangAgents(), true
	case zoneInBounds(zones.ControlsToggleParallel, msg):
		// Parallel specialists is a repoconfig knob, not a transient
		// runtime flag. Direct the user to the settings tab where the
		// persistent toggle lives.
		return m.openSettings(settings.StartRepoContext), true
	case zoneInBounds(zones.ControlsToggleDryRun, msg):
		m.opts.DryRun = !m.opts.DryRun
		m.refreshDetailViews()
		return nil, true
	case zoneInBounds(zones.ControlsTogglePeruse, msg):
		m.peruseRequested = !m.peruseRequested
		m.refreshDetailViews()
		return nil, true
	case zoneInBounds(zones.ControlsStartReview, msg):
		peruse := m.peruseRequested
		m.peruseRequested = false
		_, cmd := m.startReviewOverlay(peruse)
		return cmd, true
	case zoneInBounds(zones.ControlsStartReviewPeruse, msg):
		m.peruseRequested = false
		_, cmd := m.startReviewOverlay(true)
		return cmd, true
	}
	return nil, false
}

func (m *Model) setStrictness(level aiconfig.ReviewStrictness) {
	if m.opts.AIConfig == nil {
		m.opts.AIConfig = aiconfig.DefaultConfig()
	}
	m.opts.AIConfig.ReviewStrictness = level
	m.refreshDetailViews()
}

func (m *Model) cycleAIProfile(delta int) {
	if m.opts.AIConfig == nil {
		return
	}
	m.opts.AIConfig.CycleActive(delta)
	m.refreshDetailViews()
}

func (m *Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.descriptionOpen = false
		m.diffOnly = false
		m.mode = modeList
		return m, nil
	case "g":
		m.descriptionOpen = !m.descriptionOpen
		m.refreshDetailViews()
		return m, nil
	case "tab":
		m.cyclePane(+1)
		m.refreshDetailViews()
		return m, nil
	case "shift+tab":
		m.cyclePane(-1)
		m.refreshDetailViews()
		return m, nil
	case "d":
		m.diffOnly = !m.diffOnly
		m.refreshDetailViews()
		return m, nil
	case "r":
		peruse := m.peruseRequested
		m.peruseRequested = false
		return m.startReviewOverlay(peruse)
	case "ctrl+v":
		// Peruse: same review run, read-only walkthrough. The overlay
		// disables post/skip actions and lets the user see the final
		// rendered summary without committing anything to GitHub.
		m.peruseRequested = false
		return m.startReviewOverlay(true)
	case "c":
		// Toggle the right-hand "Review controls" pane. When the user
		// hides it explicitly, remember that preference so a window
		// resize that would otherwise auto-show it stays hidden.
		m.controlsUserHidden = !m.controlsUserHidden
		if m.focusedPane == paneControls {
			m.focusedPane = paneDiff
		}
		m.refreshDetailViews()
		return m, nil
	case "ctrl+t":
		// Tech experts share storage with repo agents (sibling json file
		// in the same per-repo cache dir), so the build path is the
		// same: open the repo-agents tab focused on the current PR's
		// repo and let the user pick the Techs section.
		return m, m.openRepoAgentsForCurrentPR(false)
	case "a":
		return m.reopenApprovalIfPossible()
	case "P":
		if m.draft == nil {
			return m, nil
		}
		modal := overlays.NewBulkConfirmOverlay(m.draft.Ref.String())
		cfg := overlay.DefaultOverlayConfig()
		return m, tea.Batch(
			m.overlayStack.Push(modal, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)
	case "j", "down":
		m.detailNavigate(+1)
		m.refreshDetailViews()
		return m, nil
	case "k", "up":
		m.detailNavigate(-1)
		m.refreshDetailViews()
		return m, nil
	case "ctrl+d":
		m.diffView.ScrollDown(max(1, m.diffView.Height/2))
		return m, nil
	case "ctrl+u":
		m.diffView.ScrollUp(max(1, m.diffView.Height/2))
		return m, nil
	case "o":
		return m, m.openSettings(settings.StartAI)
	case ",", "ctrl+@":
		return m, m.openSettings(settings.StartReview)
	case "ctrl+g":
		return m, m.openSettings(settings.StartRepoContext)
	case "ctrl+r":
		// Pre-focus on the current PR's repo so the tab opens on the row
		// that matters for this PR rather than the alphabetical first repo.
		return m, m.openRepoAgentsForCurrentPR(false)
	case "ctrl+l":
		return m, m.openLangAgents()
	case "ctrl+b":
		// "Build/refresh repo agents for this PR's repo" — focus on the
		// PR's repo and immediately fire Regenerate all. This is the
		// one-key path the user asked for: from a PR, build the per-repo
		// agents that will be injected into the next review.
		return m, m.openRepoAgentsForCurrentPR(true)
	case "O":
		if m.currentPR != nil {
			if u := strings.TrimSpace(m.currentPR.URL); u != "" {
				return m, util.OpenInBrowserCmd(u)
			}
		}
		return m, nil
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
		if len(m.treeRows) == 0 {
			return
		}
		m.treeIdx = clampInt(m.treeIdx+dir, 0, len(m.treeRows)-1)
		m.selectedFilePath = m.treeRows[m.treeIdx].Path
		m.diffView.SetYOffset(0)
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

func (m *Model) startReviewOverlay(peruse bool) (tea.Model, tea.Cmd) {
	if m.currentPR == nil {
		return m, nil
	}
	ref := gh.Ref{Owner: m.currentPR.Owner, Repo: m.currentPR.Repo, Number: m.currentPR.Number}
	m.draft = nil
	m.recomputeTreeRows()
	parallelSpec, parallelRE := repoParallelExecutionFlags()
	ro := reviewtab.New(m.width, m.height, m.opts.DryRun, parallelSpec, parallelRE, m.opts.AIConfig)
	ro.SetPeruse(peruse)
	m.currentReviewOverlay = ro
	cfg := overlay.DefaultOverlayConfig()
	cfg.CloseOnEscape = false
	cfg.CloseOnClickOutside = false
	return m, tea.Batch(
		m.overlayStack.Push(ro, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		data.StartReviewCmd(ref, m.opts.AIConfig),
	)
}

func (m *Model) reopenApprovalIfPossible() (tea.Model, tea.Cmd) {
	if m.draft == nil {
		return m, nil
	}
	parallelSpec, parallelRE := repoParallelExecutionFlags()
	ro := reviewtab.New(m.width, m.height, m.opts.DryRun, parallelSpec, parallelRE, m.opts.AIConfig)
	adoptCmd := ro.AdoptDraft(m.draft)
	m.currentReviewOverlay = ro
	cfg := overlay.DefaultOverlayConfig()
	cfg.CloseOnEscape = false
	cfg.CloseOnClickOutside = false
	cmds := []tea.Cmd{
		m.overlayStack.Push(ro, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	}
	if adoptCmd != nil {
		cmds = append(cmds, adoptCmd)
	}
	if fetch := ro.CmdAfterAdoptIfNeeded(); fetch != nil {
		cmds = append(cmds, fetch)
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) applyProgress(p review.Progress) {
	if p.Stage == "done" {
		m.draft = p.Final
		m.recomputeTreeRows()
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
	}
}

func (m *Model) recomputeTreeRows() {
	m.treeRows = buildTreeRows(m.parsedDiff, m.draft)
	if m.treeIdx >= len(m.treeRows) {
		m.treeIdx = max(0, len(m.treeRows)-1)
	}
}

func (m *Model) refreshDetailViews() {
	if m.width == 0 || m.height == 0 {
		return
	}
	m.relayout()

	if m.currentPR == nil {
		m.diffView.SetContent(styles.DimStyle.Render("No PR loaded."))
		return
	}

	// Diff pane content
	var selFile *review.FileDiff
	if m.selectedFilePath != "" {
		selFile = review.FindFile(m.parsedDiff, m.selectedFilePath)
	}
	diffContent := renderDiffPane(selFile, m.draft, m.focusedPane == paneDiff, m.diffView.Width)
	if m.descriptionOpen && strings.TrimSpace(m.currentPR.Body) != "" {
		diffContent = renderDescriptionBlock(m.currentPR.Body, m.diffView.Width) + "\n\n" + diffContent
	}
	m.diffView.SetContent(util.WrapForViewport(diffContent, m.diffView.Width))

	// Tree pane: do not run util.WrapForViewport here — renderTreePane already fits
	// each row to contentCols; wrapping would split bubblezone row markers across
	// lines and break mouse hit-testing.
	treeContent := renderTreePane(m.treeRows, m.treeIdx, m.treeView.Width, m.focusedPane == paneTree)
	m.treeScrollLines = util.ViewportLineCount(treeContent)
	m.treeView.SetContent(treeContent)

	// Controls pane: only repopulate when actually visible — relayout
	// shrinks the viewport to 1x1 when hidden, so wasted work is small
	// either way but the zone marks would otherwise leak into a hidden
	// region and confuse the bubblezone scan.
	if !m.controlsHidden {
		m.controlsView.SetContent(m.renderControlsPane(m.controlsView.Width))
	}
}

// renderDescriptionBlock renders the PR description as an inline section
// above the diff. The body is treated as markdown — GitHub PR descriptions
// always are — and run through glamour so headings, lists, code fences,
// and links render with proper styling instead of as raw `# foo` text.
func (m *Model) renderDetailMiniHeader() string {
	if m.currentPR == nil {
		return ""
	}
	totalA, totalD := 0, 0
	for _, f := range m.parsedDiff {
		totalA += f.Additions
		totalD += f.Deletions
	}
	files := len(m.parsedDiff)
	parts := []string{
		styles.DimStyle.Render(fmt.Sprintf("%d file(s)", files)),
		fmt.Sprintf("%s/%s",
			styles.OkStyle.Render(fmt.Sprintf("+%d", totalA)),
			styles.ErrStyle.Render(fmt.Sprintf("-%d", totalD))),
	}
	if badge := reviewStateBadge(m.currentPR.ReviewState); badge != "" {
		parts = append(parts, badge)
	}
	if hint := viewerActionBadge(m.currentPR.ReviewState); hint != "" {
		parts = append(parts, hint)
	}
	desc := zone.Mark(zones.DescriptionToggle, styles.DimStyle.Render(" description (g) "))
	if m.descriptionOpen {
		desc = zone.Mark(zones.DescriptionToggle, styles.BoldStyle.Render(" description (g) "))
	}
	parts = append(parts, desc)
	// Repo / lang agent chips moved into the right-hand "Review controls"
	// pane so the mini-header stays focused on PR meta. The same
	// freshness state is rendered there with explicit row labels.
	if strings.TrimSpace(m.currentPR.URL) != "" {
		parts = append(parts, zone.Mark(zones.OpenInBrowser, styles.DimStyle.Render(" open in browser (O) ")))
	}
	if m.draft != nil {
		parts = append(parts, zone.Mark(zones.ReopenApproval, styles.OkStyle.Render(" reopen approval (a) ")))
	}
	line := strings.Join(parts, "  ·  ")
	return styles.AppPadding.Render(line)
}

func (m *Model) renderPRDetailBody(bodyH int) string {
	mini := m.renderDetailMiniHeader()
	miniH := lipgloss.Height(mini)
	paneH := bodyH - miniH

	if m.diffOnly {
		framed := m.framePane("Diff (full width — d to restore)", &m.diffView, m.width, paneH, paneFocusFor(paneDiff, m.focusedPane), zones.PaneDiffBody)
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

	tree := m.framePane("Files · "+focusHint(paneTree, m.focusedPane), &m.treeView, treeOuter, paneH, paneFocusFor(paneTree, m.focusedPane), zones.PaneTreeBody)
	tree = zone.Mark(zones.PaneTree, tree)

	diff := m.framePane(m.diffPaneTitle(), &m.diffView, diffOuter, paneH, paneFocusFor(paneDiff, m.focusedPane), zones.PaneDiffBody)
	diff = zone.Mark(zones.PaneDiff, diff)

	if m.controlsHidden {
		row := lipgloss.JoinHorizontal(lipgloss.Top, tree, diff)
		return lipgloss.JoinVertical(lipgloss.Left, mini, row)
	}

	controls := m.framePane(controlsPaneTitle(m.focusedPane), &m.controlsView, ctlOuter, paneH, paneFocusFor(paneControls, m.focusedPane), zones.PaneControlsBody)
	controls = zone.Mark(zones.PaneControls, controls)

	row := lipgloss.JoinHorizontal(lipgloss.Top, tree, diff, controls)
	return lipgloss.JoinVertical(lipgloss.Left, mini, row)
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

func (m *Model) framePane(title string, vp *viewport.Model, outerW, outerH int, focused bool, viewportZone string) string {
	innerW := max(1, outerW-prDetailPanel.GetHorizontalFrameSize())
	titleOneLine := ansi.Truncate(title, innerW, "…")
	rendered := styles.DetailPaneTitleStyle.Width(innerW).Render(titleOneLine)
	if focused {
		rendered = styles.DetailPaneTitleStyle.Bold(true).Width(innerW).Render(titleOneLine)
	}
	vpStr := vp.View()
	if viewportZone != "" {
		vpStr = zone.Mark(viewportZone, vpStr)
	}
	col := lipgloss.JoinVertical(lipgloss.Left, rendered, vpStr)
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
	return prDetailPanel.MaxWidth(outerW).MaxHeight(outerH).Render(col)
}
