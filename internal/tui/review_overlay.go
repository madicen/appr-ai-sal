package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

const (
	oaPending = iota
	oaRunning
	oaDone
	oaSkipped
	oaErr
)

// overlayAgentStage groups overlay rows into the four pipeline stages so the
// running view can show parallelism, dependencies, and per-stage progress
// rather than just a flat pending/running/done flag.
type overlayAgentStage int

const (
	stageGroupSpecialists overlayAgentStage = iota
	stageGroupExperts
	stageGroupArbiter
	stageGroupVibe
)

// stageGroupOrder is the rendering order top-to-bottom. It also reflects
// runtime ordering: specialists, then repo arbiter, then vibe-coach.
// Specialists may run sequentially or in parallel (repo-context.json).
var stageGroupOrder = []overlayAgentStage{
	stageGroupSpecialists,
	stageGroupArbiter,
	stageGroupVibe,
}

// stageGroupMeta is the human label and one-line note shown above each
// group's row list.
type stageGroupMeta struct {
	label string
	note  string
}

var stageGroupMetas = map[overlayAgentStage]stageGroupMeta{
	stageGroupSpecialists: {label: "Specialists", note: ""},
	stageGroupExperts:     {label: "Repo experts", note: ""}, // unused after repo-agents refactor; kept to satisfy the enum
	stageGroupArbiter:     {label: "Repo arbiter", note: "after specialists"},
	stageGroupVibe:        {label: "Vibe coach", note: "after repo arbiter"},
}

// Agent name constant for the repo arbiter row in the overlay. The runner
// emits Stage="repo-arbiter" with Detail="start"/"done"/"skipped".
const (
	overlayAgentRepoArbiter = "repo-arbiter"
)

type overlayAgentRow struct {
	name  string
	stage overlayAgentStage
	phase int
	err   error
	// summary is the agent's short result text (specialist summary, vibe-coach
	// summary). Shown when expanded.
	summary   string
	findingsN int
	// startedAt and finishedAt let the renderer show elapsed time live (per-agent
	// timers show how long each specialist took even though they run in order).
	startedAt  time.Time
	finishedAt time.Time
	// retries counts how many times the runner restarted this agent (parse
	// failures, transient HTTP, etc.). Surfaced as "retried Nx" on the row.
	retries int
	// lastRetry is the most recent retry detail string for tooltips when the
	// row is expanded.
	lastRetry string
	expanded  bool
}

// overlayPhase tags which screen the persistent review overlay is showing.
type overlayPhase int

const (
	phaseRunning overlayPhase = iota
	phaseApprove
	// phaseGeneratingSummary is a short interstitial that runs the
	// deferred vibe-coach call against the user's final skip set before
	// the rendered summary is shown. The runner emits
	// Progress{Stage: "vibe-coach", Detail: "deferred"} so this phase
	// only exists in the TUI — it sits between approve and summary.
	phaseGeneratingSummary
	phaseSummary
	phaseConfirmApprove
	phasePosted
)

// approvalCardState records what the user did with each finding.
type approvalCardState int

const (
	cardPending approvalCardState = iota
	cardPosted
	cardSkipped
	cardError
	// cardAlreadyOnPR means the same inline comment is already on GitHub (viewer-authored).
	cardAlreadyOnPR
)

// approvalCard wraps one inline finding with its UI state.
type approvalCard struct {
	finding review.FlatFinding
	state   approvalCardState
	err     error
	hunk    *review.Hunk
	file    *review.FileDiff
}

// reviewOverlay is the persistent overlay that hosts the entire review flow:
// running → approve findings one-by-one → confirm summary post → posted.
type reviewOverlay struct {
	outerW int
	outerH int
	vp     viewport.Model
	sp     spinner.Model
	agents []overlayAgentRow
	log    []string
	cursor int
	// specialistsParallel / repoExpertsParallel mirror repo-context.json (+ env overrides)
	// so the running header matches how the runner dispatches API calls.
	specialistsParallel bool
	repoExpertsParallel bool
	// runStartedAt is when the running phase began (overlay construction). The
	// running view shows total elapsed time relative to this; per-agent timers
	// show each specialist's duration after it finishes.
	runStartedAt time.Time

	phase  overlayPhase
	dryRun bool

	draft *review.Draft
	files []review.FileDiff
	cards []approvalCard
	idx   int

	// summary phase state
	summaryDone   bool
	summarySkip   bool
	summaryErr    error
	summaryDryMsg string

	// When repo arbiter suppressed inline findings, require explicit ack before y/n.
	pendingSuppressAck bool

	// approveAfterSkipDisagree: user skipped every postable inline (posted none)
	// while the AI verdict was not APPROVE — we offer GitHub APPROVE anyway.
	approveAfterSkipDisagree bool

	// noFindingsApprove: every agent came back clean (Draft.HasNoFindings).
	// We auto-route to phaseConfirmApprove with a "no issues found" message
	// instead of dumping the user on a near-empty post-summary screen.
	noFindingsApprove bool

	existingCommentsLoading bool

	// priorActivity captures whether appr-ai-sal has already reviewed this
	// PR (by counting inline comments and review bodies whose disclosure
	// markers match). When .Found() is true the overlay renders an
	// acknowledgement banner so the human knows this is a refresh, not a
	// first pass.
	priorActivity gh.PriorAprrAISalActivity

	// refreshing is true while a refreshPRCmd is in flight. It disables the
	// refresh button and shows an inline "refreshing PR…" status near the
	// error so the user knows their R press was registered.
	refreshing bool
	// refreshNote is an optional banner shown after a refresh completes,
	// e.g. "PR head moved abc1234 → def5678; 2 finding(s) no longer anchor
	// to a hunk on the new diff." Cleared when the user moves on.
	refreshNote string

	// aiConfig is the LLM config used to run vibe-coach lazily when the
	// user transitions from approve → summary. Nil in test constructors;
	// when nil, enterSummary still flips into phaseSummary directly (so
	// existing overlay tests don't need to stub an LLM).
	aiConfig *aiconfig.Config
	// lastCoachHash is the hash of d.UserSkipPostKeys at the time
	// d.VibeCoach was last generated. enterSummary uses it to decide
	// whether to re-run vibe-coach when the user navigates back to
	// approve, changes skips, and re-enters summary.
	lastCoachHash string
	// coachInFlight guards against double-issuing the vibe-coach LLM
	// call when the user bounces between phases. enterSummary returns
	// nil cmd when this is true.
	coachInFlight bool
	// coachErr is the most recent error from the deferred vibe-coach
	// run, surfaced in the summary header so the user knows the summary
	// they're seeing may be stale or missing fix-prompts.
	coachErr error

	// peruse is the read-only walkthrough mode (entered via ctrl+v from
	// the PR detail view). When true, actPost* and actSkip* become no-ops
	// and the help line says so — the user can browse findings and the
	// rendered summary without committing anything to GitHub.
	peruse bool
	// peruseHint is set briefly when the user presses a disabled action
	// key in peruse mode. Rendered in the help line for one frame as a
	// flash response, then cleared on the next non-flash key.
	peruseHint string
}

func zoneOverlayAgent(i int) string {
	return fmt.Sprintf("zone:overlay:agent:%d", i)
}

// newReviewOverlay builds a fresh review overlay. cfg is used to run
// vibe-coach lazily on the approve→summary transition; pass nil in
// tests that don't exercise that path (the overlay then skips the
// deferred LLM call and lands directly in phaseSummary with whatever
// the draft already has).
func newReviewOverlay(screenW, screenH int, dryRun bool, specialistsParallel, repoExpertsParallel bool, cfg *aiconfig.Config) *reviewOverlay {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	ow := clamp(screenW-4, 60, 140)
	oh := clamp(screenH-6, 16, 60)
	innerW := max(12, ow-modalChrome.GetHorizontalFrameSize()-4)
	innerH := max(6, oh-modalChrome.GetVerticalFrameSize()-6)
	vp := viewport.New(innerW, innerH)
	vp.MouseWheelEnabled = true
	// Build the agent rows in pipeline order. The running view groups them by
	// stage (specialists → arbiter → vibe). Each row tracks its own
	// start/finish timestamps and retry count for actionable feedback.
	ag := make([]overlayAgentRow, 0, len(review.AllSpecialists)+2)
	for _, n := range review.AllSpecialists {
		ag = append(ag, overlayAgentRow{name: n, stage: stageGroupSpecialists, phase: oaPending})
	}
	ag = append(ag,
		overlayAgentRow{name: overlayAgentRepoArbiter, stage: stageGroupArbiter, phase: oaPending},
		overlayAgentRow{name: review.SpecVibeCoach, stage: stageGroupVibe, phase: oaPending},
	)
	return &reviewOverlay{
		outerW:              ow,
		outerH:              oh,
		vp:                  vp,
		sp:                  sp,
		agents:              ag,
		cursor:              0,
		phase:               phaseRunning,
		specialistsParallel: specialistsParallel,
		repoExpertsParallel: repoExpertsParallel,
		dryRun:              dryRun,
		runStartedAt:        time.Now(),
		aiConfig:            cfg,
	}
}

func (m *reviewOverlay) specialistsStageNote() string {
	n := len(review.AllSpecialists)
	if m.specialistsParallel {
		return fmt.Sprintf("%d agents · parallel", n)
	}
	return fmt.Sprintf("%d agents · sequential", n)
}

func (m *reviewOverlay) repoExpertsStageNote() string {
	// Retained for symmetry with stageGroupMetas; the repo-experts stage is
	// no longer rendered after the repo-agents refactor.
	return ""
}

func (m *reviewOverlay) Init() tea.Cmd {
	return m.sp.Tick
}

func (m *reviewOverlay) resizeFromScreen(sw, sh int) {
	m.outerW = clamp(sw-4, 60, 140)
	m.outerH = clamp(sh-6, 16, 60)
	innerW := max(12, m.outerW-modalChrome.GetHorizontalFrameSize()-4)
	innerH := max(6, m.outerH-modalChrome.GetVerticalFrameSize()-6)
	m.vp.Width = innerW
	m.vp.Height = innerH
}

