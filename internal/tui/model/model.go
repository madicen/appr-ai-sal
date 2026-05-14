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
	modeURLInput
	modeSettings
	modeRepoAgents
	modeLangAgents
)

// treePaneWidth is the fixed width allocated to the file-tree pane content
// (frame is added on top by the panel border).
const treePaneWidth = 30

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
	return tea.Batch(data.LoadPRsCmd(m.explicitReviewerOnly), m.spinner.Tick)
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

	case state.NavigateMsg:
		return m.handleNavigate(msg.Target)

	case overlays.PostedOverlayDismissMsg:
		_, c := m.overlayStack.Pop()
		m.mode = modeList
		m.draft = nil
		return m, tea.Batch(c, data.LoadPRsCmd(m.explicitReviewerOnly))

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
		ordered := sortPRsByActionability(msg.PRs)
		items := make([]list.Item, 0, len(ordered))
		for _, p := range ordered {
			items = append(items, prItem{pr: p})
		}
		m.list.SetItems(items)
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
