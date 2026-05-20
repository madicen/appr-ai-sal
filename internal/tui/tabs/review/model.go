package review

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"

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
	stageGroupContextInjection overlayAgentStage = iota
	stageGroupSpecialists
	stageGroupExperts
	stageGroupArbiter
	stageGroupVibe
)

// stageGroupOrder is the rendering order top-to-bottom. It also reflects
// runtime ordering: context injection (lang briefs / tech experts /
// repo experts feed every specialist), then specialists, then repo
// arbiter, then vibe-coach. Specialists may run sequentially or in
// parallel (repo-context.json).
var stageGroupOrder = []overlayAgentStage{
	stageGroupContextInjection,
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
	stageGroupContextInjection: {label: "Context injection", note: "before specialists"},
	stageGroupSpecialists:      {label: "Specialists", note: ""},
	stageGroupExperts:          {label: "Repo experts", note: ""}, // unused after repo-agents refactor; kept to satisfy the enum
	stageGroupArbiter:          {label: "Repo arbiter", note: "after specialists"},
	stageGroupVibe:             {label: "Vibe coach", note: "after repo arbiter"},
}

// Agent name constants for the synthetic overlay rows. Specialist rows
// use the specialist name directly (review.SpecFormatting etc.); the
// rows below are the non-specialist rows the runner drives via stages
// other than "specialist".
const (
	overlayAgentRepoArbiter = "repo-arbiter"
	// Context-injection rows: each resolves from the matching runner
	// progress stage (lang-agents / tech-agents / repo-agents) before
	// any specialist starts.
	overlayAgentLangBriefs  = "language-briefs"
	overlayAgentTechExperts = "tech-experts"
	overlayAgentRepoExperts = "repo-experts"
)

// overlayAgentLabels returns the human-friendly title for synthetic
// overlay rows whose name is not itself a specialist key. Falls back to
// the raw name.
func overlayAgentLabel(name string) string {
	switch name {
	case overlayAgentLangBriefs:
		return "Language briefs"
	case overlayAgentTechExperts:
		return "Tech experts"
	case overlayAgentRepoExperts:
		return "Repo experts"
	case overlayAgentRepoArbiter:
		return "Repo arbiter"
	}
	return name
}

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
	// phaseGeneratingSummary is a short interstitial that re-runs
	// vibe-coach against the user's final skip set when it differs
	// from the pipeline-time set. Vibe-coach normally runs as part of
	// the review pipeline, so most users go straight from approve to
	// summary; this phase only appears when the user actually skipped
	// (or unskipped) findings before posting.
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
	// anchorRelocatedFrom is the finding's ORIGINAL Line before the
	// overlay re-anchored this card via review.FindUniqueExcerptInFile
	// (triggered when the original line fell outside any hunk in the
	// current diff but the model's AnchorExcerpt uniquely matched a
	// different line). Zero when no relocation happened. The card's
	// finding.Finding.Line is mutated to the new line so the post
	// payload uses the corrected anchor; this field exists purely to
	// surface a "auto-corrected from N → M" banner so the reviewer can
	// sanity-check the new position before posting.
	anchorRelocatedFrom int
	// fileLevelPost flips to true when the reviewer pressed F on a
	// cardError state to escape with a file-level GitHub comment for
	// this finding (no line/side anchor). The post command branches on
	// this; the resulting comment shows up on the PR's "Files changed"
	// tab attached to the file header rather than inline at a line.
	fileLevelPost bool
}

// Model is the persistent overlay that hosts the entire review flow:
// running → approve findings one-by-one → confirm summary post → posted.
type Model struct {
	outerW int
	outerH int
	vp     viewport.Model
	sp     spinner.Model
	// chromeMinimized mirrors bubble-overlay's LayerState.Minimized so
	// OverlayTitle() can shape the tab string for the current visual
	// state. The lib pushes this in via OnOverlayMinimize when the user
	// clicks the [-]/[+] toggle; we don't try to read it back out of
	// the stack because the model has no handle on its own LayerState.
	chromeMinimized bool
	agents          []overlayAgentRow
	log             []string
	cursor          int
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
	// demoMode mirrors model.Options.Demo. We thread it down to every
	// data.*Cmd call this overlay makes (RefreshPRCmd,
	// PostReviewWithVerdictCmd, PostApproveBareCmd,
	// FetchExistingPRCommentsCmd) so demo recordings stay self-contained
	// even when the user navigates into the persistent review overlay.
	demoMode bool

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

	// refreshing is true while a data.RefreshPRCmd is in flight. It disables the
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
	// coachErr is the most recent error from a TUI-triggered vibe-coach
	// re-run (after the user changed skips), surfaced in the summary
	// header so the user knows the summary they're seeing may be stale
	// or missing fix-prompts.
	coachErr error
}