// adoptDraft is invoked once the runner reports Stage="done". It populates
// the approval cards and switches into approve phase. When the verdict is
// approve and there are no inline findings to walk (and the arbiter has no
// suppressions for the user to acknowledge), the overlay jumps straight to
// the one-click confirmApprove phase — no summary preview, no markdown body,
// just an "Approve PR?" confirmation.
//
// If the entire pipeline came back clean (Draft.HasNoFindings) we also route
// directly to confirmApprove regardless of the AI verdict, with a "no issues
// found, approving" message — landing on the post-summary screen with
// nothing to summarize is confusing.
//
// If the arbiter suppressed inline findings, we route through phaseApprove
// first so pendingSuppressAck can show the suppress notice; the user
// acknowledges and walks the (possibly empty) card list before reaching
// confirmApprove via advanceCard.
// adoptDraft installs the runner's final draft, builds approval cards,
// and routes to the appropriate first phase. Returns a tea.Cmd that
// callers MUST include in their tea.Batch — when the post-arbiter set
// has no cards and the verdict isn't APPROVE, the overlay needs to
// dispatch the deferred vibe-coach call before showing the summary.
// (Tests that ignore the return value still work because they don't
// exercise the deferred-LLM path.)
func (m *reviewOverlay) adoptDraft(d *review.Draft) tea.Cmd {
	m.draft = d
	m.approveAfterSkipDisagree = false
	m.noFindingsApprove = false
	if d == nil {
		return nil
	}
	m.files = review.ParseDiff(d.Diff)
	flat := d.FlatPostableFindingsForPost()
	allN := len(d.FlatPostableFindings())
	m.pendingSuppressAck = d.HasRepoExpertSuppressions() && len(flat) < allN
	m.cards = make([]approvalCard, 0, len(flat))
	for _, f := range flat {
		card := approvalCard{finding: f}
		if file := review.FindFile(m.files, f.Finding.Path); file != nil {
			card.file = file
			if h, _ := review.HunkAroundLine(file, f.Finding.Line); h != nil {
				card.hunk = h
			}
		}
		m.cards = append(m.cards, card)
	}
	m.idx = 0
	m.existingCommentsLoading = false
	// If the draft came back with a non-nil VibeCoach (e.g. a stub for
	// tests, or a legacy runner that didn't defer), record its skip
	// hash now so a same-skip-set re-entry doesn't pointlessly re-run.
	if d.VibeCoach != nil {
		m.lastCoachHash = skipSetHash(d.UserSkipPostKeys)
	} else {
		m.lastCoachHash = ""
	}
	switch {
	case m.pendingSuppressAck:
		m.phase = phaseApprove
		m.vp.GotoTop()
		return nil
	case d.HasNoFindings():
		// Nothing actionable came back from any agent — go straight to a
		// tailored APPROVE confirmation instead of the post-summary screen.
		// No vibe-coach needed (APPROVE bodies are empty).
		m.noFindingsApprove = true
		m.phase = phaseConfirmApprove
		m.vp.GotoTop()
		return nil
	case len(m.cards) == 0 && d.PostEvent() == "APPROVE":
		m.phase = phaseConfirmApprove
		m.vp.GotoTop()
		return nil
	case len(m.cards) == 0:
		// No cards to walk → user can't change skips → vibe-coach
		// runs against the post-arbiter set immediately on enter.
		return m.enterSummary()
	default:
		m.phase = phaseApprove
		m.vp.GotoTop()
		return nil
	}
}

func (m *reviewOverlay) cmdAfterAdoptIfNeeded() tea.Cmd {
	if m.dryRun || len(m.cards) == 0 || m.draft == nil {
		return nil
	}
	m.existingCommentsLoading = true
	return fetchExistingPRCommentsCmd(m.draft.Ref)
}

func (m *reviewOverlay) markCardsAlreadyOnGitHub(viewer string, existing []gh.PullReviewComment) tea.Cmd {
	for i := range m.cards {
		ff := &m.cards[i].finding
		side := ff.Finding.Side
		if side == "" {
			side = "RIGHT"
		}
		body := review.ReviewCommentBody(ff.Specialist, ff.Finding)
		if gh.ViewerHasMatchingComment(viewer, ff.Finding.Path, ff.Finding.Line, side, body, existing) {
			m.cards[i].state = cardAlreadyOnPR
		}
	}
	m.idx = m.firstPendingCardIndex()
	if m.idx >= len(m.cards) {
		return m.enterSummary()
	}
	m.vp.GotoTop()
	return nil
}

func (m *reviewOverlay) firstPendingCardIndex() int {
	for i := range m.cards {
		if m.cards[i].state == cardPending {
			return i
		}
	}
	return len(m.cards)
}

func (m *reviewOverlay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resizeFromScreen(msg.Width, msg.Height)
		m.rebuildBody()
		var c0, c1 tea.Cmd
		m.vp, c0 = m.vp.Update(msg)
		m.sp, c1 = m.sp.Update(msg)
		return m, tea.Batch(c0, c1)

	case progressMsg:
		cmd := m.mergeProgress(review.Progress(msg))
		m.rebuildBody()
		return m, cmd

	case existingPRCommentsMsg:
		m.existingCommentsLoading = false
		m.priorActivity = msg.Prior
		if msg.Prior.Found() {
			m.log = append(m.log, formatPriorActivityLog(msg.Prior))
		}
		if msg.ListErr != nil {
			m.log = append(m.log, "existing PR comments: "+msg.ListErr.Error())
			m.rebuildBody()
			return m, nil
		}
		if msg.ViewerErr != nil || strings.TrimSpace(msg.Viewer) == "" {
			m.log = append(m.log, "duplicate detection skipped (could not resolve gh user)")
			m.rebuildBody()
			return m, nil
		}
		cmd := m.markCardsAlreadyOnGitHub(msg.Viewer, msg.Comments)
		m.rebuildBody()
		return m, cmd

	case stagedFindingPostedMsg:
		// Single finding succeeded — mark current as posted and advance.
		var advCmd tea.Cmd
		if m.phase == phaseApprove && m.idx < len(m.cards) {
			m.cards[m.idx].state = cardPosted
			advCmd = m.advanceCard()
		}
		m.rebuildBody()
		return m, advCmd

	case prRefreshedMsg:
		// User pressed R after a 422 / drift; we have a fresh PR + diff. Adopt
		// it and re-anchor every pending card to the new diff so the next y
		// press hits an up-to-date commit_id and a valid line.
		m.applyPRRefresh(msg.pr, msg.diff)
		return m, nil

	case vibeCoachDoneMsg:
		m.coachInFlight = false
		// If the user changed skips again between issue and completion,
		// the current skip-hash will differ from the one this message
		// captured. Re-issue against the new set rather than installing
		// a stale result.
		if m.draft == nil {
			return m, nil
		}
		curHash := skipSetHash(m.draft.UserSkipPostKeys)
		if curHash != msg.atSkipHash {
			// Stale completion. Re-issue (enterSummary will set
			// coachInFlight=true again).
			return m, m.enterSummary()
		}
		if msg.result != nil {
			m.draft.VibeCoach = msg.result
			m.coachErr = msg.result.Err
		}
		m.lastCoachHash = curHash
		// Verdict may have changed (e.g. user skipped the last blocker
		// → APPROVE). Route accordingly.
		if !m.peruse && m.draft.PostEvent() == "APPROVE" && len(m.cards) > 0 {
			// Only auto-route to confirmApprove when there were
			// cards (i.e. we came through phaseApprove). If we
			// reached enterSummary from adoptDraft directly with
			// no cards, the original adopt logic already routed
			// us — don't override the user.
		}
		m.phase = phaseSummary
		m.vp.GotoTop()
		m.rebuildBody()
		return m, nil

	case dryRunPayloadMsg:
		// In dry-run, both the approve flow and summary post route here.
		var advCmd tea.Cmd
		switch {
		case m.phase == phaseApprove && m.idx < len(m.cards):
			m.cards[m.idx].state = cardPosted // treat preview as accepted under dry-run
			advCmd = m.advanceCard()
		case m.phase == phaseSummary, m.phase == phaseConfirmApprove:
			// Mirror the real-post path: record the receipt and move to
			// phasePosted so the user gets a "Close (enter)" hint instead of
			// staring at the same card while the preview accumulates.
			m.summaryDryMsg = msg.Title
			m.summaryDone = true
			m.phase = phasePosted
		}
		m.rebuildBody()
		return m, advCmd

	case spinner.TickMsg:
		var c0, c1 tea.Cmd
		m.sp, c0 = m.sp.Update(msg)
		m.vp, c1 = m.vp.Update(msg)
		if m.phase == phaseRunning || m.phase == phaseGeneratingSummary {
			// Elapsed timers use time.Since(startedAt); refresh body each tick so they live-update.
			m.rebuildBody()
		}
		return m, tea.Batch(c0, c1)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *reviewOverlay) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseRunning:
		switch msg.String() {
		case "j":
			if m.cursor < len(m.agents)-1 {
				m.cursor++
			}
			m.rebuildBody()
			return m, nil
		case "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.rebuildBody()
			return m, nil
		case " ", "enter":
			if m.cursor >= 0 && m.cursor < len(m.agents) {
				if hasExpandableContent(&m.agents[m.cursor]) {
					m.agents[m.cursor].expanded = !m.agents[m.cursor].expanded
					m.rebuildBody()
				}
			}
			return m, nil
		case "q", "esc":
			// Abort: close the overlay even though the runner is still in flight.
			// The goroutine continues; if it eventually finishes, m.draft on the
			// root model is populated and the user can press 'a' to reopen
			// approval against that draft.
			return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
		}
	case phaseApprove:
		if m.pendingSuppressAck {
			switch msg.String() {
			case "enter", " ":
				m.pendingSuppressAck = false
				// If the user had no cards to walk after the suppress notice,
				// jump immediately to the right next phase rather than parking
				// them on a "no findings" empty card view.
				if len(m.cards) == 0 || m.idx >= len(m.cards) {
					if m.draft != nil && m.draft.PostEvent() == "APPROVE" {
						m.phase = phaseConfirmApprove
						m.vp.GotoTop()
						m.rebuildBody()
						return m, nil
					}
					cmd := m.enterSummary()
					return m, cmd
				}
				m.rebuildBody()
				return m, nil
			case "q", "esc":
				return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
			}
			return m, nil
		}
		if m.existingCommentsLoading {
			switch msg.String() {
			case "q", "esc":
				return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
			}
			return m, nil
		}
		switch msg.String() {
		case "y", "Y":
			return m.actPostCurrent()
		case "n", "N", "s":
			return m.actSkipCurrent()
		case "right", "l":
			return m.actNext()
		case "left", "h":
			return m.actPrev()
		case "r", "R":
			return m.actRefreshPR()
		case "f":
			// Finish approving early; jump to summary even with cards left.
			// enterSummary handles syncing skips + dispatching the
			// deferred vibe-coach call against the final set.
			cmd := m.enterSummary()
			return m, cmd
		case "q", "esc":
			return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
		}
	case phaseGeneratingSummary:
		// Refining-summary interstitial. The only escape is to abort
		// the overlay entirely; everything else waits for the
		// vibeCoachDoneMsg to flip into phaseSummary.
		switch msg.String() {
		case "q", "esc":
			return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
		}
		return m, nil
	case phaseSummary:
		switch msg.String() {
		case "y", "Y", "enter":
			return m.actPostSummary()
		case "a", "A":
			if m.summaryPhaseOfferApproveWithoutSummary() {
				return m.actPostApprove()
			}
			return m, nil
		case "n", "N":
			// In peruse mode there's nothing to "skip posting" of, so
			// just close the overlay rather than rendering a misleading
			// "you skipped post" message.
			if m.peruse {
				return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
			}
			m.summarySkip = true
			m.phase = phasePosted
			m.rebuildBody()
			return m, nil
		case "r", "R":
			return m.actRefreshPR()
		case "esc", "q":
			return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
		}
	case phaseConfirmApprove:
		switch msg.String() {
		case "y", "Y", "enter":
			return m.actPostApprove()
		case "n", "N":
			// In the no-findings auto-approve flow there is no summary to
			// fall back to, so swallow n. Otherwise route to phaseSummary
			// for a comment-only review.
			if m.noFindingsApprove {
				return m, nil
			}
			m.approveAfterSkipDisagree = false
			cmd := m.enterSummary()
			return m, cmd
		case "r", "R":
			return m.actRefreshPR()
		case "esc", "q":
			return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
		}
	case phasePosted:
		switch msg.String() {
		case "esc", "enter", "q", " ":
			return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
		}
	}
	// Fall through: pass scroll keys to the viewport.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *reviewOverlay) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		switch m.phase {
		case phaseRunning:
			for i := range m.agents {
				if z := zone.Get(zoneOverlayAgent(i)); z != nil && z.InBounds(msg) {
					m.cursor = i
					if hasExpandableContent(&m.agents[i]) {
						m.agents[i].expanded = !m.agents[i].expanded
						m.rebuildBody()
					}
					return m, nil
				}
			}
		case phaseApprove:
			if m.pendingSuppressAck {
				return m, nil
			}
			if m.existingCommentsLoading {
				return m, nil
			}
			if z := zone.Get(ZoneStagedPost); z != nil && z.InBounds(msg) {
				return m.actPostCurrent()
			}
			if z := zone.Get(ZoneStagedSkip); z != nil && z.InBounds(msg) {
				return m.actSkipCurrent()
			}
			if z := zone.Get(ZoneStagedNext); z != nil && z.InBounds(msg) {
				return m.actNext()
			}
			if z := zone.Get(ZoneStagedPrev); z != nil && z.InBounds(msg) {
				return m.actPrev()
			}
			if z := zone.Get(ZoneStagedRefresh); z != nil && z.InBounds(msg) {
				return m.actRefreshPR()
			}
			if z := zone.Get(ZoneStagedFinish); z != nil && z.InBounds(msg) {
				cmd := m.enterSummary()
				return m, cmd
			}
			if z := zone.Get(ZoneStagedQuit); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
			}
		case phaseSummary:
			if z := zone.Get(ZoneStagedSummaryYes); z != nil && z.InBounds(msg) {
				return m.actPostSummary()
			}
			if z := zone.Get(ZoneStagedSummaryNo); z != nil && z.InBounds(msg) {
				if m.peruse {
					return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
				}
				m.summarySkip = true
				m.phase = phasePosted
				m.rebuildBody()
				return m, nil
			}
			if m.summaryPhaseOfferApproveWithoutSummary() {
				if z := zone.Get(ZoneStagedSummaryApproveOnly); z != nil && z.InBounds(msg) {
					return m.actPostApprove()
				}
			}
			if z := zone.Get(ZoneStagedRefresh); z != nil && z.InBounds(msg) {
				return m.actRefreshPR()
			}
			if z := zone.Get(ZoneStagedQuit); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
			}
		case phaseConfirmApprove:
			if z := zone.Get(ZoneStagedSummaryYes); z != nil && z.InBounds(msg) {
				return m.actPostApprove()
			}
			// The no-findings approve flow doesn't render the "no" zone (no
			// comment-only review to fall back to when there's nothing to
			// say), so only honour the click when the button is present.
			if !m.noFindingsApprove {
				if z := zone.Get(ZoneStagedSummaryNo); z != nil && z.InBounds(msg) {
					m.approveAfterSkipDisagree = false
					cmd := m.enterSummary()
					return m, cmd
				}
			}
			if z := zone.Get(ZoneStagedRefresh); z != nil && z.InBounds(msg) {
				return m.actRefreshPR()
			}
			if z := zone.Get(ZoneStagedQuit); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
			}
		case phasePosted:
			if z := zone.Get(ZonePostedOK); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return reviewOverlayCloseMsg{} }
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// vibeCoachDoneMsg is delivered when the deferred vibe-coach LLM call
// (kicked off by enterSummary) completes. The atSkipHash captures the
// state of d.UserSkipPostKeys when the call was issued so a stale
// completion (user has since changed skips) doesn't overwrite a newer
// in-flight result.
type vibeCoachDoneMsg struct {
	result      *review.VibeCoachResult
	atSkipHash  string
	requestedAt time.Time
}

