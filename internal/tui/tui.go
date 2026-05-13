// Package tui is the Bubble Tea front-end for appr-ai-sal.
package tui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	langagentstui "github.com/madicen/appr-ai-sal/internal/tui/langagents"
	repoagentstui "github.com/madicen/appr-ai-sal/internal/tui/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/settings"
)

type mode int

const (
	modeList mode = iota
	modeDetail
	modeURLInput
	modeSettings
	modeRepoAgents
	modeLangAgents
)

// treePaneWidth is the fixed width allocated to the file-tree pane content
// (frame is added on top by the panel border).
const treePaneWidth = 30

// prItem adapts a gh.PR for the bubbles/list component.
type prItem struct{ pr gh.PR }

func (i prItem) FilterValue() string { return i.pr.Title + " " + i.pr.Repository }
func (i prItem) Title() string       { return fmt.Sprintf("#%d  %s", i.pr.Number, i.pr.Title) }
func (i prItem) Description() string {
	parts := []string{
		i.pr.Repository,
		"@" + i.pr.Author,
		humanSince(i.pr.UpdatedAt),
	}
	if i.pr.IsDraft {
		parts = append(parts, dimStyle.Render("draft"))
	}
	if badge := reviewStateBadge(i.pr.ReviewState); badge != "" {
		parts = append(parts, badge)
	}
	if hint := viewerActionBadge(i.pr.ReviewState); hint != "" {
		parts = append(parts, hint)
	}
	return strings.Join(parts, " · ")
}

// reviewStateBadge returns the PR-wide approval-state chip ("approved",
// "changes requested", "no review") or empty when we have no review data
// yet. Color is applied with lipgloss so the default list delegate can
// pass it through unmodified.
func reviewStateBadge(rs gh.ReviewState) string {
	switch {
	case strings.EqualFold(rs.Decision, gh.ReviewDecisionApproved):
		return okStyle.Render("approved")
	case rs.ChangesRequested > 0 || strings.EqualFold(rs.Decision, gh.ReviewDecisionChangesRequested):
		return errStyle.Render("changes requested")
	case rs.Approvals > 0:
		// Has at least one approval but GitHub still wants more (typical
		// branch-protection "2 approvals required" case).
		return warnStyle.Render(fmt.Sprintf("approved x%d · more needed", rs.Approvals))
	case strings.EqualFold(rs.Decision, gh.ReviewDecisionReviewRequired):
		return dimStyle.Render("no review")
	default:
		return ""
	}
}