func zoneOverlayAgent(i int) string {
	return fmt.Sprintf("zone:overlay:agent:%d", i)
}

// New builds a fresh review overlay. cfg is used to
// re-run vibe-coach lazily when the user changes their skip set
// between approve and summary; pass nil in tests that don't exercise
// that path (the overlay then skips the LLM re-run and lands directly
// in phaseSummary with whatever the draft already has).
func New(screenW, screenH int, dryRun bool, specialistsParallel, repoExpertsParallel bool, cfg *aiconfig.Config, demoMode bool) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	// Start with a 1×1 viewport that resizeFromScreen replaces below —
	// keeping the actual size math in one place (setOuterDims) means
	// New, tea.WindowSizeMsg, and OnOverlayResize all agree on inner
	// dims without three separate copies of the chrome-overhead
	// arithmetic.
	vp := viewport.New(1, 1)
	vp.MouseWheelEnabled = true
	// Build the agent rows in pipeline order. The running view groups them by
	// stage (context injection → specialists → arbiter → vibe). Each row
	// tracks its own start/finish timestamps and retry count for actionable
	// feedback. The context-injection rows resolve before any specialist
	// starts (load lang briefs / tech experts / repo experts) so the user
	// can see what's being threaded into specialists.
	ag := make([]overlayAgentRow, 0, len(review.AllSpecialists)+5)
	ag = append(ag,
		overlayAgentRow{name: overlayAgentLangBriefs, stage: stageGroupContextInjection, phase: oaPending},
		overlayAgentRow{name: overlayAgentTechExperts, stage: stageGroupContextInjection, phase: oaPending},
		overlayAgentRow{name: overlayAgentRepoExperts, stage: stageGroupContextInjection, phase: oaPending},
	)
	for _, n := range review.AllSpecialists {
		ag = append(ag, overlayAgentRow{name: n, stage: stageGroupSpecialists, phase: oaPending})
	}
	ag = append(ag,
		overlayAgentRow{name: overlayAgentRepoArbiter, stage: stageGroupArbiter, phase: oaPending},
		overlayAgentRow{name: review.SpecVibeCoach, stage: stageGroupVibe, phase: oaPending},
	)
	m := &Model{
		vp:                  vp,
		sp:                  sp,
		agents:              ag,
		cursor:              0,
		phase:               phaseRunning,
		specialistsParallel: specialistsParallel,
		repoExpertsParallel: repoExpertsParallel,
		dryRun:              dryRun,
		demoMode:            demoMode,
		runStartedAt:        time.Now(),
		aiConfig:            cfg,
	}
	m.resizeFromScreen(screenW, screenH)
	return m
}

func (m *Model) specialistsStageNote() string {
	n := len(review.AllSpecialists)
	if m.specialistsParallel {
		return fmt.Sprintf("%d agents · parallel", n)
	}
	return fmt.Sprintf("%d agents · sequential", n)
}

func (m *Model) repoExpertsStageNote() string {
	// Retained for symmetry with stageGroupMetas; the repo-experts stage is
	// no longer rendered after the repo-agents refactor.
	return ""
}

func (m *Model) Init() tea.Cmd {
	return m.sp.Tick
}

// Chrome / body overhead cells.
//
// reviewChromeFrameW / reviewChromeFrameH are what the bubble-overlay
// WindowChrome paints around our body when Resizable=true:
//
//	width:  2 (left + right box border)
//	height: 4 (1 TabOffsetTop mask row + 1 tab cap row + 1 tab-on-border row
//	           which is also the box top + 1 box bottom row)
//
// reviewBodyPadW / reviewBodyPadH are the cells reviewBodyStyle's
// Padding(1, 2) adds inside that frame.
//
// reviewBodySepH counts the four non-viewport rows of the body's
// JoinVertical layout: title, blank, blank, help.
const (
	reviewChromeFrameW = 2
	reviewChromeFrameH = 4
	reviewBodyPadW     = 4
	reviewBodyPadH     = 2
	reviewBodySepH     = 4
)