// skipSetHash returns a stable hash of the user-skip set so enterSummary
// can decide whether to re-run vibe-coach. Empty set hashes to "".
func skipSetHash(keys map[string]struct{}) string {
	if len(keys) == 0 {
		return ""
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	sum := sha256.Sum256([]byte(strings.Join(out, "\n")))
	return hex.EncodeToString(sum[:])
}

// enterSummary is the canonical transition into phaseSummary. It syncs
// the user's skips onto the draft, then either:
//
//   - lands directly in phaseSummary when no LLM refresh is needed
//     (no aiConfig, identical skip set as last run, or peruse mode
//     with an already-fresh draft), or
//   - sets phaseGeneratingSummary and returns a tea.Cmd that runs
//     vibe-coach against the final finding set off the UI thread.
//
// Callers should include the returned cmd in their tea.Batch. Safe to
// call repeatedly — coachInFlight guards against double-issue.
func (m *reviewOverlay) enterSummary() tea.Cmd {
	m.syncUserSkipsToDraft()
	hash := ""
	if m.draft != nil {
		hash = skipSetHash(m.draft.UserSkipPostKeys)
	}
	// If vibe-coach is already current for this skip set, just flip
	// the phase and let the user see the cached summary. This is the
	// common case on re-entry (user backs out, doesn't change skips,
	// re-enters).
	if m.draft != nil && m.draft.VibeCoach != nil && hash == m.lastCoachHash && m.coachErr == nil {
		m.phase = phaseSummary
		m.vp.GotoTop()
		m.rebuildBody()
		return nil
	}
	// No AI config (tests / dev) → just show the summary with whatever
	// is on the draft. Production always passes a config.
	if m.aiConfig == nil || m.draft == nil {
		m.phase = phaseSummary
		m.vp.GotoTop()
		m.rebuildBody()
		return nil
	}
	// Already running a vibe-coach call → don't issue a second one.
	if m.coachInFlight {
		m.phase = phaseGeneratingSummary
		m.rebuildBody()
		return nil
	}
	m.coachInFlight = true
	m.coachErr = nil
	m.phase = phaseGeneratingSummary
	m.vp.GotoTop()
	m.rebuildBody()
	return runVibeCoachCmd(m.draft, m.aiConfig, hash)
}

// runVibeCoachCmd kicks off vibe-coach against the draft's post-skip
// finding set on a background goroutine. The atSkipHash is echoed back
// in the done-msg so the receiver can drop stale results.
func runVibeCoachCmd(d *review.Draft, cfg *aiconfig.Config, atSkipHash string) tea.Cmd {
	requestedAt := time.Now()
	return func() tea.Msg {
		res := review.RunVibeCoachForDraft(context.Background(), cfg, d)
		return vibeCoachDoneMsg{result: res, atSkipHash: atSkipHash, requestedAt: requestedAt}
	}
}

// syncUserSkipsToDraft copies skipped approval-card findings onto the draft so
// RenderBody and FlatPostableFindingsForPost exclude them from the GitHub summary.
func (m *reviewOverlay) syncUserSkipsToDraft() {
	if m.draft == nil {
		return
	}
	m.draft.UserSkipPostKeys = nil
	for _, c := range m.cards {
		if c.state != cardSkipped {
			continue
		}
		k := review.FindingSuppressionKey(c.finding.Specialist, c.finding.Finding)
		if m.draft.UserSkipPostKeys == nil {
			m.draft.UserSkipPostKeys = make(map[string]struct{})
		}
		m.draft.UserSkipPostKeys[k] = struct{}{}
	}
}

// advanceCard moves to the next pending card. When the cards are
// exhausted it transitions into the next phase (confirmApprove or, via
// enterSummary, the deferred vibe-coach + summary path). Returns the
// tea.Cmd that callers must include in their batch so the deferred
// vibe-coach LLM call gets dispatched.
//
// Note: PostEvent() may currently return a stale verdict (vibe-coach
// hasn't re-run yet against the final skip set). That's fine — the
// confirmApprove vs summary decision is conservative: if the pre-skip
// verdict was APPROVE, the user wanted approval and the post-skip set
// can only have shrunk, so APPROVE is still safe. If it was anything
// else, we route through summary, where the vibeCoachDoneMsg handler
// can re-evaluate.
func (m *reviewOverlay) advanceCard() tea.Cmd {
	if m.idx < len(m.cards) {
		m.idx++
	}
	if m.idx >= len(m.cards) {
		_, posted, skipped := m.tallyCardKinds()
		// User skipped every suggestion (posted no inline comments) but the AI
		// did not recommend approve — treat as disagreeing with the objections
		// and offer GitHub APPROVE before the long summary path.
		skipDisagree := m.draft != nil && m.draft.PostEvent() != "APPROVE" && posted == 0 && skipped > 0
		// Peruse mode never offers approval shortcuts — we only show
		// the rendered summary so the user can read it.
		if m.peruse {
			skipDisagree = false
		}
		m.approveAfterSkipDisagree = skipDisagree
		switch {
		case skipDisagree:
			m.phase = phaseConfirmApprove
			m.vp.GotoTop()
			return nil
		case !m.peruse && m.draft != nil && m.draft.PostEvent() == "APPROVE":
			m.approveAfterSkipDisagree = false
			m.phase = phaseConfirmApprove
			m.vp.GotoTop()
			return nil
		default:
			m.approveAfterSkipDisagree = false
			return m.enterSummary()
		}
	}
	m.vp.GotoTop()
	return nil
}

func (m *reviewOverlay) actPostCurrent() (tea.Model, tea.Cmd) {
	if m.peruse {
		return m.flashPeruse("peruse mode — no posting; use ←/→ to navigate, f to jump to summary, q to exit")
	}
	if m.existingCommentsLoading || m.idx >= len(m.cards) || m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	cur := &m.cards[m.idx]
	if cur.state == cardAlreadyOnPR {
		advCmd := m.advanceCard()
		m.rebuildBody()
		return m, advCmd
	}
	// Local pre-flight: if we couldn't anchor this finding to any hunk in the
	// parsed diff, GitHub's reviews/comments endpoints will reject it with
	// "pull_request_review_thread.line could not be resolved". Catch it here
	// so the user gets an actionable, local explanation instead of a 422.
	if !m.dryRun && cur.hunk == nil {
		cur.state = cardError
		cur.err = fmt.Errorf("can't post: %s:%d isn't on a hunk in the current PR diff (line may have moved or been removed). Press R to refresh the PR or s to skip this finding.",
			cur.finding.Finding.Path, cur.finding.Finding.Line)
		m.rebuildBody()
		return m, nil
	}
	cmd := postSingleFindingCmd(m.draft.Ref, m.draft.PR, cur.finding.Specialist, cur.finding.Finding, m.dryRun)
	return m, cmd
}

// actRefreshPR re-fetches the PR view (head SHA) and unified diff so the
// overlay can re-anchor each pending card to the new diff. It's the recovery
// path for "PR head moved" and "line could not be resolved" errors.
func (m *reviewOverlay) actRefreshPR() (tea.Model, tea.Cmd) {
	if m.refreshing || m.draft == nil {
		return m, nil
	}
	m.refreshing = true
	m.refreshNote = ""
	m.rebuildBody()
	return m, refreshPRCmd(m.draft.Ref)
}

// applyPRRefresh adopts a freshly fetched PR + diff, re-anchors every pending
// approval card to the new diff, and clears any stale per-card / summary
// error state so the user can immediately retry. Findings whose original line
// is no longer on a hunk are flagged as cardError with a local message —
// GitHub would reject those anyway.
func (m *reviewOverlay) applyPRRefresh(pr *gh.PR, diff string) {
	m.refreshing = false
	if m.draft == nil || pr == nil {
		return
	}
	wasSHA := ""
	if m.draft.PR != nil {
		wasSHA = m.draft.PR.HeadSHA
	}
	m.draft.PR = pr
	m.draft.Diff = diff
	m.files = review.ParseDiff(diff)

	unanchored := 0
	for i := range m.cards {
		c := &m.cards[i]
		// Re-anchor every card. Skip cards already posted / already on PR /
		// explicitly skipped — those don't need a new hunk.
		switch c.state {
		case cardPosted, cardSkipped, cardAlreadyOnPR:
			c.file = review.FindFile(m.files, c.finding.Finding.Path)
			if c.file != nil {
				c.hunk, _ = review.HunkAroundLine(c.file, c.finding.Finding.Line)
			} else {
				c.hunk = nil
			}
			continue
		}
		c.file = review.FindFile(m.files, c.finding.Finding.Path)
		if c.file != nil {
			c.hunk, _ = review.HunkAroundLine(c.file, c.finding.Finding.Line)
		} else {
			c.hunk = nil
		}
		// Reset transient error state — the user is retrying after a refresh.
		if c.state == cardError {
			c.state = cardPending
			c.err = nil
		}
		if c.hunk == nil {
			unanchored++
		}
	}
	// Reset the summary-phase error so the user can re-attempt the post.
	m.summaryErr = nil

	switch {
	case wasSHA != "" && wasSHA != pr.HeadSHA && unanchored > 0:
		m.refreshNote = fmt.Sprintf("PR refreshed · head %s → %s · %d finding(s) no longer anchor to a hunk on the new diff (skip them or edit on GitHub).",
			shortSHA(wasSHA), shortSHA(pr.HeadSHA), unanchored)
	case wasSHA != "" && wasSHA != pr.HeadSHA:
		m.refreshNote = fmt.Sprintf("PR refreshed · head %s → %s · all findings still anchor to the new diff.",
			shortSHA(wasSHA), shortSHA(pr.HeadSHA))
	case unanchored > 0:
		m.refreshNote = fmt.Sprintf("PR refreshed · head unchanged · %d finding(s) don't anchor to a hunk on the current diff.", unanchored)
	default:
		m.refreshNote = "PR refreshed · head unchanged · all findings re-anchored."
	}
	m.rebuildBody()
}

// shortSHA returns the first 7 chars of a SHA, or s if shorter.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func (m *reviewOverlay) actSkipCurrent() (tea.Model, tea.Cmd) {
	if m.peruse {
		return m.flashPeruse("peruse mode — no skipping; use ←/→ to navigate, f to jump to summary, q to exit")
	}
	if m.idx >= len(m.cards) {
		return m, nil
	}
	if m.cards[m.idx].state == cardAlreadyOnPR {
		advCmd := m.advanceCard()
		m.rebuildBody()
		return m, advCmd
	}
	m.cards[m.idx].state = cardSkipped
	advCmd := m.advanceCard()
	m.rebuildBody()
	return m, advCmd
}