// sortPRsByActionability returns prs reordered so the PRs most in need of
// the viewer's attention rise to the top. The tiers are:
//
//  0. needs you, direct request — your approval would unblock merge.
//  1. needs you, only via a team request — same urgency but ambiguous owner.
//  2. you've reviewed (commented but not approved) — already weighed in.
//  3. changes requested by someone, you haven't reviewed — author's turn.
//  4. you've already approved — done on your side.
//  5. PR is fully approved — least actionable.
//
// Within each tier we keep the most-recently-updated PR first so an active
// PR doesn't get buried under stale ones. The returned slice is a fresh
// allocation; the input is not mutated, which makes the call cheap to
// embed in the prListMsg handler without surprising callers.
func sortPRsByActionability(prs []gh.PR) []gh.PR {
	out := append([]gh.PR(nil), prs...)
	sort.SliceStable(out, func(i, j int) bool {
		ti := actionabilityTier(out[i].ReviewState)
		tj := actionabilityTier(out[j].ReviewState)
		if ti != tj {
			return ti < tj
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func actionabilityTier(rs gh.ReviewState) int {
	switch {
	case strings.EqualFold(rs.Decision, gh.ReviewDecisionApproved):
		return 5
	case rs.ViewerHasApproved:
		return 4
	case rs.ChangesRequested > 0 && !rs.ViewerHasReviewed:
		return 3
	case rs.ViewerHasReviewed:
		return 2
	case rs.NeedsViewerReview() && rs.ViewerStillRequested:
		return 0
	case rs.NeedsViewerReview():
		return 1
	default:
		return 2
	}
}

// viewerActionBadge surfaces what the viewer specifically should do with the
// PR. "needs you" is the strongest signal (direct request still pending,
// viewer hasn't reviewed). The team-fallback variant covers PRs that landed
// in our queue via team membership only.
func viewerActionBadge(rs gh.ReviewState) string {
	switch {
	case rs.ViewerHasApproved:
		return dimStyle.Render("you approved")
	case rs.ViewerHasReviewed:
		return dimStyle.Render("you reviewed")
	case rs.NeedsViewerReview() && rs.ViewerStillRequested:
		return boldStyle.Foreground(lipgloss.Color("#7AA2F7")).Render("needs you")
	case rs.NeedsViewerReview():
		return dimStyle.Render("needs you (team)")
	default:
		return ""
	}
}

// Model is the root TUI model.
type Model struct {
	opts Options
	mode mode

	settings         *settings.Model
	settingsPrevMode mode

	repoAgents         *repoagentstui.Model
	repoAgentsPrevMode mode

	langAgents         *langagentstui.Model
	langAgentsPrevMode mode

	width  int
	height int

	list         list.Model
	prsLoaded    bool
	overlayStack overlay.OverlayStack
	overlayFocus overlay.FocusTrap

	explicitReviewerOnly bool

	currentPR *gh.PR
	diff      string
	draft     *review.Draft

	// PR detail layout: tree + diff.
	parsedDiff       []review.FileDiff
	treeRows         []treeRow
	treeIdx          int
	focusedPane      pane
	selectedFilePath string
	diffOnly         bool

	treeView viewport.Model
	diffView viewport.Model

	// treeScrollLines is the line count of tree viewport content after the last
	// refresh (used for mouse row mapping; must match visible wrapped lines).
	treeScrollLines int

	// PR description overlay state (g key to open).
	descriptionOpen bool

	urlInput textinput.Model

	spinner    spinner.Model
	progressCh <-chan review.Progress

	err error

	// Active review overlay reference (for direct state pokes when posting).
	currentReviewOverlay *reviewOverlay

	// List: single click selects; double-click (same row, within window) opens PR.
	listClickArmed     bool
	listClickIndex     int
	listClickAt        time.Time
	listDoubleClickWin time.Duration

	// Tree: same single-click-to-select / double-click-to-emphasise pattern as the list.
	treeClickArmed bool
	treeClickIndex int
	treeClickAt    time.Time

	// repoAgentsFreshnessCache backs the ctrl+b chip / status hint colouring
	// so the renderer doesn't os.ReadFile on every frame. Entries are TTL'd
	// (see repoAgentsFreshnessCacheTTL); the cache is also dropped wholesale
	// on DoneMsg from the repo-agents tab and on each openRepoAgents call so
	// regen results show up immediately when the user returns to the PR.
	repoAgentsFreshnessCache map[string]repoAgentsFreshnessEntry

	// prLanguages caches the canonical touched-language set for each PR
	// we've parsed a diff for during this session. Populated whenever
	// the detail-mode loader hands us a parsedDiff. Keyed by
	// "owner/repo#NUMBER" so the list-mode hint can colour rows the
	// user has previously visited without re-fetching anything.
	//
	// Entries are sticky — diffs don't churn within a session and a
	// stale entry just means we'd render a slightly out-of-date chip;
	// the user runs a review and the freshness recomputes from the
	// updated diff. invalidateLangAgentsFreshness drops the wholesale
	// cache when the user finishes a lang-agents tab session so newly
	// generated briefs flip the chip colour on return.
	prLanguages map[string][]langagentsstore.Language

	// langAgentsFreshnessCache memoises the PR-aggregated freshness
	// reading (computed from prLanguages + the on-disk cache). Same
	// TTL story as repoAgentsFreshnessCache; same invalidation hook.
	langAgentsFreshnessCache map[string]langAgentsFreshnessEntry
}

// repoAgentsFreshnessCacheTTL bounds how long a cached freshness reading
// survives without an explicit invalidation. Short enough to pick up
// out-of-band edits to repo-agents.json without restarting the TUI; long
// enough to keep the render loop cheap.
const repoAgentsFreshnessCacheTTL = 5 * time.Second

// langAgentsFreshnessCacheTTL bounds the lang-agents freshness cache,
// same rationale as the repo-agents version.
const langAgentsFreshnessCacheTTL = 5 * time.Second

type langAgentsFreshnessEntry struct {
	state    langagentsstore.Freshness
	computed time.Time
}

type repoAgentsFreshnessEntry struct {
	state    repoagentsstore.Freshness
	computed time.Time
}

func repoParallelExecutionFlags() (specialistsParallel, repoExpertsParallel bool) {
	rc, err := repoconfig.Load()
	if err != nil || rc == nil {
		return false, false
	}
	repoconfig.ApplyParallelExecutionEnv(rc)
	return rc.ParallelSpecialists, rc.ParallelRepoExperts
}

// New constructs a fresh model in the list-loading state.
func New(opts Options) *Model {
	zone.NewGlobal()
	delegate := list.NewDefaultDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = "PRs awaiting your review"
	l.SetShowStatusBar(true)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()

	ti := textinput.New()
	ti.Placeholder = "https://github.com/owner/repo/pull/123  or  owner/repo#123"
	ti.CharLimit = 200
	ti.Width = 80

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	if opts.AIConfig == nil {
		opts.AIConfig = aiconfig.DefaultConfig()
	}

	tv := viewport.New(0, 0)
	dv := viewport.New(0, 0)
	for _, vp := range []*viewport.Model{&tv, &dv} {
		vp.SetHorizontalStep(4)
		// Parent routes wheel events to exactly one pane; disable built-in
		// viewport mouse handling so wheels never hit two panes at once.
		vp.MouseWheelEnabled = false
	}

	m := &Model{
		opts:               opts,
		mode:               modeList,
		list:               l,
		urlInput:           ti,
		spinner:            sp,
		treeView:           tv,
		diffView:           dv,
		focusedPane:        paneTree,
		listDoubleClickWin: 500 * time.Millisecond,
	}
	m.overlayFocus.Stack = &m.overlayStack
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(loadPRsCmd(m.explicitReviewerOnly), m.spinner.Tick)
}

// reviewOverlayOnTop returns the active review overlay if it sits at the top
// of the modal stack, otherwise nil.
func (m *Model) reviewOverlayOnTop() *reviewOverlay {
	if top := m.overlayStack.Top(); top != nil {
		if ro, ok := top.(*reviewOverlay); ok {
			return ro
		}
	}
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		if m.mode == modeSettings && m.settings != nil {
			m.settings.Resize(m.width, m.chromeBodyHeight())
		}
		if m.mode == modeRepoAgents && m.repoAgents != nil {
			m.repoAgents.Resize(m.width, m.chromeBodyHeight())
		}
		if m.mode == modeLangAgents && m.langAgents != nil {
			m.langAgents.SetSize(m.width, m.chromeBodyHeight())
		}
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, m.overlayStack.Update(msg)

	case bulkPostAnswerMsg:
		_, popCmd := m.overlayStack.Pop()
		if msg.Confirm && m.draft != nil && m.draft.PR != nil {
			return m, tea.Sequence(popCmd, postReviewCmd(m.draft.Ref, m.draft, m.opts.DryRun))
		}
		return m, popCmd

	case errorOverlayDismissMsg:
		_, c := m.overlayStack.Pop()
		m.err = nil
		return m, c

	case dryRunDismissMsg:
		_, c := m.overlayStack.Pop()
		return m, c

	case reviewOverlayCloseMsg:
		_, c := m.overlayStack.Pop()
		m.currentReviewOverlay = nil
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, c

	case settings.DoneMsg:
		m.mode = m.settingsPrevMode
		m.settings = nil
		if msg.Cancelled {
			return m, nil
		}
		if msg.Err != nil {
			em := newErrorOverlay(msg.Err.Error(), max(40, m.width-6), max(8, m.height-8))
			cfg := overlay.DefaultOverlayConfig()
			return m, tea.Batch(
				m.overlayStack.Push(em, cfg),
				func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
			)
		}
		if msg.Cfg != nil {
			m.opts.AIConfig = msg.Cfg.Clone()
		}
		m.relayout()
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, nil

	case repoagentstui.DoneMsg:
		m.mode = m.repoAgentsPrevMode
		m.repoAgents = nil
		// Any specialist for any repo could have been regenerated, added,
		// or deleted while the tab was open; the safest invalidation is
		// to drop the whole cache so the chip / status hint re-read on
		// the next render.
		m.invalidateRepoAgentsFreshness()
		m.relayout()
		if msg.Err != nil {
			em := newErrorOverlay(msg.Err.Error(), max(40, m.width-6), max(8, m.height-8))
			cfg := overlay.DefaultOverlayConfig()
			return m, tea.Batch(
				m.overlayStack.Push(em, cfg),
				func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
			)
		}
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, nil

	case langagentstui.DoneMsg:
		m.mode = m.langAgentsPrevMode
		m.langAgents = nil
		m.invalidateLangAgentsFreshness()
		m.relayout()
		if msg.Err != nil {
			em := newErrorOverlay(msg.Err.Error(), max(40, m.width-6), max(8, m.height-8))
			cfg := overlay.DefaultOverlayConfig()
			return m, tea.Batch(
				m.overlayStack.Push(em, cfg),
				func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
			)
		}
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, nil

	case postedOverlayDismissMsg:
		_, c := m.overlayStack.Pop()
		m.mode = modeList
		m.draft = nil
		return m, tea.Batch(c, loadPRsCmd(m.explicitReviewerOnly))

	case dryRunPayloadMsg:
		// If the persistent review overlay is active, let it absorb the dry-run
		// receipt internally — we don't want to cover the approval flow with
		// another modal in that case.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.Update(msg)
			return m, nil
		}
		modal := newDryRunOverlay(msg.Title, msg.Payload, max(40, m.width-6), max(12, m.height-6))
		cfg := overlay.DefaultOverlayConfig()
		return m, tea.Batch(
			m.overlayStack.Push(modal, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)

	case stagedFindingPostedMsg:
		// Posting the last card causes advanceCard → enterSummary, which
		// returns the vibe-coach goroutine cmd. We MUST forward that cmd
		// or the overlay flips into phaseGeneratingSummary with
		// coachInFlight=true but no goroutine ever runs, leaving the UI
		// stuck on the "Refining summary…" interstitial forever.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil

	case vibeCoachDoneMsg:
		// The deferred vibe-coach goroutine completed. The overlay's
		// handler is the only thing that knows what to do with the
		// result; without an explicit case here the message would fall
		// through the root Update without ever reaching the overlay,
		// stranding it in phaseGeneratingSummary.
		//
		// The overlay's handler may re-issue against a fresher skip set
		// (returns m.enterSummary()), so forward the cmd.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil

	case prListMsg:
		m.prsLoaded = true
		ordered := sortPRsByActionability(msg.prs)
		items := make([]list.Item, 0, len(ordered))
		for _, p := range ordered {
			items = append(items, prItem{pr: p})
		}
		m.list.SetItems(items)
		m.updateListTitle()
		m.resetListClickTracking()
		return m, nil

	case prDetailMsg:
		m.resetListClickTracking()
		m.currentPR = msg.pr
		m.diff = msg.diff
		m.draft = nil
		m.parsedDiff = review.ParseDiff(m.diff)
		m.recordPRLanguages(msg.pr, m.parsedDiff)
		m.treeRows = buildTreeRows(m.parsedDiff, m.draft)
		m.treeIdx = 0
		m.diffOnly = false
		m.focusedPane = paneTree
		m.selectedFilePath = ""
		if len(m.parsedDiff) > 0 {
			m.selectedFilePath = m.parsedDiff[0].Path
		}
		m.mode = modeDetail
		m.refreshDetailViews()
		return m, nil

	case reviewStartedMsg:
		m.progressCh = msg.ch
		return m, waitForProgressCmd(m.progressCh)

	case progressMsg:
		m.applyProgress(review.Progress(msg))
		cmd := waitForProgressCmd(m.progressCh)
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, overlayCmd := ro.Update(msg)
			cmd = tea.Batch(cmd, overlayCmd)
		}
		return m, cmd

	case existingPRCommentsMsg:
		// The overlay's handler can return a markCardsAlreadyOnGitHub
		// cmd — forward it so the duplicate-detection pass actually runs
		// instead of stalling on "Checking GitHub for inline comments…".
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil

	case prRefreshedMsg:
		// Update root state so the detail view's diff/PR head SHA stay in
		// sync with whatever the overlay just adopted (force-pushed commits,
		// renamed files, etc.).
		if msg.pr != nil {
			if m.currentPR != nil && m.currentPR.Number == msg.pr.Number && m.currentPR.Owner == msg.pr.Owner && m.currentPR.Repo == msg.pr.Repo {
				m.currentPR = msg.pr
				m.diff = msg.diff
				m.parsedDiff = review.ParseDiff(m.diff)
				m.recordPRLanguages(msg.pr, m.parsedDiff)
				m.treeRows = buildTreeRows(m.parsedDiff, m.draft)
				m.refreshDetailViews()
			}
			if m.draft != nil && m.draft.PR != nil && m.draft.PR.Number == msg.pr.Number {
				m.draft.PR = msg.pr
				m.draft.Diff = msg.diff
			}
		}
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil

	case reviewClosedMsg:
		m.progressCh = nil
		// Persistent overlay stays open through the approval flow; we no
		// longer auto-pop it here.
		if m.draft != nil {
			m.recomputeTreeRows()
			m.refreshDetailViews()
		}
		return m, nil

	case postDoneMsg:
		// Either summary (from review overlay) or bulk (legacy P key) just
		// posted successfully.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.MarkSummaryPosted()
			return m, nil
		}
		m.stagedReset()
		m.mode = modeDetail
		m.refreshDetailViews()
		pm := postedOverlay{}
		cfg := overlay.DefaultOverlayConfig()
		cfg.CloseOnClickOutside = false
		return m, tea.Batch(
			m.overlayStack.Push(pm, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)

	case browserOpenedMsg:
		if msg.Err == nil {
			return m, nil
		}
		// Surface launch failures (xdg-open missing, unsupported scheme,
		// etc.) through the standard error overlay so the user can see
		// what went wrong without us hijacking the more disruptive
		// errMsg pathway (which would also clobber review-overlay state).
		em := newErrorOverlay(
			fmt.Sprintf("open in browser: %s\n\nURL: %s", msg.Err.Error(), msg.URL),
			max(40, m.width-6), max(8, m.height-8),
		)
		cfg := overlay.DefaultOverlayConfig()
		return m, tea.Batch(
			m.overlayStack.Push(em, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)

	case errMsg:
		m.err = msg.err
		em := newErrorOverlay(msg.err.Error(), max(40, m.width-6), max(8, m.height-8))
		cfg := overlay.DefaultOverlayConfig()
		pushErr := tea.Batch(
			m.overlayStack.Push(em, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)
		// Review overlay: still record failure on the card / summary strip, but also
		// stack the dedicated error modal (copy button, scroll) like jj-tui — the
		// inline "✗ …" line is easy to miss and truncates long gh API payloads.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.MarkPostError(msg.err)
			return m, pushErr
		}
		return m, pushErr

	case spinner.TickMsg:
		if ro := m.reviewOverlayOnTop(); ro != nil {
			return m, m.overlayStack.Update(msg)
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		if m.mode == modeSettings && m.settings != nil {
			if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+c" {
				FlushMouse()
				return m, tea.Quit
			}
			if !m.overlayFocus.InteractiveToBase(msg) {
				return m, m.overlayStack.Update(msg)
			}
			sm, cmd := m.settings.Update(msg)
			m.settings = sm.(*settings.Model)
			return m, cmd
		}
		if m.mode == modeRepoAgents && m.repoAgents != nil {
			if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+c" {
				FlushMouse()
				return m, tea.Quit
			}
			if !m.overlayFocus.InteractiveToBase(msg) {
				return m, m.overlayStack.Update(msg)
			}
			rm, cmd := m.repoAgents.Update(msg)
			m.repoAgents = rm.(*repoagentstui.Model)
			return m, cmd
		}
		if m.mode == modeLangAgents && m.langAgents != nil {
			if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+c" {
				FlushMouse()
				return m, tea.Quit
			}
			if !m.overlayFocus.InteractiveToBase(msg) {
				return m, m.overlayStack.Update(msg)
			}
			lm, cmd := m.langAgents.Update(msg)
			m.langAgents = lm.(*langagentstui.Model)
			return m, cmd
		}
		if !m.overlayFocus.InteractiveToBase(msg) {
			return m, m.overlayStack.Update(msg)
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			FlushMouse()
			return m, tea.Quit
		}
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	if m.mode == modeSettings && m.settings != nil {
		switch msg.(type) {
		case cursor.BlinkMsg:
			sm, cmd := m.settings.Update(msg)
			m.settings = sm.(*settings.Model)
			return m, cmd
		}
	}
	if m.mode == modeRepoAgents && m.repoAgents != nil {
		// Forward all non-key/mouse messages (loaded results, regen-done, blink
		// ticks, etc.) so the subpackage can react. The repoAgents model emits
		// custom message types we cannot pattern-match on from here.
		rm, cmd := m.repoAgents.Update(msg)
		m.repoAgents = rm.(*repoagentstui.Model)
		return m, cmd
	}
	if m.mode == modeLangAgents && m.langAgents != nil {
		lm, cmd := m.langAgents.Update(msg)
		m.langAgents = lm.(*langagentstui.Model)
		return m, cmd
	}

	switch m.mode {
	case modeSettings:
		return m, nil
	case modeRepoAgents:
		return m, nil
	case modeLangAgents:
		return m, nil
	case modeList:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	case modeDetail:
		return m, nil
	case modeURLInput:
		var cmd tea.Cmd
		m.urlInput, cmd = m.urlInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

// stagedReset clears any pre-overlay-era staged-flow state. With the
// persistent overlay, this is mostly a no-op kept for safety.
func (m *Model) stagedReset() {}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	wheel := tea.MouseEvent(msg).IsWheel()
	if !wheel && msg.Action != tea.MouseActionPress {
		return m, nil
	}

	switch m.mode {
	case modeList:
		listTop := m.listBodyOriginY()
		if wheel && !m.list.SettingFilter() && msg.Y >= listTop && msg.Y < listTop+m.list.Height() {
			switch msg.Button {
			case tea.MouseButtonWheelDown:
				m.resetListClickTracking()
				m.list.CursorDown()
				return m, nil
			case tea.MouseButtonWheelUp:
				m.resetListClickTracking()
				m.list.CursorUp()
				return m, nil
			}
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if z := zone.Get(ZoneFilterToggle); z != nil && z.InBounds(msg) {
				m.resetListClickTracking()
				m.explicitReviewerOnly = !m.explicitReviewerOnly
				m.prsLoaded = false
				m.updateListTitle()
				return m, loadPRsCmd(m.explicitReviewerOnly)
			}
			if gi, ok := m.listGlobalIndexAtClick(msg); ok {
				return m.listHandleItemClick(gi)
			}
		}
		var lcmd tea.Cmd
		m.list, lcmd = m.list.Update(msg)
		return m, lcmd
	case modeDetail:
		return m.detailHandleMouse(msg, wheel)
	}
	return m, nil
}

func (m *Model) detailHandleMouse(msg tea.MouseMsg, wheel bool) (tea.Model, tea.Cmd) {
	if m.opts.MouseYAdjust != 0 {
		msg.Y += m.opts.MouseYAdjust
	}
	if wheel {
		// Route wheel by which pane bounds contain the cursor.
		switch {
		case zoneInBounds(ZonePaneTree, msg):
			wheelScrollViewport(&m.treeView, msg)
		case zoneInBounds(ZonePaneDiff, msg) || m.mouseYInChromeBody(msg):
			wheelScrollViewport(&m.diffView, msg)
		}
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	m.debugLogDetailMouse(msg)

	// Reopen approval / description toggle / finish (they take precedence over
	// pane focus changes since they are header chips).
	if z := zone.Get(ZoneReopenApproval); z != nil && z.InBounds(msg) {
		return m.reopenApprovalIfPossible()
	}
	if z := zone.Get(ZoneDescriptionToggle); z != nil && z.InBounds(msg) {
		m.descriptionOpen = !m.descriptionOpen
		m.refreshDetailViews()
		return m, nil
	}
	if z := zone.Get(ZoneBuildRepoAgents); z != nil && z.InBounds(msg) {
		return m, m.openRepoAgentsForCurrentPR(true)
	}
	if z := zone.Get(ZoneBuildLangAgents); z != nil && z.InBounds(msg) {
		return m, m.openLangAgents()
	}
	if z := zone.Get(ZoneOpenInBrowser); z != nil && z.InBounds(msg) {
		if m.currentPR != nil {
			if u := strings.TrimSpace(m.currentPR.URL); u != "" {
				return m, openInBrowserCmd(u)
			}
		}
		return m, nil
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
	case zoneInBounds(ZonePaneTree, msg):
		m.focusedPane = paneTree
		m.refreshDetailViews()
	case zoneInBounds(ZonePaneDiff, msg):
		m.focusedPane = paneDiff
		m.refreshDetailViews()
	}
	return m, nil
}

func zoneInBounds(id string, msg tea.MouseMsg) bool {
	z := zone.Get(id)
	return z != nil && z.InBounds(msg)
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeList:
		return m.handleListKey(msg)
	case modeDetail:
		return m.handleDetailKey(msg)
	case modeURLInput:
		return m.handleURLInputKey(msg)
	}
	return m, nil
}

func (m *Model) resetListClickTracking() {
	m.listClickArmed = false
	m.listClickIndex = 0
	m.listClickAt = time.Time{}
}

func (m *Model) listHandleItemClick(gi int) (tea.Model, tea.Cmd) {
	now := time.Now()
	if m.listClickArmed &&
		m.listClickIndex == gi &&
		!m.listClickAt.IsZero() &&
		now.Sub(m.listClickAt) <= m.listDoubleClickWin {
		m.resetListClickTracking()
		return m, m.listLoadDetailAtGlobalIndex(gi)
	}
	m.list.Select(gi)
	m.listClickArmed = true
	m.listClickIndex = gi
	m.listClickAt = now
	return m, nil
}

func (m *Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.resetListClickTracking()
	switch msg.String() {
	case "q":
		FlushMouse()
		return m, tea.Quit
	case "f":
		m.explicitReviewerOnly = !m.explicitReviewerOnly
		m.prsLoaded = false
		m.updateListTitle()
		return m, loadPRsCmd(m.explicitReviewerOnly)
	case "u":
		m.mode = modeURLInput
		m.urlInput.Reset()
		m.urlInput.Focus()
		return m, textinput.Blink
	case "R":
		m.prsLoaded = false
		return m, loadPRsCmd(m.explicitReviewerOnly)
	case "o":
		return m, m.openSettings(settings.StartAI)
	case ",", "ctrl+@":
		return m, m.openSettings(settings.StartReview)
	case "ctrl+g":
		return m, m.openSettings(settings.StartRepoContext)
	case "ctrl+r":
		// From the list view we can't pre-focus a single repo (the highlight
		// might not even point at a PR), so open the tab as-is.
		return m, m.openRepoAgents("", false)
	case "ctrl+l":
		return m, m.openLangAgents()
	case "ctrl+b":
		// Build/refresh repo agents for the highlighted PR's repo, if any.
		if it, ok := m.list.SelectedItem().(prItem); ok {
			focus := it.pr.Owner + "/" + it.pr.Repo
			return m, m.openRepoAgents(focus, true)
		}
		return m, m.openRepoAgents("", false)
	case "O":
		if it, ok := m.list.SelectedItem().(prItem); ok {
			if u := strings.TrimSpace(it.pr.URL); u != "" {
				return m, openInBrowserCmd(u)
			}
		}
		return m, nil
	case "enter":
		it, ok := m.list.SelectedItem().(prItem)
		if !ok {
			return m, nil
		}
		ref := gh.Ref{Owner: it.pr.Owner, Repo: it.pr.Repo, Number: it.pr.Number}
		return m, loadPRDetailCmd(ref)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
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
		return m.startReviewOverlay(false)
	case "ctrl+v":
		// Peruse: same review run, read-only walkthrough. The overlay
		// disables post/skip actions and lets the user see the final
		// rendered summary without committing anything to GitHub.
		return m.startReviewOverlay(true)
	case "a":
		return m.reopenApprovalIfPossible()
	case "P":
		if m.draft == nil {
			return m, nil
		}
		modal := newBulkConfirmOverlay(m.draft.Ref.String())
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
				return m, openInBrowserCmd(u)
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
	}
	return m, nil
}

func (m *Model) cyclePane(dir int) {
	const count = 2
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
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// startReviewOverlay constructs the review overlay, pushes it onto the
// stack, and kicks off the runner. When peruse is true, the overlay's
// peruse flag is set so post / skip actions become no-ops with a flash
// hint — the user can browse findings and the rendered summary without
// committing anything to GitHub.
func (m *Model) startReviewOverlay(peruse bool) (tea.Model, tea.Cmd) {
	if m.currentPR == nil {
		return m, nil
	}
	ref := gh.Ref{Owner: m.currentPR.Owner, Repo: m.currentPR.Repo, Number: m.currentPR.Number}
	m.draft = nil
	m.recomputeTreeRows()
	parallelSpec, parallelRE := repoParallelExecutionFlags()
	ro := newReviewOverlay(m.width, m.height, m.opts.DryRun, parallelSpec, parallelRE, m.opts.AIConfig)
	ro.peruse = peruse
	m.currentReviewOverlay = ro
	cfg := overlay.DefaultOverlayConfig()
	cfg.CloseOnEscape = false
	cfg.CloseOnClickOutside = false
	return m, tea.Batch(
		m.overlayStack.Push(ro, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		startReviewCmd(ref, m.opts.AIConfig),
	)
}

func (m *Model) reopenApprovalIfPossible() (tea.Model, tea.Cmd) {
	if m.draft == nil {
		return m, nil
	}
	parallelSpec, parallelRE := repoParallelExecutionFlags()
	ro := newReviewOverlay(m.width, m.height, m.opts.DryRun, parallelSpec, parallelRE, m.opts.AIConfig)
	adoptCmd := ro.adoptDraft(m.draft)
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
	if fetch := ro.cmdAfterAdoptIfNeeded(); fetch != nil {
		cmds = append(cmds, fetch)
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) handleURLInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.urlInput.Blur()
		return m, nil
	case "enter":
		v := strings.TrimSpace(m.urlInput.Value())
		if v == "" {
			return m, nil
		}
		ref, err := gh.ParsePRURL(v)
		if err != nil {
			return m, func() tea.Msg { return errMsg{err} }
		}
		m.urlInput.Blur()
		return m, loadPRDetailCmd(ref)
	}
	var cmd tea.Cmd
	m.urlInput, cmd = m.urlInput.Update(msg)
	return m, cmd
}

func (m *Model) updateListTitle() {
	if m.explicitReviewerOnly {
		m.list.Title = "PRs · you are explicitly requested"
	} else {
		m.list.Title = "PRs · review requested (@me, incl. teams)"
	}
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

func (m *Model) relayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	bodyH := m.chromeBodyHeight()
	headerLine := lipgloss.Height(m.renderDetailMiniHeader())

	filterH := lipgloss.Height(renderFilterLine(m.explicitReviewerOnly))
	m.list.SetSize(m.width-2, max(3, bodyH-filterH))

	switch m.mode {
	case modeDetail:
		phs := prDetailPanel.GetHorizontalFrameSize()
		pvs := prDetailPanel.GetVerticalFrameSize()
		// Outer pane height matches renderPRDetailBody (chrome body minus mini header).
		outerPaneH := max(1, bodyH-headerLine)
		if m.diffOnly {
			innerW := max(1, m.width-phs)
			titleH := measureDetailPaneTitle(innerW, "Diff (full width — d to restore)", m.focusedPane == paneDiff)
			vpH := max(1, outerPaneH-pvs-titleH)
			m.diffView.Width = max(8, m.width-phs)
			m.diffView.Height = vpH
			m.treeView.Width = 1
			m.treeView.Height = 1
			break
		}
		treeW := treePaneWidth + phs
		diffOuterW := m.width - treeW
		if diffOuterW < 12 {
			treeW = 12
			diffOuterW = m.width - treeW
		}
		m.treeView.Width = max(8, treeW-phs)
		treeOuter := m.treeView.Width + phs
		diffOuter := m.width - treeOuter
		innerTreeW := max(1, treeOuter-phs)
		innerDiffW := max(1, diffOuter-phs)
		treeTitle := "Files · " + focusHint(paneTree, m.focusedPane)
		treeTitleH := measureDetailPaneTitle(innerTreeW, treeTitle, paneFocusFor(paneTree, m.focusedPane))
		diffTitleH := measureDetailPaneTitle(innerDiffW, m.diffPaneTitle(), paneFocusFor(paneDiff, m.focusedPane))
		m.treeView.Height = max(1, outerPaneH-pvs-treeTitleH)
		m.diffView.Width = max(8, diffOuter-phs)
		m.diffView.Height = max(1, outerPaneH-pvs-diffTitleH)
	default:
		m.treeView.Width = 1
		m.treeView.Height = 1
		m.diffView.Width = 1
		m.diffView.Height = 1
	}

	m.urlInput.Width = m.width - 6
}

func (m *Model) refreshDetailViews() {
	if m.width == 0 || m.height == 0 {
		return
	}
	m.relayout()

	if m.currentPR == nil {
		m.diffView.SetContent(dimStyle.Render("No PR loaded."))
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
	m.diffView.SetContent(wrapForViewport(diffContent, m.diffView.Width))

	// Tree pane: do not run wrapForViewport here — renderTreePane already fits
	// each row to contentCols; wrapping would split bubblezone row markers across
	// lines and break mouse hit-testing.
	treeContent := renderTreePane(m.treeRows, m.treeIdx, m.treeView.Width, m.focusedPane == paneTree)
	m.treeScrollLines = viewportLineCount(treeContent)
	m.treeView.SetContent(treeContent)

}

// renderDescriptionBlock renders the PR description as an inline section
// above the diff. The body is treated as markdown — GitHub PR descriptions
// always are — and run through glamour so headings, lists, code fences,
// and links render with proper styling instead of as raw `# foo` text.
func renderDescriptionBlock(body string, width int) string {
	width = max(8, width)
	var b strings.Builder
	b.WriteString(boldStyle.Render("Description") + "  " +
		zone.Mark(ZoneDescriptionToggle, dimStyle.Render(" hide (g) ")) + "\n")
	b.WriteString(renderMarkdownIndented(strings.TrimSpace(body), width, 0) + "\n")
	return b.String()
}

// bubbleZoneCSI matches lrstanley/bubblezone private CSI markers (\x1b[Nz).
var bubbleZoneCSI = regexp.MustCompile("\x1b\\[\\d+z")

// enforceMaxLineWidth trims any output line that still exceeds the viewport after
// ansi.Wrap so bubbles/viewport's lipgloss pass does not insert extra wraps
// (which desynchronizes bubblezone hit boxes). Lines containing bubblezone
// markers are skipped — those rows must be pre-sized before zone.Mark.
func enforceMaxLineWidth(s string, maxCols int) string {
	if maxCols < 8 {
		maxCols = 8
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if bubbleZoneCSI.MatchString(line) {
			continue
		}
		if ansi.StringWidth(line) > maxCols {
			lines[i] = ansi.Truncate(line, maxCols, "")
		}
	}
	return strings.Join(lines, "\n")
}

// viewportLineCount returns how many newline-separated rows s occupies when
// rendered in a viewport (empty string → 0).
func viewportLineCount(s string) int {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// hardWrapOverflowLines splits lines that are still wider than maxCols after
// ansi.Wrap (e.g. long URLs or unbroken tokens). Skips lines containing
// bubblezone markers — those rows must be pre-sized before zone.Mark.
func hardWrapOverflowLines(s string, maxCols int) string {
	if maxCols < 8 {
		maxCols = 8
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if bubbleZoneCSI.MatchString(line) {
			b.WriteString(line)
			continue
		}
		if ansi.StringWidth(line) <= maxCols {
			b.WriteString(line)
			continue
		}
		b.WriteString(ansi.Hardwrap(line, maxCols, false))
	}
	return b.String()
}

// wrapForViewport hard-wraps styled text to the viewport content width so the
// terminal does not soft-wrap (which breaks line-based scrolling and clipping).
func wrapForViewport(s string, contentCols int) string {
	if contentCols < 8 {
		contentCols = 8
	}
	if s == "" {
		return s
	}
	wrapped := ansi.Wrap(s, contentCols, " /.,;:[]{}()=_`\"'+|&")
	wrapped = hardWrapOverflowLines(wrapped, contentCols)
	return enforceMaxLineWidth(wrapped, contentCols)
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}

	header := m.renderHeader()
	body := m.renderBody()
	status := m.renderStatus()
	main := lipgloss.JoinVertical(lipgloss.Left, header, body, status)
	out := m.overlayStack.View(main, m.width, m.height)
	return zone.Scan(out)
}

func (m *Model) renderHeader() string {
	switch m.mode {
	case modeList:
		return headerBar.Width(m.width).Render("appr-ai-sal · review queue")
	case modeDetail:
		t := "appr-ai-sal · detail"
		if m.currentPR != nil {
			t = fmt.Sprintf("appr-ai-sal · %s#%d  %s", m.currentPR.Repository, m.currentPR.Number, m.currentPR.Title)
		}
		return headerBar.Width(m.width).Render(ansi.Truncate(t, m.width-2, "…"))
	case modeURLInput:
		return headerBar.Width(m.width).Render("appr-ai-sal · paste a PR URL")
	case modeSettings:
		return headerBar.Width(m.width).Render("appr-ai-sal · settings")
	case modeRepoAgents:
		return headerBar.Width(m.width).Render("appr-ai-sal · repo agents")
	case modeLangAgents:
		return headerBar.Width(m.width).Render("appr-ai-sal · language experts")
	}
	return ""
}

// renderDetailMiniHeader is a one-line strip above the detail body that shows
// PR meta, diff stats, and quick chips for description / approval reopen.
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
		dimStyle.Render(fmt.Sprintf("%d file(s)", files)),
		fmt.Sprintf("%s/%s",
			okStyle.Render(fmt.Sprintf("+%d", totalA)),
			errStyle.Render(fmt.Sprintf("-%d", totalD))),
	}
	if badge := reviewStateBadge(m.currentPR.ReviewState); badge != "" {
		parts = append(parts, badge)
	}
	if hint := viewerActionBadge(m.currentPR.ReviewState); hint != "" {
		parts = append(parts, hint)
	}
	desc := zone.Mark(ZoneDescriptionToggle, dimStyle.Render(" description (g) "))
	if m.descriptionOpen {
		desc = zone.Mark(ZoneDescriptionToggle, boldStyle.Render(" description (g) "))
	}
	parts = append(parts, desc)
	parts = append(parts, zone.Mark(ZoneBuildRepoAgents, m.buildRepoAgentsChip()))
	parts = append(parts, zone.Mark(ZoneBuildLangAgents, m.buildLangAgentsChip()))
	if strings.TrimSpace(m.currentPR.URL) != "" {
		parts = append(parts, zone.Mark(ZoneOpenInBrowser, dimStyle.Render(" open in browser (O) ")))
	}
	if m.draft != nil {
		parts = append(parts, zone.Mark(ZoneReopenApproval, okStyle.Render(" reopen approval (a) ")))
	}
	line := strings.Join(parts, "  ·  ")
	return appPadding.Render(line)
}

func (m *Model) renderBody() string {
	bodyH := m.chromeBodyHeight()
	switch m.mode {
	case modeList:
		if !m.prsLoaded {
			return appPadding.Render(m.spinner.View() + " loading PRs from GitHub…")
		}
		filterLine := renderFilterLine(m.explicitReviewerOnly)
		return lipgloss.JoinVertical(lipgloss.Left, filterLine, appPadding.Render(m.list.View()))
	case modeDetail:
		return m.renderPRDetailBody(bodyH)
	case modeSettings:
		if m.settings == nil {
			return appPadding.Render("settings unavailable")
		}
		return m.settings.View()
	case modeRepoAgents:
		if m.repoAgents == nil {
			return appPadding.Render("repo agents unavailable")
		}
		return m.repoAgents.View()
	case modeLangAgents:
		if m.langAgents == nil {
			return appPadding.Render("language experts unavailable")
		}
		return m.langAgents.View()
	case modeURLInput:
		return appPadding.Render("\n  Enter PR URL or owner/repo#N:\n\n  " + m.urlInput.View() + "\n\n  " + dimStyle.Render("(esc to cancel)"))
	}
	return ""
}

func (m *Model) renderPRDetailBody(bodyH int) string {
	mini := m.renderDetailMiniHeader()
	miniH := lipgloss.Height(mini)
	paneH := bodyH - miniH

	if m.diffOnly {
		framed := m.framePane("Diff (full width — d to restore)", &m.diffView, m.width, paneH, paneFocusFor(paneDiff, m.focusedPane), ZonePaneDiffBody)
		framed = zone.Mark(ZonePaneDiff, framed)
		return lipgloss.JoinVertical(lipgloss.Left, mini, framed)
	}

	phs := prDetailPanel.GetHorizontalFrameSize()
	treeOuter := m.treeView.Width + phs
	diffOuter := m.width - treeOuter

	tree := m.framePane("Files · "+focusHint(paneTree, m.focusedPane), &m.treeView, treeOuter, paneH, paneFocusFor(paneTree, m.focusedPane), ZonePaneTreeBody)
	tree = zone.Mark(ZonePaneTree, tree)

	diff := m.framePane(m.diffPaneTitle(), &m.diffView, diffOuter, paneH, paneFocusFor(paneDiff, m.focusedPane), ZonePaneDiffBody)
	diff = zone.Mark(ZonePaneDiff, diff)

	row := lipgloss.JoinHorizontal(lipgloss.Top, tree, diff)
	return lipgloss.JoinVertical(lipgloss.Left, mini, row)
}

func paneFocusFor(p, focused pane) bool { return p == focused }

func focusHint(p, focused pane) string {
	if p == focused {
		return boldStyle.Render("focused (tab to switch)")
	}
	return dimStyle.Render("tab")
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
	rendered := detailPaneTitleStyle.Width(innerW).Render(titleOneLine)
	if focused {
		rendered = detailPaneTitleStyle.Bold(true).Width(innerW).Render(titleOneLine)
	}
	return lipgloss.Height(rendered)
}

func (m *Model) framePane(title string, vp *viewport.Model, outerW, outerH int, focused bool, viewportZone string) string {
	innerW := max(1, outerW-prDetailPanel.GetHorizontalFrameSize())
	titleOneLine := ansi.Truncate(title, innerW, "…")
	rendered := detailPaneTitleStyle.Width(innerW).Render(titleOneLine)
	if focused {
		rendered = detailPaneTitleStyle.Bold(true).Width(innerW).Render(titleOneLine)
	}
	vpStr := vp.View()
	if viewportZone != "" {
		vpStr = zone.Mark(viewportZone, vpStr)
	}
	col := lipgloss.JoinVertical(lipgloss.Left, rendered, vpStr)
	// Height alone can still grow past outerH with borders + title + viewport;
	// MaxHeight clips so the detail row never exceeds the chrome body budget.
	return prDetailPanel.Width(outerW).MaxWidth(outerW).Height(outerH).MaxHeight(outerH).Render(col)
}

func renderFilterLine(explicit bool) string {
	label := "filter: teams+you"
	if explicit {
		label = "filter: explicit reviewer only"
	}
	return appPadding.Render(zone.Mark(ZoneFilterToggle, boldStyle.Render("  "+label+"  (click or f)  ")))
}

func (m *Model) renderStatus() string {
	dry := ""
	if m.opts.DryRun {
		dry = " · " + errStyle.Render("DRY-RUN")
	}
	var hint string
	switch m.mode {
	case modeList:
		owner, repo := m.repoAgentsFreshnessForListSelection()
		lOwner, lRepo, lNum := m.listSelectionForLangFreshness()
		hint = "↑/↓ · click · double-click open · enter · u URL · O browser · o/, settings · ctrl+g repo ctx · ctrl+r repo agents · " +
			m.renderBuildLangAgentsHint(lOwner, lRepo, lNum) +
			" · " + m.renderBuildAgentsHint(owner, repo) +
			" · / filter · f · R · q quit" + dry
	case modeDetail:
		owner, repo, number := "", "", 0
		if m.currentPR != nil {
			owner, repo, number = m.currentPR.Owner, m.currentPR.Repo, m.currentPR.Number
		}
		hint = "tab pane · j/k nav · r review · a reopen approval · O browser · g description · d diff-only · P bulk · ctrl+r repo agents · " +
			m.renderBuildLangAgentsHint(owner, repo, number) +
			" · " + m.renderBuildAgentsHint(owner, repo) +
			" · ctrl+d/u · esc back" + dry
	case modeSettings:
		hint = "[ ] tabs · ctrl+s save · esc · tab fields · ↑/↓ strictness · wheel · o AI · , review · ctrl+g repo tab · ctrl+c quit" + dry
	case modeRepoAgents:
		hint = "←/→ repo · a add repo · A regen all · click chips · esc close · ctrl+s save edit · ctrl+c quit" + dry
	case modeLangAgents:
		hint = "↑/↓ select · g/r generate or regenerate · d delete cached · esc close · ctrl+c quit" + dry
	case modeURLInput:
		hint = "enter submit · esc cancel" + dry
	}
	return statusBar.Width(m.width).Render(hint)
}

func humanSince(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// truncWidth truncates by byte length (not terminal cells). Safe only for plain
// ASCII; strings with ANSI or wide runes need ansi.Truncate.
func truncWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if len(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return s[:w-1] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *Model) openSettings(start settings.StartSection) tea.Cmd {
	m.settingsPrevMode = m.mode
	m.mode = modeSettings
	m.settings = settings.New(settings.Opts{
		Cfg:          m.opts.AIConfig,
		Width:        m.width,
		BodyHeight:   m.chromeBodyHeight(),
		StartSection: start,
	})
	return m.settings.Init()
}

// openRepoAgents seeds the repo-agents tab with repos derived from the
// currently-loaded PR list (plus any saved on disk) and switches into the
// new mode. Pass review.Complete and gh.BuildReviewHistoryDigest as the LLM
// hooks; the subpackage only depends on those callables, not on the rest of
// the review package.
//
// focusRepo (lowercased owner/repo) is selected at startup when non-empty
// — typically the repo for the PR the user is viewing, so ctrl+r from
// detail lands on the right row instead of the alphabetical first one.
//
// When autoRegen is true and focusRepo is set, "Regenerate all" fires
// immediately. That's the path bound to ctrl+b ("build agents") so the user
// gets straight from key press to running LLM jobs.
func (m *Model) openRepoAgents(focusRepo string, autoRegen bool) tea.Cmd {
	m.repoAgentsPrevMode = m.mode
	m.mode = modeRepoAgents
	// We're about to let the user edit / regenerate; invalidate eagerly so
	// the freshness chip reflects the new state the moment they return.
	m.invalidateRepoAgentsFreshness()
	rc, _ := repoconfig.Load()
	if rc == nil {
		rc = repoconfig.Default()
	}
	repoconfig.ApplyParallelExecutionEnv(rc)

	seeds := m.repoSeedsFromList(rc)
	if m.currentPR != nil {
		seeds = append(seeds, m.currentPR.Owner+"/"+m.currentPR.Repo)
	}

	pathHistoryFetcher := func(ctx context.Context, owner, repo string) (string, error) {
		rows, err := review.LoadOrFetchPathHistory(ctx, rc, owner, repo, nil, false)
		if err != nil {
			return "", err
		}
		return review.FormatPathHistoryAggregate(review.AggregatePathHistory(rows)), nil
	}

	m.repoAgents = repoagentstui.New(repoagentstui.Opts{
		AICfg:        m.opts.AIConfig,
		RC:           rc,
		Width:        m.width,
		BodyHeight:   m.chromeBodyHeight(),
		Complete:     repoagentsstore.CompleteFunc(review.Complete),
		History:      repoagentsstore.HistoryFetcher(gh.BuildReviewHistoryDigest),
		PathHistory:  repoagentsstore.PathHistoryFetcher(pathHistoryFetcher),
		InitialRepos: seeds,
		FocusRepo:    strings.ToLower(strings.TrimSpace(focusRepo)),
		AutoRegenAll: autoRegen,
	})
	return m.repoAgents.Init()
}

// openLangAgents opens the language-experts tab. From detail mode (a
// PR is loaded with a parsed diff) the tab is scoped to ONLY the
// languages that PR touches, so generation flows are anchored to "I
// need a brief because this PR uses it." From list mode the tab opens
// unscoped, showing cached briefs only; the user is expected to drill
// into a PR to discover and generate new languages.
//
// Language briefs themselves are user-global — generating Swift from
// PR #1234 makes Swift available to every subsequent review across
// every repo.
func (m *Model) openLangAgents() tea.Cmd {
	m.langAgentsPrevMode = m.mode
	m.mode = modeLangAgents
	opts := langagentstui.Opts{
		AICfg:      m.opts.AIConfig,
		Width:      m.width,
		BodyHeight: m.chromeBodyHeight(),
		Complete:   langagentsstore.CompleteFunc(review.Complete),
	}
	if m.langAgentsPrevMode == modeDetail && len(m.parsedDiff) > 0 {
		// Use a non-nil slice (even when empty) to opt into scoped
		// rendering — the tab's header tells the user we noticed the
		// PR even when no rows match.
		opts.PRLanguages = languagesForFileDiffs(m.parsedDiff)
		if m.currentPR != nil {
			opts.PRLabel = fmt.Sprintf("%s#%d", m.currentPR.Repository, m.currentPR.Number)
		}
	}
	m.langAgents = langagentstui.New(opts).(*langagentstui.Model)
	return m.langAgents.Init()
}

// languagesForFileDiffs returns the canonical language names touched
// by a parsed diff, sorted by descending touch count (sum of added +
// deleted lines per language). Used to scope the language-experts
// tab to the dominant-first language set the PR exercises.
func languagesForFileDiffs(files []review.FileDiff) []langagentsstore.Language {
	if len(files) == 0 {
		return []langagentsstore.Language{}
	}
	touches := map[langagentsstore.Language]int{}
	for _, f := range files {
		c := langagentsstore.LanguageForPath(f.Path)
		if c == "" {
			continue
		}
		touches[c] += f.Additions + f.Deletions
	}
	out := make([]langagentsstore.Language, 0, len(touches))
	for l := range touches {
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if touches[out[i]] != touches[out[j]] {
			return touches[out[i]] > touches[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// openRepoAgentsForCurrentPR is the convenience wrapper bound to the
// "Build/refresh repo agents" action available on the PR detail view. It
// pre-focuses the tab on the PR's owner/repo and (when autoRegen is true)
// kicks off "Regenerate all" so a single key press takes the user from
// detail to "all 5 specialist agents are being rebuilt for this repo".
func (m *Model) openRepoAgentsForCurrentPR(autoRegen bool) tea.Cmd {
	focus := ""
	if m.currentPR != nil {
		focus = m.currentPR.Owner + "/" + m.currentPR.Repo
	}
	return m.openRepoAgents(focus, autoRegen)
}

// repoAgentsFreshness returns a TTL-cached freshness reading for the given
// owner/repo so the chip and status hint can colour themselves cheaply on
// every render. Empty owner or repo returns FreshnessUnknown so the caller
// renders neutrally.
func (m *Model) repoAgentsFreshness(owner, repo string) repoagentsstore.Freshness {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = strings.ToLower(strings.TrimSpace(repo))
	if owner == "" || repo == "" {
		return repoagentsstore.FreshnessUnknown
	}
	key := owner + "/" + repo
	now := time.Now()
	if e, ok := m.repoAgentsFreshnessCache[key]; ok {
		if now.Sub(e.computed) < repoAgentsFreshnessCacheTTL {
			return e.state
		}
	}
	state := repoagentsstore.LoadFreshness(owner, repo, now, repoagentsstore.DefaultStaleAfter)
	if m.repoAgentsFreshnessCache == nil {
		m.repoAgentsFreshnessCache = map[string]repoAgentsFreshnessEntry{}
	}
	m.repoAgentsFreshnessCache[key] = repoAgentsFreshnessEntry{state: state, computed: now}
	return state
}

// invalidateRepoAgentsFreshness drops cached freshness entries so the next
// render re-reads from disk. Called when the repo-agents tab returns (any
// repo could have been regenerated) and proactively when the user opens the
// tab (so the post-edit reading is fresh on return).
func (m *Model) invalidateRepoAgentsFreshness() {
	m.repoAgentsFreshnessCache = nil
}

// prKey is the cache key for prLanguages / langAgentsFreshnessCache.
// We use owner+repo+number rather than just number so two PRs with the
// same number in different repos can't collide.
func prKey(owner, repo string, number int) string {
	return strings.ToLower(strings.TrimSpace(owner)) + "/" + strings.ToLower(strings.TrimSpace(repo)) + "#" + strconv.Itoa(number)
}

// recordPRLanguages stores the canonical touched-language set for a
// PR. Called from the detail-mode loaders right after ParseDiff so
// list-mode rendering of the same PR knows what's touched without
// re-fetching anything. Called with a nil/empty parsedDiff is a no-op.
func (m *Model) recordPRLanguages(pr *gh.PR, parsed []review.FileDiff) {
	if pr == nil {
		return
	}
	if m.prLanguages == nil {
		m.prLanguages = map[string][]langagentsstore.Language{}
	}
	// Use an empty slice (not nil) to mark "we parsed; nothing
	// recognised" so the freshness computer returns FreshnessFresh
	// rather than FreshnessUnknown.
	m.prLanguages[prKey(pr.Owner, pr.Repo, pr.Number)] = languagesForFileDiffs(parsed)
	// Any change to a PR's touched set invalidates the cached
	// freshness reading for that PR (and is cheap to recompute on
	// next render).
	delete(m.langAgentsFreshnessCache, prKey(pr.Owner, pr.Repo, pr.Number))
}

// langAgentsFreshness returns the PR-aggregated freshness reading
// for a (owner, repo, number) triple. Returns FreshnessUnknown when
// we have no record of the PR's touched languages — typically a list
// row the user hasn't drilled into this session. Callers should
// render neutrally on Unknown rather than warn (no signal == no nag).
//
// TTL'd via langAgentsFreshnessCache so the renderer doesn't re-read
// disk on every frame; invalidated wholesale by
// invalidateLangAgentsFreshness when the lang-agents tab returns.
func (m *Model) langAgentsFreshness(owner, repo string, number int) langagentsstore.Freshness {
	if number == 0 || strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return langagentsstore.FreshnessUnknown
	}
	key := prKey(owner, repo, number)
	touched, known := m.prLanguages[key]
	if !known {
		return langagentsstore.FreshnessUnknown
	}
	now := time.Now()
	if e, ok := m.langAgentsFreshnessCache[key]; ok {
		if now.Sub(e.computed) < langAgentsFreshnessCacheTTL {
			return e.state
		}
	}
	cache, _ := langagentsstore.LoadCache()
	state := langagentsstore.ComputePR(touched, cache, now, langagentsstore.DefaultStaleAfter)
	if m.langAgentsFreshnessCache == nil {
		m.langAgentsFreshnessCache = map[string]langAgentsFreshnessEntry{}
	}
	m.langAgentsFreshnessCache[key] = langAgentsFreshnessEntry{state: state, computed: now}
	return state
}

// invalidateLangAgentsFreshness drops the cached PR-freshness
// readings so the next render recomputes from disk. Called when the
// user closes the lang-agents tab so any brief they generated or
// deleted flips the chip colour on the surrounding views immediately.
func (m *Model) invalidateLangAgentsFreshness() {
	m.langAgentsFreshnessCache = nil
}

// buildRepoAgentsChip is the right-side chip in the PR detail mini-header.
// It always has the same key affordance ("build repo agents (ctrl+b)") but
// gains a warning state suffix and a louder colour when this PR's repo has
// no agents, partial agents, or aging agents — so the reviewer sees at a
// glance whether the next review will get rich repo-aware context.
func (m *Model) buildRepoAgentsChip() string {
	if m.currentPR == nil {
		return dimStyle.Render(" build repo agents (ctrl+b) ")
	}
	state := m.repoAgentsFreshness(m.currentPR.Owner, m.currentPR.Repo)
	switch state {
	case repoagentsstore.FreshnessMissing:
		return errStyle.Render(" build repo agents (ctrl+b) — missing ")
	case repoagentsstore.FreshnessIncomplete:
		return warnStyle.Render(" build repo agents (ctrl+b) — partial ")
	case repoagentsstore.FreshnessStale:
		return warnStyle.Render(" build repo agents (ctrl+b) — stale ")
	default:
		return dimStyle.Render(" build repo agents (ctrl+b) ")
	}
}

// renderBuildAgentsHint returns the styled "ctrl+b build agents" segment
// for the bottom status bar. When the freshness for the supplied repo
// needs attention, the segment is coloured (red for missing, yellow for
// partial / stale) and gets a short state suffix; otherwise it returns the
// plain label that the surrounding statusBar style renders dim with the
// rest of the hints.
func (m *Model) renderBuildAgentsHint(owner, repo string) string {
	const label = "ctrl+b build agents"
	state := m.repoAgentsFreshness(owner, repo)
	switch state {
	case repoagentsstore.FreshnessMissing:
		return errStyle.Render(label + " (missing!)")
	case repoagentsstore.FreshnessIncomplete:
		return warnStyle.Render(label + " (partial)")
	case repoagentsstore.FreshnessStale:
		return warnStyle.Render(label + " (stale)")
	default:
		return label
	}
}

// repoAgentsFreshnessForListSelection returns the freshness state for the
// currently-highlighted PR in the list, or FreshnessUnknown if there isn't
// one (loading, empty filter result, etc.).
func (m *Model) repoAgentsFreshnessForListSelection() (owner, repo string) {
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return "", ""
	}
	return it.pr.Owner, it.pr.Repo
}

// listSelectionForLangFreshness is the lang-agents twin of
// repoAgentsFreshnessForListSelection. Returns owner/repo/number for
// the highlighted PR so renderBuildLangAgentsHint can colour itself.
// Empty triple means "no selection" (or fresh load) and the caller
// renders neutrally.
func (m *Model) listSelectionForLangFreshness() (owner, repo string, number int) {
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return "", "", 0
	}
	return it.pr.Owner, it.pr.Repo, it.pr.Number
}

// renderBuildLangAgentsHint is the lang-agents twin of
// renderBuildAgentsHint. Same colouring rules — red for missing, yellow
// for stale, plain otherwise — but driven by a per-PR aggregator instead
// of a per-repo one. The hint also stays neutral when we have no record
// of the PR's languages, which is the common case for un-visited list
// rows: showing a warning we can't ground would be more noisy than
// helpful.
func (m *Model) renderBuildLangAgentsHint(owner, repo string, number int) string {
	const label = "ctrl+l lang experts"
	state := m.langAgentsFreshness(owner, repo, number)
	switch state {
	case langagentsstore.FreshnessMissing:
		return errStyle.Render(label + " (missing!)")
	case langagentsstore.FreshnessStale:
		return warnStyle.Render(label + " (stale)")
	default:
		return label
	}
}

// buildLangAgentsChip is the lang-agents twin of buildRepoAgentsChip.
// Pinned to the right side of the PR detail mini-header so the reviewer
// sees a "this PR has a language with no expert" warning the moment
// they open the PR, not just when they glance at the bottom status bar.
func (m *Model) buildLangAgentsChip() string {
	if m.currentPR == nil {
		return dimStyle.Render(" build lang experts (ctrl+l) ")
	}
	state := m.langAgentsFreshness(m.currentPR.Owner, m.currentPR.Repo, m.currentPR.Number)
	switch state {
	case langagentsstore.FreshnessMissing:
		return errStyle.Render(" build lang experts (ctrl+l) — missing ")
	case langagentsstore.FreshnessStale:
		return warnStyle.Render(" build lang experts (ctrl+l) — stale ")
	default:
		return dimStyle.Render(" build lang experts (ctrl+l) ")
	}
}

func (m *Model) repoSeedsFromList(_ *repoconfig.Config) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, it := range m.list.Items() {
		pi, ok := it.(prItem)
		if !ok {
			continue
		}
		k := strings.ToLower(pi.pr.Owner + "/" + pi.pr.Repo)
		if k == "/" {
			continue
		}
		if _, exists := seen[k]; exists {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// chromeBodyHeight is the vertical space between header and status bars.
func (m *Model) chromeBodyHeight() int {
	return max(1, m.height-lipgloss.Height(m.renderHeader())-lipgloss.Height(m.renderStatus()))
}

func (m *Model) mouseYInChromeBody(msg tea.MouseMsg) bool {
	top := lipgloss.Height(m.renderHeader())
	bottom := m.height - lipgloss.Height(m.renderStatus())
	return msg.Y >= top && msg.Y < bottom
}

// wheelScrollViewport applies a wheel event to a single viewport (mouse is
// routed by the parent so panes never share one wheel tick).
func wheelScrollViewport(vp *viewport.Model, msg tea.MouseMsg) {
	delta := vp.MouseWheelDelta
	if delta < 1 {
		delta = 3
	}
	const hStep = 4
	switch msg.Button { //nolint:exhaustive
	case tea.MouseButtonWheelUp:
		if msg.Shift {
			vp.ScrollLeft(hStep)
		} else {
			vp.ScrollUp(delta)
		}
	case tea.MouseButtonWheelDown:
		if msg.Shift {
			vp.ScrollRight(hStep)
		} else {
			vp.ScrollDown(delta)
		}
	case tea.MouseButtonWheelLeft:
		vp.ScrollLeft(hStep)
	case tea.MouseButtonWheelRight:
		vp.ScrollRight(hStep)
	}
}