// resizeFromScreen sizes the modal from a raw screen budget (full
// terminal width / height, as delivered via tea.WindowSizeMsg). This is
// the "first paint" path — before the user has dragged the chrome
// anywhere — so we apply the same 60..140 / 16..60 clamp the modal
// has always used.
//
// Chrome-driven resizes (OnOverlayResize) take the direct content-rect
// path through ResizeContent below; they have already been clamped by
// WindowChrome.MinWidth / MinHeight independently.
func (m *Model) resizeFromScreen(sw, sh int) {
	outerW := util.Clamp(sw-4, 60, 140)
	outerH := util.Clamp(sh-6, 16, 60)
	m.setOuterDims(outerW, outerH)
}

// ResizeContent applies a content rect reported by bubble-overlay's
// WindowChrome (the rectangle inside the chrome's box border, excluding
// the tab). The library reports this after every resize gesture; we
// recover outerW / outerH by adding the chrome's own frame cells back
// in so the body keeps filling the chrome exactly.
//
// Exposed so the package-level OverlayResizer implementation in
// progress.go can call it without touching the unexported overhead
// constants.
func (m *Model) ResizeContent(contentW, contentH int) {
	outerW := contentW + reviewChromeFrameW
	outerH := contentH + reviewChromeFrameH
	m.setOuterDims(outerW, outerH)
}

func (m *Model) setOuterDims(outerW, outerH int) {
	m.outerW = outerW
	m.outerH = outerH
	innerW := max(12, m.outerW-reviewChromeFrameW-reviewBodyPadW)
	innerH := max(6, m.outerH-reviewChromeFrameH-reviewBodyPadH-reviewBodySepH)
	m.vp.Width = innerW
	m.vp.Height = innerH
}

// AdoptDraft is invoked once the runner reports Stage="done". It populates
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
// AdoptDraft installs the runner's final draft, builds approval cards,
// and routes to the appropriate first phase. Returns a tea.Cmd that
// callers MUST include in their tea.Batch — when the post-arbiter set
// has no cards and the verdict isn't APPROVE, the overlay flips to
// summary and (only if the draft is missing a vibe-coach result) may
// dispatch a regeneration. The runner normally produces VibeCoach in
// the pipeline, so the typical path is a nil cmd straight to summary.
func (m *Model) AdoptDraft(d *review.Draft) tea.Cmd {
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
		anchorCardToDiff(&card, m.files)
		m.cards = append(m.cards, card)
	}
	m.idx = 0
	m.existingCommentsLoading = false
	// The runner produces a VibeCoach result against the post-arbiter
	// finding set (UserSkipPostKeys is empty at pipeline time). Record
	// its skip hash so a same-skip-set entry to phaseSummary reuses
	// the cached result instead of re-running. A nil VibeCoach (e.g.
	// when downstream agents were skipped because every specialist
	// came back clean) leaves the hash empty so the TUI will regenerate
	// only if it eventually needs to.
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

// anchorCardToDiff fills in card.file and card.hunk against files, mutating
// the card in place. The preferred outcome is a direct hit: the finding's
// Line falls inside one of the file's hunks. When that fails (the line is
// outside every hunk on the current diff — typically because a force-push
// moved the surrounding code), the helper falls back to relocating via the
// model's AnchorExcerpt: if the excerpt uniquely matches a single line
// somewhere in the file's hunks, the card's finding.Finding.Line is
// rewritten to that line and card.anchorRelocatedFrom records the original
// for the TUI banner. When neither path lands the card on a hunk, card.hunk
// stays nil and the existing "isn't on a hunk" error path takes over (now
// joined by the F-key file-level fallback the reviewer can use to post
// anyway).
//
// Cards that were never inside the diff (Path absent from files) get
// card.file == nil and no relocation attempt — there is no hunk to search.
func anchorCardToDiff(card *approvalCard, files []review.FileDiff) {
	if card == nil {
		return
	}
	card.file = review.FindFile(files, card.finding.Finding.Path)
	card.hunk = nil
	if card.file == nil {
		return
	}
	if h, _ := review.HunkAroundLine(card.file, card.finding.Finding.Line); h != nil {
		card.hunk = h
		return
	}
	excerpt := strings.TrimSpace(card.finding.Finding.AnchorExcerpt)
	if excerpt == "" {
		return
	}
	newLine, ok := review.FindUniqueExcerptInFile(card.file, excerpt)
	if !ok || newLine == card.finding.Finding.Line {
		return
	}
	h, _ := review.HunkAroundLine(card.file, newLine)
	if h == nil {
		return
	}
	card.anchorRelocatedFrom = card.finding.Finding.Line
	card.finding.Finding.Line = newLine
	card.hunk = h
}