func (m *reviewOverlay) actNext() (tea.Model, tea.Cmd) {
	if m.idx < len(m.cards)-1 {
		m.idx++
		m.vp.GotoTop()
		m.rebuildBody()
	}
	return m, nil
}

func (m *reviewOverlay) actPrev() (tea.Model, tea.Cmd) {
	if m.idx > 0 {
		m.idx--
		m.vp.GotoTop()
		m.rebuildBody()
	}
	return m, nil
}

func (m *reviewOverlay) actPostSummary() (tea.Model, tea.Cmd) {
	if m.peruse {
		return m.flashPeruse("peruse mode — no posting; q to exit without sending anything")
	}
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	m.syncUserSkipsToDraft()
	// The summary phase posts a body-only review with the verdict event
	// (REQUEST_CHANGES or COMMENT). The approve verdict is handled by
	// actPostApprove which lives in its own phase.
	return m, postReviewWithVerdictCmd(m.draft.Ref, m.draft, m.dryRun, "")
}

// actPostApprove posts a GitHub review with event=APPROVE and an empty body.
// Reachable only from phaseConfirmApprove (verdict=approve, user clicks Approve).
func (m *reviewOverlay) actPostApprove() (tea.Model, tea.Cmd) {
	if m.peruse {
		return m.flashPeruse("peruse mode — no approving; q to exit without sending anything")
	}
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	return m, postReviewWithVerdictCmd(m.draft.Ref, m.draft, m.dryRun, "APPROVE")
}

// flashPeruse records a one-frame help-line hint to surface why an
// action key was ignored in peruse mode, then triggers a rebuild so
// the hint is visible. Returns the no-op (m, nil) tuple every caller
// uses, so it's an inline-friendly bail-out.
func (m *reviewOverlay) flashPeruse(hint string) (tea.Model, tea.Cmd) {
	m.peruseHint = hint
	m.rebuildBody()
	return m, nil
}

// summaryPhaseOfferApproveWithoutSummary reports whether we should offer GitHub
// APPROVE with an empty body from the summary step. That applies when the merge
// verdict is not already APPROVE and this session posted no inline comments —
// e.g. the user skipped every suggestion, had no postable inlines, or only
// findings already on the PR.
func (m *reviewOverlay) summaryPhaseOfferApproveWithoutSummary() bool {
	if m.peruse {
		// Peruse never offers approval shortcuts — the whole point is
		// "look without committing".
		return false
	}
	if m.draft == nil || m.draft.PR == nil {
		return false
	}
	if m.draft.PostEvent() == "APPROVE" {
		return false
	}
	onPR, sessPosted, skippedOnly := m.tallyCardKinds()
	if sessPosted > 0 {
		return false
	}
	if len(m.cards) == 0 {
		return true
	}
	return skippedOnly > 0 || onPR+skippedOnly == len(m.cards)
}

// reviewOverlayCloseMsg is the signal the overlay sends to the root model when
// the user is done (or chose to abort). Root then pops the stack.
type reviewOverlayCloseMsg struct{}

func (m *reviewOverlay) mergeProgress(p review.Progress) tea.Cmd {
	switch p.Stage {
	case "checkout":
		if p.Err != nil {
			m.log = append(m.log, "checkout: "+p.Err.Error())
		} else {
			m.log = append(m.log, "worktree: "+p.Detail)
		}
	case "diff":
		if p.Err != nil {
			m.log = append(m.log, "diff: "+p.Err.Error())
		} else {
			m.log = append(m.log, "diff: "+p.Detail)
		}
	case "repo-context":
		m.log = append(m.log, "repo context: "+p.Detail)
	case "context-summary":
		m.log = append(m.log, "context vs change: "+p.Detail)
	case "specialist":
		// runner emits "<name>:start", "<name>:done", or "<name>:retry N (...)".
		parts := strings.SplitN(p.Detail, ":", 2)
		if len(parts) != 2 {
			return nil
		}
		name, detail := parts[0], parts[1]
		m.applyAgentDetail(name, detail, p)
	case "vibe-coach":
		// runner emits Detail = "start" / "done" / "retry N (...)".
		m.applyAgentDetail(review.SpecVibeCoach, p.Detail, p)
	case "repo-arbiter":
		// runner emits Detail = "start" / "done" / "skipped".
		m.applyAgentDetail(p.Stage, p.Detail, p)
	case "fetch-pr":
		if p.Err != nil {
			m.log = append(m.log, "fetch PR: "+p.Err.Error())
		}
	case "done":
		// Root model receives the same progress message and sets m.draft. The
		// overlay also adopts it directly so we can compute approval cards.
		if p.Final != nil {
			adoptCmd := m.adoptDraft(p.Final)
			fetchCmd := m.cmdAfterAdoptIfNeeded()
			return tea.Batch(adoptCmd, fetchCmd)
		}
	}
	return nil
}

