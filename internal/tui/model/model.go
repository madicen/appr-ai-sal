// Package model hosts the root Bubble Tea model that wires the list /
// detail / review / settings / repo-agents / lang-agents tabs together.
// The public facade lives in internal/tui (a thin re-export shim); only
// cmd/appr-ai-sal/main.go imports that. Other packages may not import
// this one directly.
package model

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	bubbledropdown "github.com/madicen/bubble-dropdown"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
	langagentstui "github.com/madicen/appr-ai-sal/internal/tui/tabs/langagents"
	repoagentstui "github.com/madicen/appr-ai-sal/internal/tui/tabs/repoagents"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// handleNavigate is the single dispatch point for cross-tab transitions.
// Tabs emit state.NavigateMsg{Target: ...}; root unpacks the target here
// and decides what to mutate (mode, tab pointer, freshness caches, etc.).
//
// The NavBack arm is mode-aware: closing settings restores AIConfig and
// triggers refreshDetailViews; closing repoagents/langagents drops the
// matching freshness cache. Future kinds (NavToDetail, NavParseURL, …)
func (m *Model) handleNavigate(t state.NavigateTarget) (tea.Model, tea.Cmd) {
	switch t.Kind {
	case state.NavBack:
		switch m.mode {
		case modeSettings:
			m.mode = m.settingsPrevMode
			m.settings = nil
			if t.Cancelled {
				return m, nil
			}
			if t.Err != nil {
				em := overlays.NewErrorOverlay(t.Err.Error(), max(40, m.width-6), max(8, m.height-8))
				cfg := overlay.DefaultOverlayConfig()
				return m, tea.Batch(
					m.overlayStack.Push(em, cfg),
					func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
				)
			}
			if t.Cfg != nil {
				m.opts.AIConfig = t.Cfg.Clone()
			}
			m.relayout()
			if m.mode == modeDetail {
				m.refreshDetailViews()
			}
			return m, nil

		case modeRepoAgents:
			m.mode = m.repoAgentsPrevMode
			m.repoAgents = nil
			// Any specialist for any repo could have been regenerated, added,
			// or deleted while the tab was open; the safest invalidation is
			// to drop the whole cache so the chip / status hint re-read on
			// the next render.
			m.invalidateRepoAgentsFreshness()
			m.relayout()
			if t.Err != nil {
				em := overlays.NewErrorOverlay(t.Err.Error(), max(40, m.width-6), max(8, m.height-8))
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

		case modeLangAgents:
			m.mode = m.langAgentsPrevMode
			m.langAgents = nil
			m.invalidateLangAgentsFreshness()
			m.relayout()
			if t.Err != nil {
				em := overlays.NewErrorOverlay(t.Err.Error(), max(40, m.width-6), max(8, m.height-8))
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
		}
	}
	return m, nil
}

// FlushMouse is re-exported from internal/tui/util so the cmd entry point
// (which calls tui.FlushMouse on quit) keeps working through the package
// restructure without churning main.go.
var FlushMouse = util.FlushMouse

type mode int

const (
	modeList mode = iota
	modeDetail
	modeSettings
	modeRepoAgents
	modeLangAgents
)

// filterMode names the PR-list filter chip the user has selected. The
// values are also the cycle order for the `f` keybinding (and the chip
// rendering order on screen).
type filterMode int

const (
	// filterReviewTeams ("teams+you") is the default landing filter —
	// PRs where you're the requested reviewer either directly or via
	// a team request.
	filterReviewTeams filterMode = iota
	// filterReviewExplicit narrows filterReviewTeams to PRs where your
	// login is the direct requestee (drops team-only requests).
	filterReviewExplicit
	// filterAuthored returns PRs you authored (author:@me) — the "my
	// PRs" chip on the top panel.
	filterAuthored
)

// nextFilterMode is the cycle order for the `f` keybinding and the
// rotating filter chip click handler. Sweeps through every filterMode
// value exactly once and wraps around.
func nextFilterMode(f filterMode) filterMode {
	switch f {
	case filterReviewTeams:
		return filterReviewExplicit
	case filterReviewExplicit:
		return filterAuthored
	default:
		return filterReviewTeams
	}
}

// listFocus tracks which inline panel widget is receiving keystrokes
// while the user is in modeList. focusList means the bubbles/list
// itself; focusSearch / focusURL route keys into the matching text
// input until the user blurs (esc, tab away, or enter on URL submit).
type listFocus int

const (
	focusList listFocus = iota
	focusSearch
	focusURL
)

// defaultTreePaneWidth is the initial width allocated to the file-tree
// pane content (frame is added on top by the panel border). Stored on
// Model.treePaneWidth so the user can drag the tree/diff seam to resize.
const defaultTreePaneWidth = 30

// defaultControlsPaneWidth is the initial width of the right-hand
// "Review controls" pane content (frame is added on top). Stored on
// Model.controlsPaneWidth so the user can drag the diff/controls seam
// to resize. Auto-hidden in relayout when the terminal is too narrow
// to fit all three panes side by side.
const defaultControlsPaneWidth = 38

// minTreePaneWidth / minControlsPaneWidth bound how narrow the user
// can drag each pane. Below these the pane is too narrow to host its
// title strip and content meaningfully; the seam clamps instead of
// silently auto-hiding.
const (
	minTreePaneWidth     = 12
	minControlsPaneWidth = 16
)

// controlsAutoHideMinDiffWidth is the minimum diff outer width below
// which the controls pane is auto-hidden. Keeps the diff readable on
// narrow terminals; the user can re-show it with `c` once they have
// more screen real estate. Drags that would starve the diff below
// this threshold are clamped at the seam.
const controlsAutoHideMinDiffWidth = 36

// dividerTarget identifies which pane seam an active mouse drag is
// resizing. dividerNone means no drag is in flight.
type dividerTarget int

const (
	dividerNone dividerTarget = iota
	dividerTreeDiff
	dividerDiffControls
)

// paneDrag tracks an in-flight drag on one of the pane seams. Anchored
// at press time so motion events can compute the absolute width from
// the original (originX, originTreeW, originControlsW) rather than
// accumulating per-event deltas (which would amplify rounding error
// on terminals that batch motion reports).
type paneDrag struct {
	target          dividerTarget
	originX         int
	originTreeW     int
	originControlsW int
}

// prItem adapts a gh.PR for the bubbles/list component.
type Model struct {
	opts Options
	mode mode

	settings         *settings.Model
	settingsPrevMode mode

	repoAgents         *repoagentstui.Model
	repoAgentsPrevMode mode

	langAgents         *langagentstui.Model
	langAgentsPrevMode mode

	// controlsProfileDD is the AI-profile dropdown in the PR-detail
	// "Review controls" pane. It is positioned from the trigger's
	// bubblezone-scanned (absolute) coordinates, cached here so the panel
	// stays put while open even though the trigger sits under the overlay.
	controlsProfileDD    *bubbledropdown.Dropdown
	controlsProfileDDRow int
	controlsProfileDDCol int

	width  int
	height int

	list         list.Model
	prsLoaded    bool
	overlayStack overlay.OverlayStack
	overlayFocus overlay.FocusTrap

	// filter is the active top-panel chip; drives the GitHub query
	// LoadPRsCmd runs and which chip renders highlighted.
	filter filterMode

	// prsAll caches the most recent ordered PR slice from data.PRListMsg.
	// The bubbles list's visible items are derived from this slice plus
	// searchQuery so we can re-filter on every keystroke without
	// re-fetching from GitHub.
	prsAll []gh.PR

	// searchQuery is the live text in the inline search input. Used to
	// filter prsAll into the bubbles list (title / repo / author match).
	searchQuery string

	// searchInput is the always-visible search text input in the list
	// top panel. Focused / blurred via listFocus + the `/` keybinding.
	searchInput textinput.Model

	// listFocus selects which widget on the list screen consumes
	// keystrokes (the list itself, the search input, or the URL input).
	listFocus listFocus

	currentPR *gh.PR
	diff      string
	draft     *review.Draft

	// PR detail layout: tree + diff.
	parsedDiff       []review.FileDiff
	treeRows         []treeRow
	treeIdx          int // cursor row into treeViewRows (folders + files)
	focusedPane      pane
	selectedFilePath string
	diffOnly         bool

	// Tree view (hierarchical, with collapsible folders) — derived from
	// treeRows + collapsedFolders. treeIdx indexes treeViewRows so j/k
	// can land on folder rows and toggle them with space; files set
	// selectedFilePath while folder rows leave it sticky. Built by
	// buildTreeView; rebuilt on every recomputeTreeRows / collapse.
	treeViewRows     []treeViewRow
	treeFileToLine   []int // index into treeRows -> line index in treeViewRows
	treeLineToFile   []int // index into treeViewRows -> index into treeRows (-1 for folders)
	collapsedFolders map[string]bool

	// scrollToSelectedFile is set on j/k / file click / refresh so the
	// next refreshDetailViews scrolls the selected row into view; reset
	// after applying so wheel-scroll doesn't fight with cursor scroll.
	scrollToSelectedFile bool

	treeView     viewport.Model
	diffView     viewport.Model
	controlsView viewport.Model

	// treePaneWidth / controlsPaneWidth are the user-adjustable inner
	// widths for the left and right panes of the PR detail body. Seeded
	// from defaultTreePaneWidth / defaultControlsPaneWidth in New() and
	// mutated by the drag-resize handler in detail_resize.go. The diff
	// pane absorbs whatever's left over inside relayout().
	treePaneWidth     int
	controlsPaneWidth int

	// paneDrag carries the state of an in-flight seam drag. Zero value
	// (dividerNone) means no drag is active; press inside a seam arms
	// it, motion updates the corresponding pane width, release clears
	// it. See detail_resize.go.
	paneDrag paneDrag

	// controlsHidden is true when the right-hand "Review controls" pane
	// is hidden — either because the terminal is too narrow to host all
	// three panes (set automatically in relayout) or because the user
	// pressed `c` to collapse it.
	controlsHidden     bool
	controlsUserHidden bool

	// startReviewMinimized toggles the "Start minimized" preference
	// for the next review run kicked from the controls panel. When
	// true the review overlay opens collapsed to its tab strip so the
	// PR detail view stays fully visible; reset when a review starts.
	startReviewMinimized bool

	// treeScrollLines is the line count of tree viewport content after the last
	// refresh (used for mouse row mapping; must match visible wrapped lines).
	treeScrollLines int

	// centerView selects which content the centre pane shows. centerDiff
	// (the default) restores the historical "tree-driven diff" behaviour;
	// centerDescription / centerChecks / centerDiscussion replace the diff
	// with the corresponding overview content. Driven by clicks on the new
	// PR-overview selector at the top of the left column and by the `g`
	// shortcut. While diffOnly is active centerView is overridden to
	// centerDiff so the full-width diff pane stays consistent.
	centerView centerView

	// checks / discussion are populated lazily when the user first lands on
	// their respective overview rows. Loading flips while the gh fetch is
	// in flight; *Err sticks until the user retries so the renderer can
	// show a retry chip. Cleared whenever a fresh PR is loaded so we don't
	// leak the previous PR's data into the new context.
	checks            *gh.ChecksReport
	checksLoading     bool
	checksErr         error
	discussion        []gh.DiscussionEvent
	discussionLoading bool
	discussionErr     error

	urlInput textinput.Model

	spinner    spinner.Model
	progressCh <-chan review.Progress

	err error

	// Active review overlay reference (for direct state pokes when posting).
	currentReviewOverlay *reviewtab.Model

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

func repoParallelExecutionFlags() (specialistsParallel, repoExpertsParallel, prAgentsParallel bool) {
	rc, err := repoconfig.Load()
	if err != nil || rc == nil {
		return false, false, false
	}
	repoconfig.ApplyParallelExecutionEnv(rc)
	return rc.ParallelSpecialists, rc.ParallelRepoExperts, rc.ParallelPRAgents
}

// techExpertsConfigured reports whether the current PR's repo has usable
// technology-expert briefs. It drives whether the tech specialist (and its
// overlay tab) is surfaced for this run; a load error or missing config
// resolves to false (no tech specialist), matching the runner's own gating.
func (m *Model) techExpertsConfigured() bool {
	if m.currentPR == nil {
		return false
	}
	rc, err := repoconfig.Load()
	if err != nil {
		rc = nil
	}
	return review.HasUsableTechExperts(m.currentPR, rc)
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
	// Filtering is owned by the top panel's inline search input now; the
	// bubbles built-in `/` overlay would fight with it.
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	// The panel draws its own "▎ " gutter glyph beside each input, so
	// clear the textinput defaults to avoid a double-prompt ("▎ > …").
	// Leaving Prompt set also widens the rendered View() output by two
	// cells past Width, which used to push the panel's right border
	// off-screen.
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "https://github.com/owner/repo/pull/123  or  owner/repo#123"
	ti.CharLimit = 200
	ti.Width = 80

	si := textinput.New()
	si.Prompt = ""
	si.Placeholder = "filter by title, repo, or author"
	si.CharLimit = 200
	si.Width = 40

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	if opts.AIConfig == nil {
		opts.AIConfig = aiconfig.DefaultConfig()
	}

	tv := viewport.New(0, 0)
	dv := viewport.New(0, 0)
	cv := viewport.New(0, 0)
	for _, vp := range []*viewport.Model{&tv, &dv, &cv} {
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
		searchInput:        si,
		spinner:            sp,
		treeView:           tv,
		diffView:           dv,
		controlsView:       cv,
		focusedPane:        paneTree,
		listDoubleClickWin: 500 * time.Millisecond,
		treePaneWidth:      defaultTreePaneWidth,
		controlsPaneWidth:  defaultControlsPaneWidth,
	}
	m.overlayFocus.Stack = &m.overlayStack
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(data.LoadPRsCmd(m.listMode(), m.opts.Demo), m.spinner.Tick)
}

// listMode maps the active filter chip onto the gh.ListMode the data
// loader runs. Kept beside Init so the LoadPRsCmd call sites and the
// chip rendering can share a single mapping.
func (m *Model) listMode() gh.ListMode {
	switch m.filter {
	case filterReviewExplicit:
		return gh.ListModeReviewExplicit
	case filterAuthored:
		return gh.ListModeAuthored
	default:
		return gh.ListModeReviewTeams
	}
}

// reviewOverlayOnTop returns the active review overlay if it sits at the top
// of the modal stack, otherwise nil.
func (m *Model) reviewOverlayOnTop() *reviewtab.Model {
	if top := m.overlayStack.Top(); top != nil {
		if ro, ok := top.(*reviewtab.Model); ok {
			return ro
		}
	}
	return nil
}

// shouldPassMouseToBackground decides whether a mouse event should be
// forwarded to handleMouse (the PR detail / list dispatcher) instead of
// the overlay stack while a review modal is open.
//
// Returns true only when ALL of:
//
//   - The user is on the PR detail page. We don't want clicks to leak
//     into the PR list while a review is mid-flight there — starting
//     a second review behind the first would be a mess. The detail
//     page is where it actually helps: the user can read the diff
//     while waiting for the AI to finish.
//   - The top overlay is the review modal. Other overlays (errors,
//     confirms, bulk-post prompts) are short-lived and demand the
//     user's full attention; passing clicks through them would defeat
//     their purpose.
//   - bubble-overlay's MouseTargetsTop says the coordinates land
//     outside the modal rect AND no chrome gesture (drag, resize) is
//     in progress. Clicks inside the modal — including the chrome's
//     tab strip, [x] button, and resize handles — keep routing
//     through the stack so every overlay-internal control still
//     works.
func (m *Model) shouldPassMouseToBackground(msg tea.MouseMsg) bool {
	if m.mode != modeDetail {
		return false
	}
	if m.reviewOverlayOnTop() == nil {
		return false
	}
	return !m.overlayStack.MouseTargetsTop(msg, m.width, m.height)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// PR-detail "Review controls" profile dropdown: while its panel is open
	// (and no modal overlay is active) it owns key/mouse input. ctrl+c still
	// quits. Result messages close it and apply the chosen profile.
	if m.mode == modeDetail && m.overlayStack.Top() == nil {
		switch typed := msg.(type) {
		case bubbledropdown.ItemChosenMsg, bubbledropdown.ItemCanceledMsg:
			return m, m.handleControlsProfileResult(msg)
		case tea.KeyMsg:
			if m.controlsProfileDropdownOpen() && typed.String() != "ctrl+c" {
				return m, m.forwardControlsProfileDropdown(msg)
			}
		case tea.MouseMsg:
			if m.controlsProfileDropdownOpen() {
				return m, m.forwardControlsProfileDropdown(msg)
			}
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		if m.mode == modeSettings && m.settings != nil {
			m.settings.Resize(m.width, m.chromeBodyHeight())
			m.settings.SetContentOrigin(m.headerHeight())
		}
		if m.mode == modeRepoAgents && m.repoAgents != nil {
			m.repoAgents.Resize(m.width, m.chromeBodyHeight())
			m.repoAgents.SetContentOrigin(m.headerHeight())
		}
		if m.mode == modeLangAgents && m.langAgents != nil {
			m.langAgents.Resize(m.width, m.chromeBodyHeight())
		}
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, m.overlayStack.Update(msg)

	case overlays.BulkPostAnswerMsg:
		_, popCmd := m.overlayStack.Pop()
		if msg.Confirm && m.draft != nil && m.draft.PR != nil {
			return m, tea.Sequence(popCmd, data.PostReviewCmd(m.draft.Ref, m.draft, m.opts.DryRun))
		}
		return m, popCmd

	case overlays.ErrorOverlayDismissMsg:
		_, c := m.overlayStack.Pop()
		m.err = nil
		return m, c

	case overlays.DryRunDismissMsg:
		_, c := m.overlayStack.Pop()
		return m, c

	case reviewtab.CloseMsg:
		_, c := m.overlayStack.Pop()
		m.currentReviewOverlay = nil
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, c

	case reviewOverlayMinimizeRequestMsg:
		return m, m.minimizeReviewOverlay()

	case state.NavigateMsg:
		return m.handleNavigate(msg.Target)

	case overlays.PostedOverlayDismissMsg:
		_, c := m.overlayStack.Pop()
		m.mode = modeList
		m.draft = nil
		// Same reasoning as the detail.go esc/q branch: re-sync the
		// bubbles list height now that we're back in list mode so
		// the panel's bubblezone bounds stay aligned with their
		// visible rows even when the post flow lengthened the
		// status hint.
		m.relayout()
		return m, tea.Batch(c, data.LoadPRsCmd(m.listMode(), m.opts.Demo))

	case data.DryRunPayloadMsg:
		// If the persistent review overlay is active, let it absorb the dry-run
		// receipt internally — we don't want to cover the approval flow with
		// another modal in that case.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.Update(msg)
			return m, nil
		}
		modal := overlays.NewDryRunOverlay(msg.Title, msg.Payload, max(40, m.width-6), max(12, m.height-6))
		cfg := overlay.DefaultOverlayConfig()
		return m, tea.Batch(
			m.overlayStack.Push(modal, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)

	case data.StagedFindingPostedMsg:
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

	case reviewtab.VibeCoachDoneMsg:
		// A TUI-triggered vibe-coach re-run (kicked off when the user
		// changed skips between approve and summary) completed. The
		// overlay's handler is the only thing that knows what to do
		// with the result; without an explicit case here the message
		// would fall through the root Update without ever reaching the
		// overlay, stranding it in phaseGeneratingSummary.
		//
		// The overlay's handler may re-issue against a fresher skip set
		// (returns m.enterSummary()), so forward the cmd.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil

	case data.PRListMsg:
		m.prsLoaded = true
		m.prsAll = sortPRsByActionability(msg.PRs)
		m.applySearchFilter()
		m.updateListTitle()
		m.resetListClickTracking()
		return m, nil

	case data.PRDetailMsg:
		m.resetListClickTracking()
		m.currentPR = msg.PR
		m.diff = msg.Diff
		m.draft = nil
		m.parsedDiff = review.ParseDiff(m.diff)
		m.recordPRLanguages(msg.PR, m.parsedDiff)
		// Loading a different PR shouldn't carry collapse state forward —
		// the folder paths from the previous PR are likely irrelevant.
		m.collapsedFolders = map[string]bool{}
		m.treeRows = buildTreeRows(m.parsedDiff, m.draft)
		m.treeIdx = 0
		m.diffOnly = false
		m.centerView = centerDiff
		m.resetOverviewData()
		m.focusedPane = paneTree
		m.selectedFilePath = ""
		if len(m.parsedDiff) > 0 {
			m.selectedFilePath = m.parsedDiff[0].Path
		}
		m.recomputeTreeView()
		m.scrollToSelectedFile = true
		m.mode = modeDetail
		m.refreshDetailViews()
		return m, nil

	case data.ChecksMsg:
		// Stale ref guard: ignore the result if the user has since opened
		// a different PR.
		if m.currentPR == nil || msg.Ref.Owner != m.currentPR.Owner ||
			msg.Ref.Repo != m.currentPR.Repo || msg.Ref.Number != m.currentPR.Number {
			return m, nil
		}
		m.checksLoading = false
		m.checksErr = msg.Err
		if msg.Err == nil {
			m.checks = msg.Report
		}
		m.refreshDetailViews()
		return m, nil

	case data.DiscussionMsg:
		if m.currentPR == nil || msg.Ref.Owner != m.currentPR.Owner ||
			msg.Ref.Repo != m.currentPR.Repo || msg.Ref.Number != m.currentPR.Number {
			return m, nil
		}
		m.discussionLoading = false
		m.discussionErr = msg.Err
		if msg.Err == nil {
			m.discussion = msg.Timeline
			if m.discussion == nil {
				m.discussion = []gh.DiscussionEvent{}
			}
		}
		m.refreshDetailViews()
		return m, nil

	case data.ReviewStartedMsg:
		m.progressCh = msg.Ch
		return m, data.WaitForProgressCmd(m.progressCh)

	case data.ProgressMsg:
		m.applyProgress(review.Progress(msg))
		cmd := data.WaitForProgressCmd(m.progressCh)
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, overlayCmd := ro.Update(msg)
			cmd = tea.Batch(cmd, overlayCmd)
		}
		return m, cmd

	case data.ExistingPRCommentsMsg:
		// The overlay's handler can return a markCardsAlreadyOnGitHub
		// cmd — forward it so the duplicate-detection pass actually runs
		// instead of stalling on "Checking GitHub for inline comments…".
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil

	case data.PRRefreshedMsg:
		// Update root state so the detail view's diff/PR head SHA stay in
		// sync with whatever the overlay just adopted (force-pushed commits,
		// renamed files, etc.).
		if msg.PR != nil {
			if m.currentPR != nil && m.currentPR.Number == msg.PR.Number && m.currentPR.Owner == msg.PR.Owner && m.currentPR.Repo == msg.PR.Repo {
				m.currentPR = msg.PR
				m.diff = msg.Diff
				m.parsedDiff = review.ParseDiff(m.diff)
				m.recordPRLanguages(msg.PR, m.parsedDiff)
				m.treeRows = buildTreeRows(m.parsedDiff, m.draft)
				m.recomputeTreeView()
				m.refreshDetailViews()
			}
			if m.draft != nil && m.draft.PR != nil && m.draft.PR.Number == msg.PR.Number {
				m.draft.PR = msg.PR
				m.draft.Diff = msg.Diff
			}
		}
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil

	case data.ReviewClosedMsg:
		m.progressCh = nil
		// Persistent overlay stays open through the approval flow; we no
		// longer auto-pop it here.
		if m.draft != nil {
			m.recomputeTreeRows()
			m.refreshDetailViews()
		}
		return m, nil

	case data.PostDoneMsg:
		// Either summary (from review overlay) or bulk (legacy P key) just
		// posted successfully.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.MarkSummaryPosted()
			return m, nil
		}
		m.stagedReset()
		m.mode = modeDetail
		m.refreshDetailViews()
		pm := overlays.PostedOverlay{}
		cfg := overlay.DefaultOverlayConfig()
		cfg.CloseOnClickOutside = false
		return m, tea.Batch(
			m.overlayStack.Push(pm, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)

	case util.BrowserOpenedMsg:
		if msg.Err == nil {
			return m, nil
		}
		// Surface launch failures (xdg-open missing, unsupported scheme,
		// etc.) through the standard error overlay so the user can see
		// what went wrong without us hijacking the more disruptive
		// data.ErrMsg pathway (which would also clobber review-overlay state).
		em := overlays.NewErrorOverlay(
			fmt.Sprintf("open in browser: %s\n\nURL: %s", msg.Err.Error(), msg.URL),
			max(40, m.width-6), max(8, m.height-8),
		)
		cfg := overlay.DefaultOverlayConfig()
		return m, tea.Batch(
			m.overlayStack.Push(em, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)

	case data.ErrMsg:
		m.err = msg.Err
		em := overlays.NewErrorOverlay(msg.Err.Error(), max(40, m.width-6), max(8, m.height-8))
		cfg := overlay.DefaultOverlayConfig()
		pushErr := tea.Batch(
			m.overlayStack.Push(em, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)
		// Review overlay: still record failure on the card / summary strip, but also
		// stack the dedicated error modal (copy button, scroll) like jj-tui — the
		// inline "✗ …" line is easy to miss and truncates long gh API payloads.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.MarkPostError(msg.Err)
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
		// Status-bar hint clicks are dispatched before mode/tab routing
		// so the always-present quit segment (and the list/detail hint
		// buttons) fire even when a tab otherwise owns the event stream.
		// Gated on InteractiveToBase so a click that belongs to an open
		// modal isn't stolen by the status bar behind it.
		if mm, ok := msg.(tea.MouseMsg); ok && m.overlayFocus.InteractiveToBase(msg) {
			if model, cmd, handled := m.handleStatusBarMouse(mm); handled {
				return model, cmd
			}
		}
		if m.mode == modeSettings && m.settings != nil {
			if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+c" {
				util.FlushMouse()
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
				util.FlushMouse()
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
				util.FlushMouse()
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
			// Pass-through: while the review overlay is open over the
			// PR detail page, mouse events that land outside the modal
			// are forwarded to handleMouse so the user can keep
			// browsing the file tree, scrolling diffs, etc. while the
			// AI review runs in the background. The chrome (drag tab,
			// resize handles, close button) and the modal body still
			// receive their own clicks via MouseTargetsTop returning
			// true. Keyboard input is intentionally NOT split — the
			// overlay still owns its keymap so esc / q / action keys
			// keep working exactly as today.
			if mm, ok := msg.(tea.MouseMsg); ok && m.shouldPassMouseToBackground(mm) {
				return m.handleMouse(mm)
			}
			return m, m.overlayStack.Update(msg)
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			util.FlushMouse()
			return m, tea.Quit
		}
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	if m.mode == modeSettings && m.settings != nil {
		// Forward any remaining message to the settings model so async
		// child-component messages (cursor blink, bubble-color-picker
		// ColorChangedMsg / ColorCanceledMsg, etc.) reach their owners.
		sm, cmd := m.settings.Update(msg)
		m.settings = sm.(*settings.Model)
		return m, cmd
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
		// In modeList we may have any of three focused widgets — the
		// list itself, the inline search input, or the inline URL
		// input. Async messages (cursor blink, etc.) are forwarded to
		// the matching widget so its caret keeps animating; keystrokes
		// went through handleListKey above.
		switch m.listFocus {
		case focusSearch:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		case focusURL:
			var cmd tea.Cmd
			m.urlInput, cmd = m.urlInput.Update(msg)
			return m, cmd
		default:
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
	case modeDetail:
		return m, nil
	}

	return m, nil
}

// stagedReset clears any pre-overlay-era staged-flow state. With the
// persistent overlay, this is mostly a no-op kept for safety.
func (m *Model) stagedReset() {}