func (m *Model) CmdAfterAdoptIfNeeded() tea.Cmd {
	if m.dryRun || len(m.cards) == 0 || m.draft == nil {
		return nil
	}
	m.existingCommentsLoading = true
	return data.FetchExistingPRCommentsCmd(m.draft.Ref, m.demoMode)
}

func (m *Model) markCardsAlreadyOnGitHub(viewer string, existing []gh.PullReviewComment) tea.Cmd {
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

func (m *Model) firstPendingCardIndex() int {
	for i := range m.cards {
		if m.cards[i].state == cardPending {
			return i
		}
	}
	return len(m.cards)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resizeFromScreen(msg.Width, msg.Height)
		m.rebuildBody()
		var c0, c1 tea.Cmd
		m.vp, c0 = m.vp.Update(msg)
		m.sp, c1 = m.sp.Update(msg)
		return m, tea.Batch(c0, c1)

	case data.ProgressMsg:
		cmd := m.mergeProgress(review.Progress(msg))
		m.rebuildBody()
		return m, cmd

	case data.ExistingPRCommentsMsg:
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

	case data.StagedFindingPostedMsg:
		// Single finding succeeded — mark current as posted and advance.
		var advCmd tea.Cmd
		if m.phase == phaseApprove && m.idx < len(m.cards) {
			m.cards[m.idx].state = cardPosted
			advCmd = m.advanceCard()
		}
		m.rebuildBody()
		return m, advCmd

	case data.PRRefreshedMsg:
		// User pressed R after a 422 / drift; we have a fresh PR + diff. Adopt
		// it and re-anchor every pending card to the new diff so the next y
		// press hits an up-to-date commit_id and a valid line.
		m.applyPRRefresh(msg.PR, msg.Diff)
		return m, nil

	case VibeCoachDoneMsg:
		m.coachInFlight = false
		// If the user changed skips again between issue and completion,
		// the current skip-hash will differ from the one this message
		// captured. Re-issue against the new set rather than installing
		// a stale result.
		if m.draft == nil {
			return m, nil
		}
		curHash := skipSetHash(m.draft.UserSkipPostKeys)
		if curHash != msg.AtSkipHash {
			// Stale completion. Re-issue (enterSummary will set
			// coachInFlight=true again).
			return m, m.enterSummary()
		}
		if msg.Result != nil {
			m.draft.VibeCoach = msg.Result
			m.coachErr = msg.Result.Err
		}
		m.lastCoachHash = curHash
		// Verdict may have changed (e.g. user skipped the last blocker
		// → APPROVE). Route accordingly.
		if m.draft.PostEvent() == "APPROVE" && len(m.cards) > 0 {
			// Only auto-route to confirmApprove when there were
			// cards (i.e. we came through phaseApprove). If we
			// reached enterSummary from AdoptDraft directly with
			// no cards, the original adopt logic already routed
			// us — don't override the user.
		}
		m.phase = phaseSummary
		m.vp.GotoTop()
		m.rebuildBody()
		return m, nil

	case data.DryRunPayloadMsg:
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

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
			return m, func() tea.Msg { return CloseMsg{} }
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
				return m, func() tea.Msg { return CloseMsg{} }
			}
			return m, nil
		}
		if m.existingCommentsLoading {
			switch msg.String() {
			case "q", "esc":
				return m, func() tea.Msg { return CloseMsg{} }
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
		case "F":
			// File-level fallback: post the current finding as a
			// subject_type=file comment when its line can't anchor on
			// the current diff. The handler is a no-op when the card
			// isn't in a state where this makes sense.
			return m.actPostCurrentFileLevel()
		case "f":
			// Finish approving early; jump to summary even with cards left.
			// enterSummary handles syncing skips + re-running vibe-coach
			// only if the skip set differs from the pipeline-time run.
			cmd := m.enterSummary()
			return m, cmd
		case "q", "esc":
			return m, func() tea.Msg { return CloseMsg{} }
		}
	case phaseGeneratingSummary:
		// Refining-summary interstitial. The only escape is to abort
		// the overlay entirely; everything else waits for the
		// VibeCoachDoneMsg to flip into phaseSummary.
		switch msg.String() {
		case "q", "esc":
			return m, func() tea.Msg { return CloseMsg{} }
		}
		return m, nil
	case phaseSummary:
		switch msg.String() {
		case "y", "Y", "enter":
			return m.actPostSummary()
		case "a", "A":
			if m.summaryPhaseAllowApproveOnly() {
				return m.actPostApprove()
			}
			return m, nil
		case "n", "N":
			m.summarySkip = true
			m.phase = phasePosted
			m.rebuildBody()
			return m, nil
		case "r", "R":
			return m.actRefreshPR()
		case "esc", "q":
			return m, func() tea.Msg { return CloseMsg{} }
		}
	case phaseConfirmApprove:
		switch msg.String() {
		case "y", "Y", "enter":
			return m.actPostApprove()
		case "a", "A":
			// "Approve only" — the no-findings auto-approve flow is the
			// only branch where actPostApprove attaches a non-empty body
			// (the "no issues found by any agent" recap), so that's the
			// only branch where the user-facing distinction matters. In
			// the regular branch APPROVE already posts an empty body so
			// a is silently equivalent and we deliberately ignore it to
			// keep that screen's contract single-button.
			if m.noFindingsApprove {
				return m.actPostApproveOnly()
			}
			return m, nil
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
			return m, func() tea.Msg { return CloseMsg{} }
		}
	case phasePosted:
		switch msg.String() {
		case "esc", "enter", "q", " ":
			return m, func() tea.Msg { return CloseMsg{} }
		}
	}
	// Fall through: pass scroll keys to the viewport.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
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
			if z := zone.Get(zones.StagedPost); z != nil && z.InBounds(msg) {
				return m.actPostCurrent()
			}
			if z := zone.Get(zones.StagedSkip); z != nil && z.InBounds(msg) {
				return m.actSkipCurrent()
			}
			if z := zone.Get(zones.StagedNext); z != nil && z.InBounds(msg) {
				return m.actNext()
			}
			if z := zone.Get(zones.StagedPrev); z != nil && z.InBounds(msg) {
				return m.actPrev()
			}
			if z := zone.Get(zones.StagedRefresh); z != nil && z.InBounds(msg) {
				return m.actRefreshPR()
			}
			if z := zone.Get(zones.StagedFinish); z != nil && z.InBounds(msg) {
				cmd := m.enterSummary()
				return m, cmd
			}
			if z := zone.Get(zones.StagedQuit); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return CloseMsg{} }
			}
		case phaseSummary:
			if z := zone.Get(zones.StagedSummaryYes); z != nil && z.InBounds(msg) {
				return m.actPostSummary()
			}
			if z := zone.Get(zones.StagedSummaryNo); z != nil && z.InBounds(msg) {
				m.summarySkip = true
				m.phase = phasePosted
				m.rebuildBody()
				return m, nil
			}
			if m.summaryPhaseAllowApproveOnly() {
				if z := zone.Get(zones.StagedSummaryApproveOnly); z != nil && z.InBounds(msg) {
					return m.actPostApprove()
				}
			}
			if z := zone.Get(zones.StagedRefresh); z != nil && z.InBounds(msg) {
				return m.actRefreshPR()
			}
			if z := zone.Get(zones.StagedQuit); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return CloseMsg{} }
			}
		case phaseConfirmApprove:
			if z := zone.Get(zones.StagedSummaryYes); z != nil && z.InBounds(msg) {
				return m.actPostApprove()
			}
			// The "Approve only" zone is only rendered in the no-findings
			// auto-approve flow (the only branch where actPostApprove
			// would otherwise attach the rendered "no issues found"
			// body). Mirror the keyboard handler's noFindingsApprove
			// gate so a stale zone from a previous render can't fire
			// the bare-body post outside that branch.
			if m.noFindingsApprove {
				if z := zone.Get(zones.StagedSummaryApproveOnly); z != nil && z.InBounds(msg) {
					return m.actPostApproveOnly()
				}
			}
			// The no-findings approve flow doesn't render the "no" zone (no
			// comment-only review to fall back to when there's nothing to
			// say), so only honour the click when the button is present.
			if !m.noFindingsApprove {
				if z := zone.Get(zones.StagedSummaryNo); z != nil && z.InBounds(msg) {
					m.approveAfterSkipDisagree = false
					cmd := m.enterSummary()
					return m, cmd
				}
			}
			if z := zone.Get(zones.StagedRefresh); z != nil && z.InBounds(msg) {
				return m.actRefreshPR()
			}
			if z := zone.Get(zones.StagedQuit); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return CloseMsg{} }
			}
		case phasePosted:
			if z := zone.Get(zones.PostedOK); z != nil && z.InBounds(msg) {
				return m, func() tea.Msg { return CloseMsg{} }
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}