// applyAgentDetail handles the "start", "done", and "retry N (...)" sub-states
// uniformly across every agent type. It does NOT demote other running agents.
// Multiple agents may be running when parallel dispatch is enabled in repo-context.json.
func (m *reviewOverlay) applyAgentDetail(name, detail string, p review.Progress) {
	i := m.agentIndex(name)
	if i < 0 {
		return
	}
	row := &m.agents[i]
	switch {
	case detail == "start":
		row.phase = oaRunning
		row.startedAt = time.Now()
		row.finishedAt = time.Time{}
		row.err = nil
		// Move keyboard focus to the most recently started agent so j/k
		// hovering tracks "what just happened", but don't override the user's
		// explicit selection if they've pressed j/k already.
		if m.cursor < 0 || m.cursor >= len(m.agents) {
			m.cursor = i
		}
	case detail == "skipped":
		now := time.Now()
		row.phase = oaSkipped
		row.startedAt = now
		row.finishedAt = now
		row.err = nil
	case detail == "deferred":
		// Vibe-coach is run lazily at the approve→summary transition
		// (see enterSummary). The runner emits this marker so the row
		// shows a "deferred" pill instead of staying on pending — making
		// it clear to the user that nothing failed; we're just waiting
		// to see their final skip decisions before synthesizing prompts.
		now := time.Now()
		row.phase = oaSkipped
		row.startedAt = now
		row.finishedAt = now
		row.err = nil
		row.summary = "deferred until approve → summary (runs against your final skip set)"
	case detail == "done":
		row.finishedAt = time.Now()
		row.expanded = false
		switch {
		case p.Result != nil && p.Result.Err != nil:
			row.phase = oaErr
			row.err = p.Result.Err
		case p.Result != nil:
			row.phase = oaDone
			row.summary = p.Result.Summary
			row.findingsN = len(p.Result.Findings)
		case p.Vibe != nil && p.Vibe.Err != nil:
			row.phase = oaErr
			row.err = p.Vibe.Err
		case p.Vibe != nil:
			row.phase = oaDone
			row.summary = p.Vibe.Summary
			row.findingsN = len(p.Vibe.Prompts)
		case p.Arbiter != nil && p.Arbiter.Err != nil:
			row.phase = oaErr
			row.err = p.Arbiter.Err
		case p.Arbiter != nil:
			row.phase = oaDone
			row.summary = formatArbiterRowSummary(p.Arbiter)
			row.findingsN = len(p.Arbiter.Suppressed) + len(p.Arbiter.Demoted)
		default:
			row.phase = oaDone
		}
	case strings.HasPrefix(detail, "retry"):
		// detail looks like "retry 2 (parse specialist output: ...)"
		row.retries++
		row.lastRetry = strings.TrimSpace(detail)
	}
}

func (m *reviewOverlay) agentIndex(name string) int {
	for i := range m.agents {
		if m.agents[i].name == name {
			return i
		}
	}
	return -1
}

func (m *reviewOverlay) rebuildBody() {
	switch m.phase {
	case phaseRunning:
		m.vp.SetContent(enforceMaxLineWidth(m.renderRunningBody(), m.vp.Width))
	case phaseApprove:
		m.vp.SetContent(enforceMaxLineWidth(m.renderApprovalBody(), m.vp.Width))
	case phaseGeneratingSummary:
		m.vp.SetContent(enforceMaxLineWidth(m.renderGeneratingSummaryBody(), m.vp.Width))
	case phaseSummary:
		m.vp.SetContent(enforceMaxLineWidth(m.renderSummaryBody(), m.vp.Width))
	case phaseConfirmApprove:
		m.vp.SetContent(enforceMaxLineWidth(m.renderConfirmApproveBody(), m.vp.Width))
	case phasePosted:
		m.vp.SetContent(enforceMaxLineWidth(m.renderPostedBody(), m.vp.Width))
	}
}

// renderGeneratingSummaryBody is the brief interstitial shown while the
// deferred vibe-coach call is in flight. Keeps the user oriented (they
// just pressed "finish" or the last card just resolved) and explains
// why the summary isn't instant.
func (m *reviewOverlay) renderGeneratingSummaryBody() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("Refining summary with your final selections…"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Vibe-coach is re-reading the findings you kept and writing fix-prompts that only cover those. This usually takes a few seconds."))
	b.WriteString("\n\n")
	if m.coachErr != nil {
		b.WriteString(errStyle.Render("Previous run failed: " + m.coachErr.Error()))
		b.WriteString("\n\n")
	}
	_, posted, skipped := m.tallyCardKinds()
	total := len(m.cards)
	if total > 0 {
		kept := total - skipped
		b.WriteString(dimStyle.Render(fmt.Sprintf("Findings: %d kept · %d skipped · %d posted of %d card(s)", kept, skipped, posted, total)))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *reviewOverlay) renderRunningBody() string {
	var b strings.Builder
	rowW := max(8, m.vp.Width)
	bodyIndentW := max(8, m.vp.Width-6)

	// Top status strip: total elapsed + counts. Updates on every spinner tick
	// because rebuildBody runs per-tick during phaseRunning.
	doneN, runningN, failedN := m.countAgents()
	totalN := len(m.agents)
	elapsed := humanElapsed(time.Since(m.runStartedAt))
	stageActive := m.activeStageGroup()
	stageActiveLabel := ""
	if meta, ok := stageGroupMetas[stageActive]; ok && runningN > 0 {
		stageActiveLabel = meta.label
	}
	headline := fmt.Sprintf("%s of %d agents complete  ·  %s elapsed",
		boldStyle.Render(fmt.Sprintf("%d", doneN)), totalN, elapsed)
	if runningN > 0 && stageActiveLabel != "" {
		headline += "  ·  now: " + boldStyle.Render(stageActiveLabel)
	}
	if failedN > 0 {
		headline += "  ·  " + errStyle.Render(fmt.Sprintf("%d failed", failedN))
	}
	b.WriteString(headline + "\n")
	b.WriteString(renderProgressBar(doneN, totalN, max(20, rowW/2)) + "\n\n")

	// Stage groups in pipeline order. Each group prints a header (with its
	// own done/total) and then its rows, indented by 2 cells.
	for gi, sg := range stageGroupOrder {
		meta := stageGroupMetas[sg]
		note := meta.note
		switch sg {
		case stageGroupSpecialists:
			note = m.specialistsStageNote()
		case stageGroupExperts:
			note = m.repoExpertsStageNote()
		}
		rows := m.agentsInStage(sg)
		gDone, gRunning, gFailed := stageCounts(rows)

		// Group header — chevron reflects state, label bold when running.
		chev, state := stageChevronAndState(rows, gRunning, gDone, gFailed, len(rows))
		labelStyle := dimStyle
		if gRunning > 0 {
			labelStyle = boldStyle
		}
		header := fmt.Sprintf("%s %s  %s  %s",
			chev,
			labelStyle.Render(meta.label),
			dimStyle.Render("· "+note),
			state)
		b.WriteString(header + "\n")

		// Agent rows.
		for _, row := range rows {
			i := m.agentIndex(row.name)
			b.WriteString(m.renderAgentRow(i, &m.agents[i], rowW, bodyIndentW))
		}
		if gi < len(stageGroupOrder)-1 {
			b.WriteString("\n")
		}
	}

	// Recent log tail — capped so it doesn't push the agents off-screen on
	// short overlays. The newest line is last.
	if len(m.log) > 0 {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("recent log") + "\n")
		const maxLog = 4
		recent := m.log
		if len(recent) > maxLog {
			recent = recent[len(recent)-maxLog:]
		}
		for _, line := range recent {
			for _, wl := range strings.Split(wrapForViewport(dimStyle.Render("  · ")+line, rowW), "\n") {
				b.WriteString(wl + "\n")
			}
		}
	}
	return b.String()
}

// formatPriorActivityLog renders a one-line "tool ran here before" entry
// for the running view's log stream. Counts plus a "last review: 2h ago"
// timer so the user can tell whether this is the first refresh or the
// tenth.
func formatPriorActivityLog(p gh.PriorAprrAISalActivity) string {
	parts := []string{"⟳ appr-ai-sal has reviewed this PR before"}
	if p.InlineCount > 0 {
		parts = append(parts, fmt.Sprintf("%d inline", p.InlineCount))
	}
	if p.ReviewCount > 0 {
		parts = append(parts, fmt.Sprintf("%d review body", p.ReviewCount))
	}
	if !p.LastAt.IsZero() {
		parts = append(parts, "last "+humanElapsed(time.Since(p.LastAt))+" ago")
	}
	return strings.Join(parts, " · ")
}

