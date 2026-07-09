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
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	bubbledropdown "github.com/madicen/bubble-dropdown"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
)

// FlushMouse is re-exported from internal/tui/util so the cmd entry point
// (which calls tui.FlushMouse on quit) keeps working through the package
// restructure without churning main.go.
var FlushMouse = util.FlushMouse

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

	m := &Model{
		opts:               opts,
		mode:               modeList,
		keys:               keys.Default(),
		tabs:               map[mode]Tab{},
		list:               l,
		urlInput:           ti,
		searchInput:        si,
		spinner:            sp,
		listDoubleClickWin: 500 * time.Millisecond,
	}
	m.overlayFocus.Stack = &m.overlayStack
	m.palette = m.buildCommandRegistry()
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

// routeToActiveTab forwards msg to the Tab that owns the current mode
// (settings / repo-agents / lang-agents / detail). It is the single forwarding
// path — used for both key/mouse and async messages — that collapsed the
// two hand-written forwarding phases the root Update used to carry.
//
// Returns handled=false when modeList is active (root-native list widgets)
// or when the tab hasn't been constructed yet.
func (m *Model) routeToActiveTab(msg tea.Msg) (tea.Cmd, bool) {
	if m.mode == modeDetail {
		m.ensureDetailTab()
	}
	tab := m.tabs[m.mode]
	if tab == nil {
		return nil, false
	}
	// Detail mode: while a modal owns input (or pass-through mouse is
	// active), root Update must keep routing to the overlay stack /
	// handleMouse — not the tab adapter (see mouse_pass_through_test.go).
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		if m.mode == modeDetail && !m.overlayFocus.InteractiveToBase(msg) {
			return nil, false
		}
	}
	// ctrl+c always quits, even from inside a tab.
	if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+c" {
		util.FlushMouse()
		return tea.Quit, true
	}
	// Key/mouse events that belong to an open modal overlay go to the
	// stack, not the tab. Async (non-key/mouse) messages are always
	// delivered to the tab so its child components keep animating.
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		if !m.overlayFocus.InteractiveToBase(msg) {
			return m.overlayStack.Update(msg), true
		}
	}
	// Remember which mode routed this message. Detail's esc/q → BackToList()
	// flips m.mode to modeList inside tab.Update; storing the returned tab
	// under m.mode after that would park the detail adapter on modeList and
	// steal every subsequent list keystroke/mouse event.
	activeMode := m.mode
	updated, cmd := tab.Update(msg)
	m.tabs[activeMode] = updated
	return cmd, true
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// PR-detail "Review controls" profile dropdown: while its panel is open
	// (and no modal overlay is active) it owns key/mouse input. ctrl+c still
	// quits. Result messages close it and apply the chosen profile.
	if m.mode == modeDetail && m.overlayStack.Top() == nil {
		switch typed := msg.(type) {
		case bubbledropdown.ItemChosenMsg, bubbledropdown.ItemCanceledMsg:
			if dt := m.detailTab(); dt != nil {
				return m, dt.HandleControlsProfileResult(msg)
			}
		case tea.KeyMsg:
			if dt := m.detailTab(); dt != nil && dt.ControlsProfileDropdownOpen() && typed.String() != "ctrl+c" {
				return m, dt.ForwardControlsProfileDropdown(msg)
			}
		case tea.MouseMsg:
			if dt := m.detailTab(); dt != nil && dt.ControlsProfileDropdownOpen() {
				return m, dt.ForwardControlsProfileDropdown(msg)
			}
		}
	}

	// Generic overlay forwarding: any message whose type implements
	// data.ForwardToOverlay is routed to the active review overlay without
	// a bespoke case. This is the structural fix for the deadlock class —
	// a newly-added pipeline message the overlay needs can no longer be
	// stranded by a forgotten root case (see root_routing_vibe_test.go).
	if _, ok := msg.(data.ForwardToOverlay); ok {
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, cmd := ro.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		if tab := m.tabs[m.mode]; tab != nil {
			tab.Resize(m.width, m.chromeBodyHeight())
			tab.SetContentOrigin(m.headerHeight())
		}
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		return m, m.overlayStack.Update(msg)

	case overlays.DismissMsg:
		// Every modal overlay emits this on dismissal. We pop the top
		// overlay and dispatch on its concrete type (and DismissMsg.Result)
		// so one message type drives every modal's teardown.
		popped, popCmd := m.overlayStack.Pop()
		switch popped.(type) {
		case overlays.ErrorOverlay:
			m.err = nil
			return m, popCmd
		case overlays.PostedOverlay:
			m.mode = modeList
			m.draft = nil
			// Same reasoning as the detail.go esc/q branch: re-sync the
			// bubbles list height now that we're back in list mode so the
			// panel's bubblezone bounds stay aligned with their visible
			// rows even when the post flow lengthened the status hint.
			m.relayout()
			return m, tea.Batch(popCmd, data.LoadPRsCmd(m.listMode(), m.opts.Demo))
		case overlays.BulkConfirmOverlay:
			if ans, ok := msg.Result.(overlays.BulkPostAnswer); ok && ans.Confirm && m.draft != nil && m.draft.PR != nil {
				return m, tea.Sequence(popCmd, data.PostReviewCmd(m.draft.Ref, m.draft, m.opts.DryRun, m.opts.Demo))
			}
			return m, popCmd
		case *overlays.PaletteOverlay:
			// Command palette closed. When the user ran a command, the
			// dismiss carries a PaletteChoice — run its wired action, which
			// returns the exact tea.Cmd the command's key binding does.
			if choice, ok := msg.Result.(overlays.PaletteChoice); ok && choice.Cmd.Run != nil {
				return m, tea.Sequence(popCmd, choice.Cmd.Run())
			}
			return m, popCmd
		case overlays.ResumeOverlay:
			// U2 resume prompt answered. Yes → rehydrate the saved session into
			// a review overlay (no LLM re-run). No → discard the stored session
			// so it isn't offered again, and fall back to today's behaviour.
			ans, _ := msg.Result.(overlays.ResumeAnswer)
			if ans.Resume && m.pendingResume != nil {
				return m, m.resumeFromSession(popCmd)
			}
			if s := m.pendingResume; s != nil {
				review.NewDraftCache().ClearSession(s.Ref, s.HeadSHA)
			}
			m.pendingResume = nil
			return m, popCmd
		default:
			// DryRunOverlay and any future acknowledgement-only modal: pop.
			return m, popCmd
		}

	case reviewtab.CloseMsg:
		var cmds []tea.Cmd
		for m.reviewOverlayOnTop() != nil {
			_, c := m.overlayStack.Pop()
			if c != nil {
				cmds = append(cmds, c)
			}
		}
		m.currentReviewOverlay = nil
		m.cancelReview()
		if m.mode == modeDetail {
			m.refreshDetailViews()
		}
		if m.mode == modeList {
			m.relayout()
		}
		cmds = append(cmds, m.reviewCompleteNotifyCmd())
		return m, tea.Batch(cmds...)

	case reviewOverlayMinimizeRequestMsg:
		return m, m.minimizeReviewOverlay()

	case reviewtab.JumpToDiffMsg:
		// Phase 5 item 4: a review card asked to jump the detail diff pane to
		// its finding. Minimize the overlay so the diff is visible, then scroll.
		cmd := m.minimizeReviewOverlay()
		if m.mode == modeDetail {
			m.JumpToFinding(msg.Path, msg.Line)
		}
		return m, cmd

	case data.ThreadsLoadedMsg:
		if dt := m.detailTab(); dt != nil {
			dt.ApplyThreadsLoaded(msg.Comments, msg.Threads)
		}
		return m, nil

	case data.ThreadReplyPostedMsg:
		if dt := m.detailTab(); dt != nil {
			status := "reply posted"
			if msg.Err != nil {
				status = "reply failed: " + msg.Err.Error()
			}
			dt.ApplyThreadReply(status)
		}
		return m, nil

	case state.NavigateMsg:
		return m.handleNavigate(msg.Target)

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
		m.mode = modeDetail
		m.ensureDetailTab()
		if dt := m.detailTab(); dt != nil {
			dt.OnPRLoaded(m.parsedDiff, m.draft)
			dt.RefreshViews()
		}
		return m, m.maybeOfferResumeCmd()

	case data.ChecksMsg:
		if m.currentPR == nil || msg.Ref.Owner != m.currentPR.Owner ||
			msg.Ref.Repo != m.currentPR.Repo || msg.Ref.Number != m.currentPR.Number {
			return m, nil
		}
		if dt := m.detailTab(); dt != nil {
			dt.ApplyChecks(msg.Report, msg.Err)
		}
		return m, nil

	case data.DiscussionMsg:
		if m.currentPR == nil || msg.Ref.Owner != m.currentPR.Owner ||
			msg.Ref.Repo != m.currentPR.Repo || msg.Ref.Number != m.currentPR.Number {
			return m, nil
		}
		if dt := m.detailTab(); dt != nil {
			dt.ApplyDiscussion(msg.Timeline, msg.Err)
		}
		return m, nil

	case data.ReviewStartedMsg:
		m.progressCh = msg.Ch
		return m, data.WaitForProgressCmd(m.progressCh)

	case data.ProgressMsg:
		p := review.Progress(msg)
		if p.Stage == "done" {
			m.draft = p.Final
			m.recomputeTreeRows()
		}
		cmd := data.WaitForProgressCmd(m.progressCh)
		if ro := m.reviewOverlayOnTop(); ro != nil {
			_, overlayCmd := ro.Update(msg)
			cmd = tea.Batch(cmd, overlayCmd)
		}
		return m, cmd

	case data.PRRefreshedMsg:
		if msg.PR != nil {
			if m.currentPR != nil && m.currentPR.Number == msg.PR.Number && m.currentPR.Owner == msg.PR.Owner && m.currentPR.Repo == msg.PR.Repo {
				m.currentPR = msg.PR
				m.diff = msg.Diff
				m.parsedDiff = review.ParseDiff(m.diff)
				m.recordPRLanguages(msg.PR, m.parsedDiff)
				if dt := m.detailTab(); dt != nil {
					dt.OnDraftUpdated(m.parsedDiff, m.draft)
					dt.RefreshViews()
				}
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
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.OnRunClosed()
		}
		// Phase 5 item 3: the run finished — ring the bell + fire an OSC-9
		// desktop notification so a reviewer who backgrounded the terminal
		// while the multi-minute run churned gets pinged. The terminal
		// decides whether to surface it (focused terminals ignore/flash),
		// so it stays unobtrusive. Skipped in demo mode / recordings.
		return m, m.reviewCompleteNotifyCmd()

	case data.QueueReviewStartedMsg:
		if !m.queue.active {
			return m, nil
		}
		m.queue.ch = msg.Ch
		m.queue.stage = "running"
		m.updateListTitle()
		return m, data.WaitForQueueProgressCmd(msg.Ch)

	case data.QueueReviewErrMsg:
		if !m.queue.active {
			return m, nil
		}
		// A run that failed to start is counted as failed and the queue
		// advances rather than aborting the whole batch.
		m.queue.failed++
		return m, m.advanceQueue()

	case data.QueueProgressMsg:
		if !m.queue.active {
			return m, nil
		}
		if s := review.Progress(msg).Stage; s != "" {
			m.queue.stage = s
			m.updateListTitle()
		}
		return m, data.WaitForQueueProgressCmd(m.queue.ch)

	case data.QueueReviewClosedMsg:
		if !m.queue.active {
			return m, nil
		}
		m.queue.done++
		return m, m.advanceQueue()

	case data.PostDoneMsg:
		// Either summary (from review overlay) or bulk (legacy P key) just
		// posted successfully.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.MarkSummaryPosted()
			// B3: on a re-review, leave status replies ("resolved" / "still
			// present") on the tool's own prior threads now that the review
			// has actually posted. Nil on a first review / dry-run / demo.
			return m, ro.StatusRepliesCmd()
		}
		m.stagedReset()
		m.mode = modeDetail
		m.ensureDetailTab()
		m.refreshDetailViews()
		pm := overlays.PostedOverlay{}
		cfg := overlay.DefaultOverlayConfig()
		cfg.CloseOnClickOutside = false
		return m, tea.Batch(
			m.overlayStack.Push(pm, cfg),
			func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
		)

	case data.StatusRepliesPostedMsg:
		// B3 re-run status replies finished — note the outcome in the review
		// overlay log (fail-open: never fatal).
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.NoteStatusReplies(msg.Posted, msg.Failed)
		}
		return m, nil

	case util.ClipboardCopiedMsg:
		// Phase 5 item 9: a copy attempt finished. Route the outcome to the
		// top overlay (the review overlay's copy-status footer, or the error
		// overlay's "Copied!" badge) so it can reflect success / OSC52
		// fallback / failure. Fail-open — never surfaces its own error modal.
		return m, m.overlayStack.Update(msg)

	case util.BrowserOpenedMsg:
		if msg.Err == nil {
			return m, nil
		}
		// Surface launch failures (xdg-open missing, unsupported scheme,
		// etc.) through the standard error overlay so the user can see
		// what went wrong without us hijacking the more disruptive
		// data.ErrMsg pathway (which would also clobber review-overlay state).
		return m, m.pushErrorOverlay(fmt.Errorf("open in browser: %s\n\nURL: %s", msg.Err.Error(), msg.URL))

	case data.ErrMsg:
		m.err = msg.Err
		pushErr := m.pushErrorOverlay(msg.Err)
		// Review overlay: still record failure on the card / summary strip, but also
		// stack the dedicated error modal (copy button, scroll) like jj-tui — the
		// inline "✗ …" line is easy to miss and truncates long gh API payloads.
		if ro := m.reviewOverlayOnTop(); ro != nil {
			ro.MarkPostError(msg.Err)
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
		if m.mode == modeList {
			m.dismissStaleOverlaysSilent()
		}
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
		// Single forwarding loop: settings / repo-agents / lang-agents all
		// route through the same Tab entry.
		if cmd, handled := m.routeToActiveTab(msg); handled {
			return m, cmd
		}
		// modeList / modeDetail: while a modal is up, key/mouse belong to
		// the stack (or, over the review overlay on the detail page, pass
		// mouse-outside-modal clicks through to the background).
		if !m.overlayFocus.InteractiveToBase(msg) {
			if mm, ok := msg.(tea.MouseMsg); ok && m.shouldPassMouseToBackground(mm) {
				if m.mode == modeDetail {
					return m.detailHandleMouse(mm, tea.MouseEvent(mm).IsWheel())
				}
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

	// Async (non-key/mouse) messages: forward to the active tab so its
	// child components (cursor blink, dropdown results, regen-done, etc.)
	// keep working. The tab sub-models emit custom message types the root
	// cannot pattern-match on, hence the blanket forward.
	if cmd, handled := m.routeToActiveTab(msg); handled {
		return m, cmd
	}

	if m.mode == modeList {
		// In modeList we may have any of three focused widgets — the list
		// itself, the inline search input, or the inline URL input. Async
		// messages (cursor blink, etc.) are forwarded to the matching
		// widget so its caret keeps animating; keystrokes went through
		// handleListKey above.
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
	}

	return m, nil
}

// stagedReset clears any pre-overlay-era staged-flow state. With the
// persistent overlay, this is mostly a no-op kept for safety.
func (m *Model) stagedReset() {}