// formatPriorActivityBanner renders the multi-line acknowledgement panel
// shown above the approval card list and at the top of the post-summary
// screen when prior appr-ai-sal activity is detected on the PR. The
// banner is informational only — the new run still proceeds normally.
func formatPriorActivityBanner(p gh.PriorAprrAISalActivity) string {
	if !p.Found() {
		return ""
	}
	var b strings.Builder
	header := boldStyle.Render("Note: appr-ai-sal has reviewed this PR before.")
	b.WriteString(header + "\n")
	var bits []string
	if p.InlineCount > 0 {
		bits = append(bits, fmt.Sprintf("%d inline comment(s)", p.InlineCount))
	}
	if p.ReviewCount > 0 {
		bits = append(bits, fmt.Sprintf("%d review body(s)", p.ReviewCount))
	}
	when := ""
	if !p.LastAt.IsZero() {
		when = " · last " + humanElapsed(time.Since(p.LastAt)) + " ago"
	}
	if len(bits) > 0 || when != "" {
		b.WriteString(dimStyle.Render(strings.Join(bits, " · ") + when))
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(p.LastSummarySnippet); s != "" {
		b.WriteString(dimStyle.Render("Last review snippet: ") + s + "\n")
	}
	b.WriteString(dimStyle.Render("This run is a refresh — duplicate inline comments will be marked \"already on PR\" below."))
	return b.String()
}

// formatArbiterRowSummary turns the repo-arbiter's structured result into a
// short multi-line block we put under the row when the user expands it.
// Returns "" when the arbiter ran but had nothing to say — the row then
// renders without an expand chevron (see hasExpandableContent).
//
// The output is shaped as Markdown (paragraphs separated by blank lines,
// `**bold**` labels, `- ` bullet list for the rationale) so the agent-row
// renderer can run it through glamour and the reviewer sees rendered
// prose instead of structured-looking literal text.
func formatArbiterRowSummary(arb *review.RepoArbiterResult) string {
	if arb == nil {
		return ""
	}
	var b strings.Builder
	if s := strings.TrimSpace(arb.UserSummary); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	if n := len(arb.Suppressed); n > 0 {
		fmt.Fprintf(&b, "Suppressed %d finding(s) as out-of-norm for this repo.\n\n", n)
	}
	if n := len(arb.Demoted); n > 0 {
		fmt.Fprintf(&b, "Demoted %d finding(s) one severity rank.\n\n", n)
	}
	if v := strings.TrimSpace(arb.EffectiveVerdict); v != "" {
		if vo := strings.TrimSpace(arb.VerdictOverride); vo != "" {
			fmt.Fprintf(&b, "**Verdict override:** %s.\n\n", vo)
		} else {
			fmt.Fprintf(&b, "**Verdict** (carried from vibe-coach): %s.\n\n", v)
		}
	}
	if n := len(arb.DroppedSuppressions); n > 0 {
		fmt.Fprintf(&b, "Dropped %d invalid suppression(s).\n\n", n)
	}
	if n := len(arb.DroppedDemotions); n > 0 {
		fmt.Fprintf(&b, "Dropped %d invalid demotion(s).\n\n", n)
	}
	if len(arb.RationaleBullets) > 0 {
		b.WriteString("**Rationale:**\n\n")
		for _, r := range arb.RationaleBullets {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", r)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// hasExpandableContent reports whether expanding this row would reveal
// anything (a summary, a retry detail, or an error). When false the
// renderer suppresses the chevron and click-to-expand zone so the row
// doesn't dangle a "▶" affordance that yields an empty body.
func hasExpandableContent(a *overlayAgentRow) bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(a.summary) != "" {
		return true
	}
	if a.phase == oaErr && a.err != nil {
		return true
	}
	if a.retries > 0 && strings.TrimSpace(a.lastRetry) != "" {
		return true
	}
	return false
}

// renderAgentRow renders one agent row (always one line, optionally followed
// by an expanded block). The status icon, agent badge, and right-side detail
// are all on the same line; bubblezone marks the whole row for click-to-expand.
func (m *reviewOverlay) renderAgentRow(i int, a *overlayAgentRow, rowW, bodyIndentW int) string {
	var b strings.Builder
	cursor := "  "
	if m.cursor == i {
		cursor = "> "
	}
	icon := agentStatusIcon(a)
	tag := renderTag(a.name)
	right := agentStatusDetail(a)
	chev := " "
	if hasExpandableContent(a) {
		chev = "▶"
		if a.expanded {
			chev = "▼"
		}
	}
	prefix := cursor + chev + " " + icon + " " + tag
	// pad to a stable column so the right-side details line up.
	const tagCol = 36
	prefixW := ansi.StringWidth(prefix)
	pad := tagCol - prefixW
	if pad < 1 {
		pad = 1
	}
	header := prefix + strings.Repeat(" ", pad) + right
	hdrLine := lipgloss.NewStyle().Width(rowW).Align(lipgloss.Left).Render(header)
	if ansi.StringWidth(hdrLine) > rowW {
		hdrLine = ansi.Truncate(hdrLine, rowW, "")
	}
	row := zone.Mark(zoneOverlayAgent(i), hdrLine)
	b.WriteString(row + "\n")
	if a.expanded {
		// Retry detail (most useful failure-mode signal during a long run).
		if a.retries > 0 && strings.TrimSpace(a.lastRetry) != "" {
			line := dimStyle.Render("    last retry: ") + a.lastRetry
			for _, wl := range strings.Split(wrapForViewport(line, bodyIndentW), "\n") {
				b.WriteString(wl + "\n")
			}
		}
		if a.phase == oaErr && a.err != nil {
			for _, wl := range strings.Split(wrapForViewport(errStyle.Render("    ")+a.err.Error(), bodyIndentW), "\n") {
				b.WriteString(wl + "\n")
			}
		}
		if strings.TrimSpace(a.summary) != "" {
			label := "Thoughts"
			if a.name == overlayAgentRepoArbiter {
				label = "Arbiter notes"
			} else if a.name == review.SpecVibeCoach {
				label = "Vibe coach summary"
			}
			b.WriteString(dimStyle.Render("    "+label) + "\n")
			// Agent summaries are model-produced markdown (or, for the
			// arbiter row, a markdown-shaped recap built from the result
			// struct). Render through glamour so headings, lists, and
			// fenced code blocks show as rendered text instead of raw
			// `**bold**` / `# heading` markup. extraIndent=2 pairs with
			// glamour's built-in 2-cell margin to land the body at the
			// same column as the "    Label" line above it.
			b.WriteString(renderMarkdownIndented(strings.TrimSpace(a.summary), bodyIndentW+4, 2) + "\n")
		}
	}
	return b.String()
}

// agentStatusIcon picks a one-cell glyph for the row's current state.
func agentStatusIcon(a *overlayAgentRow) string {
	switch a.phase {
	case oaRunning:
		return boldStyle.Render("⏱")
	case oaDone:
		return okStyle.Render("✓")
	case oaSkipped:
		return dimStyle.Render("⊘")
	case oaErr:
		return errStyle.Render("✗")
	}
	return dimStyle.Render("◌")
}

// agentStatusDetail renders the right-side detail for the row: elapsed time
// + result counts when applicable, with retry annotation when present.
func agentStatusDetail(a *overlayAgentRow) string {
	switch a.phase {
	case oaPending:
		return dimStyle.Render("queued")
	case oaRunning:
		base := boldStyle.Render("running") + dimStyle.Render(" · "+humanElapsed(time.Since(a.startedAt)))
		if a.retries > 0 {
			base += dimStyle.Render(" · retried " + fmt.Sprintf("%d×", a.retries))
		}
		return base
	case oaDone:
		dur := humanElapsed(a.finishedAt.Sub(a.startedAt))
		count := ""
		switch {
		case a.name == overlayAgentRepoArbiter:
			if a.findingsN == 0 {
				count = okStyle.Render("no actions")
			} else {
				count = okStyle.Render(fmt.Sprintf("%d action(s)", a.findingsN))
			}
		case a.findingsN > 0:
			count = okStyle.Render(fmt.Sprintf("%d finding(s)", a.findingsN))
		case a.findingsN == 0:
			count = okStyle.Render("clean")
		}
		base := count + dimStyle.Render(" · "+dur)
		if a.retries > 0 {
			base += dimStyle.Render(" · retried " + fmt.Sprintf("%d×", a.retries))
		}
		return base
	case oaSkipped:
		return dimStyle.Render("skipped · no specialist findings")
	case oaErr:
		dur := humanElapsed(a.finishedAt.Sub(a.startedAt))
		base := errStyle.Render("failed") + dimStyle.Render(" · "+dur)
		if a.retries > 0 {
			base += dimStyle.Render(" · gave up after " + fmt.Sprintf("%d×", a.retries))
		}
		return base
	}
	return ""
}

// humanElapsed renders a Duration as a short human string ("12s", "1m 30s").
// Negative durations and uninitialized starts (zero) fall back to "—" so the
// renderer can call this unconditionally without guarding every call site.
func humanElapsed(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm %02ds", mins, secs)
}

// renderProgressBar draws a single-row block bar with width cells filled in
// proportion to done/total. Uses ASCII so the cell count matches the terminal
// glyph count exactly.
func renderProgressBar(done, total, width int) string {
	if total <= 0 || width < 4 {
		return ""
	}
	filled := done * width / total
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	bar := okStyle.Render(strings.Repeat("█", filled)) + dimStyle.Render(strings.Repeat("░", width-filled))
	return bar + dimStyle.Render(fmt.Sprintf("  %d/%d", done, total))
}

// countAgents returns done, running, and failed counts across all agents.
func (m *reviewOverlay) countAgents() (done, running, failed int) {
	for _, a := range m.agents {
		switch a.phase {
		case oaDone, oaSkipped:
			done++
		case oaRunning:
			running++
		case oaErr:
			failed++
		}
	}
	return done, running, failed
}

// agentsInStage returns the agents belonging to the given stage group, in the
// order they appear in m.agents (which is also pipeline order).
func (m *reviewOverlay) agentsInStage(sg overlayAgentStage) []*overlayAgentRow {
	var out []*overlayAgentRow
	for i := range m.agents {
		if m.agents[i].stage == sg {
			out = append(out, &m.agents[i])
		}
	}
	return out
}

// activeStageGroup returns the first stage group with any running agent. If
// none are running, returns the first group that still has pending agents
// (the "next up" group). Falls back to specialists.
func (m *reviewOverlay) activeStageGroup() overlayAgentStage {
	for _, sg := range stageGroupOrder {
		rows := m.agentsInStage(sg)
		for _, r := range rows {
			if r.phase == oaRunning {
				return sg
			}
		}
	}
	for _, sg := range stageGroupOrder {
		rows := m.agentsInStage(sg)
		for _, r := range rows {
			if r.phase == oaPending {
				return sg
			}
		}
	}
	return stageGroupOrder[0]
}

// stageCounts returns done, running, failed for one stage's rows.
func stageCounts(rows []*overlayAgentRow) (done, running, failed int) {
	for _, r := range rows {
		switch r.phase {
		case oaDone, oaSkipped:
			done++
		case oaRunning:
			running++
		case oaErr:
			failed++
		}
	}
	return done, running, failed
}

// stageChevronAndState picks the visual marker and right-side state label
// for a stage header.
func stageChevronAndState(rows []*overlayAgentRow, running, done, failed, total int) (string, string) {
	switch {
	case running > 0:
		return boldStyle.Render("▶"), boldStyle.Render(fmt.Sprintf("running · %d/%d", done, total))
	case done == total && failed == 0 && total > 0:
		return okStyle.Render("✓"), okStyle.Render("done")
	case failed > 0 && (done+failed) == total:
		return errStyle.Render("✗"), errStyle.Render(fmt.Sprintf("done with %d failed", failed))
	case done > 0 || failed > 0:
		return dimStyle.Render("◐"), dimStyle.Render(fmt.Sprintf("%d/%d", done, total))
	default:
		return dimStyle.Render("◌"), dimStyle.Render("queued")
	}
}

func (m *reviewOverlay) renderApprovalBody() string {
	if m.pendingSuppressAck && m.draft != nil {
		rowW := max(8, m.vp.Width)
		allN := len(m.draft.FlatPostableFindings())
		postN := len(m.draft.FlatPostableFindingsForPost())
		var b strings.Builder
		if banner := formatPriorActivityBanner(m.priorActivity); banner != "" {
			b.WriteString(banner + "\n\n")
		}
		b.WriteString(errStyle.Render("Repo arbiter") + " will skip " + fmt.Sprintf("%d", allN-postN) +
			" of " + fmt.Sprintf("%d", allN) + " inline comment(s) (not in GitHub batch).\n\n")
		if ar := m.draft.RepoArbiter; ar != nil && len(ar.Demoted) > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("It also demoted %d finding(s) one severity rank.", len(ar.Demoted))) + "\n\n")
		}
		if len(m.draft.ConventionWitness) > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("Convention witness classified %d testing/docs finding(s) against the repo's evidence.", len(m.draft.ConventionWitness))) + "\n\n")
		}
		if ar := m.draft.RepoArbiter; ar != nil && strings.TrimSpace(ar.UserSummary) != "" {
			// Arbiter UserSummary is LLM-produced markdown; render it
			// so headings / lists / emphasis look like prose, not source.
			b.WriteString(renderMarkdownIndented(strings.TrimSpace(ar.UserSummary), rowW, 0) + "\n\n")
		}
		b.WriteString(boldStyle.Render("Press Enter or Space to acknowledge and continue") + " · q abort\n")
		return b.String()
	}
	if m.idx < 0 || m.idx >= len(m.cards) {
		return dimStyle.Render("(no findings to approve — press y to post the summary, or skip)")
	}
	rowW := max(8, m.vp.Width)
	cur := m.cards[m.idx]
	var b strings.Builder

	if banner := formatPriorActivityBanner(m.priorActivity); banner != "" {
		b.WriteString(banner + "\n\n")
	}

	if m.existingCommentsLoading {
		b.WriteString(dimStyle.Render("Checking GitHub for inline comments you already posted…") + "\n\n")
	}

	// Progress strip: posted/skipped/total
	onPR, posted, skipped := m.tallyCardKinds()
	progress := fmt.Sprintf("Approving %d of %d  ·  %s already on PR  ·  %s posted now  ·  %s skipped",
		m.idx+1, len(m.cards),
		okStyle.Render(fmt.Sprintf("%d", onPR)),
		okStyle.Render(fmt.Sprintf("%d", posted)),
		dimStyle.Render(fmt.Sprintf("%d", skipped)),
	)
	b.WriteString(boldStyle.Render(progress) + "\n\n")

	// Specialist + location
	b.WriteString(renderTag(cur.finding.Specialist) + "  ")
	loc := fmt.Sprintf("%s:%d", cur.finding.Finding.Path, cur.finding.Finding.Line)
	b.WriteString(boldStyle.Render(loc) + "  ")
	b.WriteString(renderSeverity(string(cur.finding.Finding.Severity)))
	if m.draft != nil {
		if orig, ok := m.draft.FindingOriginalSeverity(cur.finding.Specialist, cur.finding.Finding); ok {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("(demoted from %s by repo arbiter)", string(orig))))
		}
	}
	b.WriteString("\n\n")

	// Diff hunk preview
	if cur.hunk != nil {
		b.WriteString(dimStyle.Render("Diff context") + "\n")
		b.WriteString(renderHunkSnippet(cur.hunk, cur.finding.Finding.Line, 4, rowW))
		b.WriteString("\n")
	} else {
		b.WriteString(dimStyle.Render("(no diff hunk located for this line — finding will still post if accepted)") + "\n\n")
	}

	// Comment + suggestion preview
	b.WriteString(dimStyle.Render("Comment GitHub will post") + "\n")
	// The preview body is exactly what GitHub will receive — markdown
	// with an `aiCommentLead` paragraph and (optionally) a fenced
	// ```suggestion block. Run through glamour so the reviewer sees the
	// rendered shape (headings, lists, code-block padding) rather than
	// the literal Markdown source. 2-cell extra indent keeps the body
	// visually nested under the section header.
	preview := review.ReviewCommentBody(cur.finding.Specialist, cur.finding.Finding)
	b.WriteString(renderMarkdownIndented(preview, rowW, 2) + "\n")
	if reason := strings.TrimSpace(cur.finding.Finding.SuggestionStrippedReason); reason != "" {
		b.WriteString("  " + dimStyle.Render("(suggestion stripped: "+reason+")") + "\n")
	}
	if note := strings.TrimSpace(cur.finding.Finding.ActionabilityNote); note != "" {
		b.WriteString("  " + dimStyle.Render("("+note+")") + "\n")
	}
	b.WriteString("\n")

	// Status badge for current card
	switch cur.state {
	case cardAlreadyOnPR:
		b.WriteString(okStyle.Render("✓ already on pull request") + "\n\n")
	case cardPosted:
		b.WriteString(okStyle.Render("✓ posted") + "\n\n")
	case cardSkipped:
		b.WriteString(dimStyle.Render("— skipped") + "\n\n")
	case cardError:
		if cur.err != nil {
			b.WriteString(renderPostErrorBlock(cur.err, rowW))
		} else {
			b.WriteString(errStyle.Render("✗ failed") + "\n\n")
		}
	}
	if m.refreshing {
		b.WriteString(dimStyle.Render("⟳ refreshing PR…") + "\n\n")
	} else if m.refreshNote != "" {
		b.WriteString(okStyle.Render("✓ "+m.refreshNote) + "\n\n")
	}

	// Action row
	left := zone.Mark(ZoneStagedPrev, dimStyle.Render(" ← prev "))
	right := zone.Mark(ZoneStagedNext, dimStyle.Render(" next → "))
	post := zone.Mark(ZoneStagedPost, okStyle.Render(" Post (y) "))
	skip := zone.Mark(ZoneStagedSkip, dimStyle.Render(" Skip (n) "))
	finish := zone.Mark(ZoneStagedFinish, boldStyle.Render(" Skip rest, post summary (f) "))
	quit := zone.Mark(ZoneStagedQuit, errStyle.Render(" Abort (q) "))
	row := strings.Join([]string{left, post, skip, right, finish, quit}, "  ")
	b.WriteString(lipgloss.NewStyle().Width(rowW).Render(row))
	b.WriteString("\n")
	if m.dryRun {
		b.WriteString("\n" + errStyle.Render("DRY-RUN") + dimStyle.Render(" — Post shows the GitHub payload only; nothing is sent.") + "\n")
	}
	return b.String()
}

// renderPostErrorBlock formats a post-time error so the user can see
// (a) what GitHub said, plus parsed validation details when available,
// (b) a one-line "what to do" hint when we recognise the cause, and
// (c) a clickable "[R] Refresh PR & retry" button.
//
// width is the viewport content width used for soft wrapping.
func renderPostErrorBlock(err error, width int) string {
	if err == nil {
		return ""
	}
	var b strings.Builder
	body := err.Error()
	for _, line := range strings.Split(body, "\n") {
		for _, wl := range strings.Split(wrapForViewport(line, width), "\n") {
			b.WriteString(errStyle.Render("✗ "+wl) + "\n")
		}
	}
	// Hint banner (only when we recognise the cause).
	if _, ok := gh.IsHeadDrift(err); ok {
		b.WriteString(dimStyle.Render("→ press R or click below to refresh the PR & retry") + "\n")
	} else if gh.IsLineUnresolvable(err) {
		b.WriteString(dimStyle.Render("→ GitHub couldn't anchor the comment to the diff. Press R to refresh and retry, or s to skip this finding.") + "\n")
	}
	refresh := zone.Mark(ZoneStagedRefresh, boldStyle.Render(" Refresh PR & retry (R) "))
	b.WriteString("\n" + refresh + "\n\n")
	return b.String()
}

func renderVerdictBanner(canonical, shortLabel string, maxW int) string {
	var border lipgloss.Color
	switch canonical {
	case review.VibeVerdictApprove:
		border = lipgloss.Color("#9ECE6A")
	case review.VibeVerdictRequestChanges:
		border = lipgloss.Color("#E0AF68")
	case review.VibeVerdictComment:
		border = lipgloss.Color("#888888")
	default:
		border = lipgloss.Color("#888888")
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		dimStyle.Render("Merge recommendation · vibe-coach"),
		"",
		boldStyle.Render(shortLabel),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(max(20, maxW-4))
	return box.Render(inner)
}

func (m *reviewOverlay) renderSummaryBody() string {
	rowW := max(8, m.vp.Width)
	var b strings.Builder
	b.WriteString(boldStyle.Render("Final step — post the review summary") + "\n\n")
	if banner := formatPriorActivityBanner(m.priorActivity); banner != "" {
		b.WriteString(banner + "\n\n")
	}
	if m.draft != nil {
		m.syncUserSkipsToDraft()
		// Use the reconciled verdict so the banner matches the GitHub event
		// PostEvent will actually send (request_changes can downgrade to
		// comment when the user skipped every blocker).
		v := review.NormalizeVibeVerdict(m.draft.ReconciledMergeVerdict())
		if lbl := review.VibeVerdictShortLabel(v); lbl != "" {
			b.WriteString(renderVerdictBanner(v, lbl, rowW))
			b.WriteString("\n\n")
		}
	}
	onPR, sessPosted, skippedOnly := m.tallyCardKinds()
	b.WriteString(fmt.Sprintf("Inline comments: %s already on PR, %s posted this session, %s skipped (%d total)\n\n",
		okStyle.Render(fmt.Sprintf("%d", onPR)),
		okStyle.Render(fmt.Sprintf("%d", sessPosted)),
		dimStyle.Render(fmt.Sprintf("%d", skippedOnly)),
		len(m.cards)))

	if m.summaryPhaseOfferApproveWithoutSummary() {
		b.WriteString(dimStyle.Render("You have not posted any inline comments this session. Submit GitHub APPROVE with an empty body (a) to approve without publishing the summary below, or post the summary as usual (y).") + "\n\n")
	}

	if m.draft == nil {
		b.WriteString(errStyle.Render("(no draft loaded)") + "\n")
		return b.String()
	}
	body := m.draft.RenderBody()
	b.WriteString(dimStyle.Render("Markdown body GitHub will publish (scroll with wheel or ↑/↓):") + "\n")
	b.WriteString("\n")
	// The body is markdown that's about to be POSTed to GitHub as-is; render
	// it through glamour so the reviewer previews approximately what the PR
	// author will see (headings, lists, fenced code blocks) rather than the
	// raw source. The 2-column extra indent keeps it visually grouped under
	// the header line above.
	b.WriteString(renderMarkdownIndented(body, rowW, 2) + "\n")

	if m.summaryDryMsg != "" {
		b.WriteString("\n" + okStyle.Render("✓ "+m.summaryDryMsg) + "\n")
	}
	if m.summaryErr != nil {
		b.WriteString("\n" + renderPostErrorBlock(m.summaryErr, rowW))
	}
	if m.refreshing {
		b.WriteString("\n" + dimStyle.Render("⟳ refreshing PR…") + "\n")
	} else if m.refreshNote != "" {
		b.WriteString("\n" + okStyle.Render("✓ "+m.refreshNote) + "\n")
	}
	b.WriteString("\n")
	yes := zone.Mark(ZoneStagedSummaryYes, okStyle.Render(" Post summary (y) "))
	no := zone.Mark(ZoneStagedSummaryNo, dimStyle.Render(" Skip summary (n) "))
	approveOnly := ""
	if m.summaryPhaseOfferApproveWithoutSummary() {
		approveOnly = zone.Mark(ZoneStagedSummaryApproveOnly, okStyle.Render(" Approve only (a) "))
	}
	q := zone.Mark(ZoneStagedQuit, errStyle.Render(" Abort (q) "))
	b.WriteString(yes + "  " + no)
	if approveOnly != "" {
		b.WriteString("  " + approveOnly)
	}
	b.WriteString("  " + q + "\n")
	if m.dryRun {
		b.WriteString("\n" + errStyle.Render("DRY-RUN") + dimStyle.Render(" — Post shows the body payload only.") + "\n")
	}
	return b.String()
}

// renderConfirmApproveBody is shown when the AI verdict is approve and the
// user has nothing left to walk: a single, deliberately small confirmation
// card. No markdown summary, no per-specialist recap — just an Approve button.
//
// The "no issues found" variant (m.noFindingsApprove) replaces the body with
// a clean "every agent passed" message and posts the rendered body so the
// GitHub APPROVE explains itself instead of looking like a content-free
// thumbs-up.
func (m *reviewOverlay) renderConfirmApproveBody() string {
	rowW := max(8, m.vp.Width)
	var b strings.Builder
	if m.draft != nil {
		b.WriteString(renderVerdictBanner(review.VibeVerdictApprove, "Approve", rowW))
		b.WriteString("\n\n")
	}

	if m.noFindingsApprove {
		b.WriteString(okStyle.Render("✓ No issues found by any agent.") + "\n\n")
		b.WriteString(dimStyle.Render("Every configured specialist reviewed this diff and produced no actionable feedback to leave on the diff or in a written summary.") + "\n\n")
		b.WriteString(boldStyle.Render("Submit GitHub APPROVE on this pull request?") + "\n\n")
		b.WriteString(dimStyle.Render("The review will be submitted with event ") +
			okStyle.Render("APPROVE") +
			dimStyle.Render(" and a brief body explaining no issues were found.") + "\n\n")

		yes := zone.Mark(ZoneStagedSummaryYes, okStyle.Render(" Approve PR (y) "))
		q := zone.Mark(ZoneStagedQuit, errStyle.Render(" Abort (q) "))
		b.WriteString(yes + "  " + q + "\n")
	} else {
		onPR, sessPosted, _ := m.tallyCardKinds()
		if len(m.cards) > 0 {
			b.WriteString(fmt.Sprintf("%s already on PR · %s posted this session.\n\n",
				okStyle.Render(fmt.Sprintf("%d", onPR)),
				okStyle.Render(fmt.Sprintf("%d", sessPosted))))
		} else {
			b.WriteString(dimStyle.Render("No inline findings to post.") + "\n\n")
		}
		if m.approveAfterSkipDisagree {
			b.WriteString(dimStyle.Render("You skipped every suggested inline comment — if you disagree with those objections, you can still submit an APPROVE here (or press n to post the written review instead).") + "\n\n")
		}
		b.WriteString(boldStyle.Render("Submit GitHub APPROVE on this pull request?") + "\n\n")
		b.WriteString(dimStyle.Render("The review will be submitted with event ") +
			okStyle.Render("APPROVE") +
			dimStyle.Render(" and an empty body — no summary text, no per-agent recap.") + "\n\n")

		yes := zone.Mark(ZoneStagedSummaryYes, okStyle.Render(" Approve PR (y) "))
		no := zone.Mark(ZoneStagedSummaryNo, dimStyle.Render(" No, leave a comment-only review (n) "))
		q := zone.Mark(ZoneStagedQuit, errStyle.Render(" Abort (q) "))
		b.WriteString(yes + "  " + no + "  " + q + "\n")
	}

	if m.summaryDryMsg != "" {
		b.WriteString("\n" + okStyle.Render("✓ "+m.summaryDryMsg) + "\n")
	}
	if m.summaryErr != nil {
		b.WriteString("\n" + renderPostErrorBlock(m.summaryErr, rowW))
	}
	if m.refreshing {
		b.WriteString("\n" + dimStyle.Render("⟳ refreshing PR…") + "\n")
	} else if m.refreshNote != "" {
		b.WriteString("\n" + okStyle.Render("✓ "+m.refreshNote) + "\n")
	}
	if m.dryRun {
		b.WriteString("\n" + errStyle.Render("DRY-RUN") + dimStyle.Render(" — Approve shows the GitHub payload only.") + "\n")
	}
	return b.String()
}

func (m *reviewOverlay) renderPostedBody() string {
	var b strings.Builder
	onPR, sessPosted, skippedOnly := m.tallyCardKinds()
	switch {
	case m.summarySkip:
		b.WriteString(boldStyle.Render("Done — summary not posted.") + "\n\n")
	case m.noFindingsApprove:
		b.WriteString(boldStyle.Render(okStyle.Render("✓ Approved — no issues found by any agent.")) + "\n\n")
	default:
		b.WriteString(boldStyle.Render(okStyle.Render("✓ Review posted to GitHub")) + "\n\n")
	}
	if !m.noFindingsApprove {
		b.WriteString(fmt.Sprintf("Inline comments: %s already on PR, %s posted this session, %s skipped (%d total)\n",
			okStyle.Render(fmt.Sprintf("%d", onPR)),
			okStyle.Render(fmt.Sprintf("%d", sessPosted)),
			dimStyle.Render(fmt.Sprintf("%d", skippedOnly)),
			len(m.cards)))
	}
	b.WriteString("\n")
	b.WriteString(zone.Mark(ZonePostedOK, dimStyle.Render(" Close (enter) ")) + "\n")
	return b.String()
}

func (m *reviewOverlay) tallyCardKinds() (onPR, posted, skipped int) {
	for _, c := range m.cards {
		switch c.state {
		case cardAlreadyOnPR:
			onPR++
		case cardPosted:
			posted++
		case cardSkipped:
			skipped++
		}
	}
	return onPR, posted, skipped
}

func (m *reviewOverlay) View() string {
	title := boldStyle.Render(m.titleForPhase()) + "  " + m.spinnerForPhase()
	help := dimStyle.Render(m.helpForPhase())
	body := lipgloss.JoinVertical(lipgloss.Left, title, "", m.vp.View(), "", help)
	return modalChrome.Width(m.outerW).Render(body)
}

func (m *reviewOverlay) titleForPhase() string {
	prefix := ""
	if m.peruse {
		prefix = "PERUSE · "
	}
	switch m.phase {
	case phaseRunning:
		return prefix + "Review in progress"
	case phaseApprove:
		if m.peruse {
			return prefix + "Browse findings"
		}
		return prefix + "Review · approve findings"
	case phaseGeneratingSummary:
		return prefix + "Review · refining summary"
	case phaseSummary:
		if m.peruse {
			return prefix + "Final summary preview"
		}
		return prefix + "Review · post summary"
	case phaseConfirmApprove:
		return prefix + "Review · approve PR"
	case phasePosted:
		return prefix + "Review complete"
	}
	return prefix + "Review"
}

func (m *reviewOverlay) spinnerForPhase() string {
	if m.phase == phaseRunning || m.phase == phaseGeneratingSummary {
		return m.sp.View()
	}
	return ""
}

func (m *reviewOverlay) helpForPhase() string {
	// Peruse-mode flash takes priority for one frame so the user sees
	// immediate feedback when they hit a disabled action key.
	if m.peruseHint != "" {
		hint := m.peruseHint
		m.peruseHint = ""
		return hint
	}
	peruseSuffix := ""
	if m.peruse {
		peruseSuffix = " · (peruse mode · no posting)"
	}
	switch m.phase {
	case phaseRunning:
		return "j/k focus row · space expand · q abort · ↑/↓ pgdn scroll · wheel" + peruseSuffix
	case phaseApprove:
		if m.peruse {
			return "←/→ prev/next · f jump to summary · R refresh PR · q exit · ↑/↓ scroll · wheel" + peruseSuffix
		}
		return "y post · n/s skip · ←/→ prev/next · R refresh PR · f skip-rest · q abort · wheel"
	case phaseGeneratingSummary:
		return "refining summary with your final selections… · q abort"
	case phaseSummary:
		if m.peruse {
			return "↑/↓ scroll preview · R refresh PR · q exit" + peruseSuffix
		}
		if m.summaryPhaseOfferApproveWithoutSummary() {
			return "y post summary · a approve without summary · R refresh PR · n skip · q abort · ↑/↓ scroll · wheel"
		}
		return "y post summary · R refresh PR · n skip · q abort · ↑/↓ scroll preview · wheel"
	case phaseConfirmApprove:
		if m.peruse {
			return "↑/↓ scroll · q exit" + peruseSuffix
		}
		if m.noFindingsApprove {
			return "y APPROVE · R refresh PR · q abort"
		}
		return "y APPROVE · n leave a comment-only review · R refresh PR · q abort"
	case phasePosted:
		return "enter close"
	}
	return ""
}

// MarkSummaryPosted is called by root after a successful (non-dry-run) summary
// post — moves into phasePosted with the success state.
func (m *reviewOverlay) MarkSummaryPosted() {
	m.summaryDone = true
	m.phase = phasePosted
	m.rebuildBody()
}

// MarkPostError records an error (for inline post, summary post, or approve confirmation).
func (m *reviewOverlay) MarkPostError(err error) {
	switch m.phase {
	case phaseApprove:
		if m.idx < len(m.cards) {
			m.cards[m.idx].state = cardError
			m.cards[m.idx].err = err
		}
	case phaseSummary, phaseConfirmApprove:
		m.summaryErr = err
	}
	m.rebuildBody()
}

// renderHunkSnippet draws a small diff window around the target line. Width
// is the viewport content width.
func renderHunkSnippet(h *review.Hunk, target, window, width int) string {
	if h == nil {
		return ""
	}
	lines := review.HunkSnippet(h, target, window)
	var b strings.Builder
	for _, ln := range lines {
		// Build a 6-char gutter: "  NNN " or "+/- NNN" with the correct sign.
		var gutter, body string
		switch ln.Kind {
		case review.DiffAdded:
			gutter = sevWarning.Render("+ ")
			body = ln.Text
		case review.DiffRemoved:
			gutter = sevError.Render("- ")
			body = ln.Text
		case review.DiffNoNewline:
			gutter = dimStyle.Render("  ")
			body = ln.Text
		default:
			gutter = dimStyle.Render("· ")
			body = ln.Text
		}
		focus := ""
		if ln.NewNo == target && ln.Kind != review.DiffRemoved {
			focus = boldStyle.Render(" ◀ here")
		}
		full := gutter + body + focus
		for _, wl := range strings.Split(wrapForViewport(full, width), "\n") {
			b.WriteString(wl + "\n")
		}
	}
	return b.String()
}

func (m *reviewOverlay) RebuildIfVisible() {
	m.rebuildBody()
}

// SetDryRun lets root toggle dry-run after the overlay was constructed (rare).
func (m *reviewOverlay) SetDryRun(b bool) { m.dryRun = b }

// Phase returns the current phase. Useful for root to decide message routing.
func (m *reviewOverlay) Phase() overlayPhase { return m.phase }

// HasDraft reports whether the overlay has adopted a draft (running phase has
// finished). Root uses this to decide whether to forward stagedFindingPostedMsg.
func (m *reviewOverlay) HasDraft() bool { return m.draft != nil }

// Draft returns the underlying draft (may be nil during running phase).
func (m *reviewOverlay) Draft() *review.Draft { return m.draft }

// Ref returns the gh.Ref for the in-flight review (zero value if none).
func (m *reviewOverlay) Ref() gh.Ref {
	if m.draft == nil {
		return gh.Ref{}
	}
	return m.draft.Ref
}
