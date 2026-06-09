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

// outputAgentOrder lists the agents that get their own tab, left to
// right: the five specialists, then the repo arbiter, then the vibe
// coach. Context-injection rows stay in the overview tab.
func outputAgentOrder() []string {
	out := make([]string, 0, len(review.AllSpecialists)+2)
	out = append(out, review.AllSpecialists...)
	out = append(out, overlayAgentRepoArbiter, review.SpecVibeCoach)
	return out
}

// buildReviewTabs constructs the full tab bar: overview, one tab per
// output agent, then the summary tab.
func buildReviewTabs() []reviewTab {
	tabs := make([]reviewTab, 0, len(outputAgentOrder())+2)
	tabs = append(tabs, reviewTab{kind: tabOverview})
	for _, name := range outputAgentOrder() {
		tabs = append(tabs, reviewTab{kind: tabAgent, agent: name})
	}
	tabs = append(tabs, reviewTab{kind: tabSummary})
	return tabs
}

// tabShortLabel is the compact label shown in the tab strip.
func tabShortLabel(t reviewTab) string {
	switch t.kind {
	case tabOverview:
		return "Overview"
	case tabSummary:
		return "Summary"
	case tabAgent:
		switch t.agent {
		case overlayAgentRepoArbiter:
			return "Arbiter"
		case review.SpecVibeCoach:
			return "Vibe"
		}
		return t.agent
	}
	return "?"
}

// summaryTabIndex returns the index of the summary tab (always last).
func (m *Model) summaryTabIndex() int { return len(m.tabs) - 1 }

// onSummaryTab reports whether the summary tab is focused.
func (m *Model) onSummaryTab() bool {
	return m.activeTab >= 0 && m.activeTab < len(m.tabs) && m.tabs[m.activeTab].kind == tabSummary
}

// activeAgent returns the agent name for the focused tab, or "" when the
// focused tab isn't an agent tab.
func (m *Model) activeAgent() string {
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		return ""
	}
	if m.tabs[m.activeTab].kind != tabAgent {
		return ""
	}
	return m.tabs[m.activeTab].agent
}

// agentCardIndices returns the global indices into m.cards whose finding
// belongs to the named agent (specialist), in card order.
func (m *Model) agentCardIndices(name string) []int {
	if name == "" {
		return nil
	}
	var out []int
	for i := range m.cards {
		if m.cards[i].finding.Specialist == name {
			out = append(out, i)
		}
	}
	return out
}

// firstCardForAgent returns the global index of the agent's first pending
// card, falling back to its first card; -1 when the agent has no cards.
func (m *Model) firstCardForAgent(name string) int {
	idxs := m.agentCardIndices(name)
	if len(idxs) == 0 {
		return -1
	}
	for _, gi := range idxs {
		if m.cards[gi].state == cardPending {
			return gi
		}
	}
	return idxs[0]
}

// focusTab switches the active tab and re-derives the overlay phase from
// it. Returns the tea.Cmd from entering the summary tab (vibe-coach
// re-run), or nil for the overview / agent tabs.
func (m *Model) focusTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	m.activeTab = i
	t := m.tabs[i]
	switch t.kind {
	case tabOverview:
		m.phase = phaseRunning
	case tabAgent:
		m.phase = phaseApprove
		m.idx = m.firstCardForAgent(t.agent)
	case tabSummary:
		switch {
		case !m.done:
			// Summary not generated yet — render the "not ready" body.
			m.phase = phaseSummary
		case m.posted:
			m.phase = phasePosted
		default:
			m.vp.GotoTop()
			return m.enterSummary()
		}
	}
	m.vp.GotoTop()
	m.rebuildBody()
	return nil
}

// nextTab / prevTab move the focus one tab over (no wraparound).
func (m *Model) nextTab() tea.Cmd {
	if m.activeTab < len(m.tabs)-1 {
		return m.focusTab(m.activeTab + 1)
	}
	return nil
}

func (m *Model) prevTab() tea.Cmd {
	if m.activeTab > 0 {
		return m.focusTab(m.activeTab - 1)
	}
	return nil
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
	// findings holds the agent's streamed findings (specialists only) so
	// the per-agent tab can show them live during the run, before the
	// pipeline completes and AdoptDraft turns them into postable cards.
	findings []review.Finding
	// verdict is the vibe-coach merge recommendation, surfaced on the
	// vibe-coach tab. Empty for every other agent.
	verdict string
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

// tabKind classifies the entries in the review overlay's tab bar.
type tabKind int

const (
	// tabOverview is the always-present first tab — the live pipeline
	// progress view (renderRunningBody).
	tabOverview tabKind = iota
	// tabAgent is one of the per-agent tabs (a specialist, the repo
	// arbiter, or the vibe coach). It shows that agent's findings (with
	// per-finding post/skip once the pipeline completes) and an
	// always-present summary of what the agent did / found.
	tabAgent
	// tabSummary is the final tab — the review summary, approve
	// confirmation, and posted receipt.
	tabSummary
)

// reviewTab is one entry in the tab bar. Agent tabs carry the agent name
// (a specialist key, overlayAgentRepoArbiter, or review.SpecVibeCoach) so
// the renderer and key handlers can resolve the matching overlayAgentRow
// and approval cards.
type reviewTab struct {
	kind  tabKind
	agent string
}

// overlayPhase tags which screen the persistent review overlay is showing.
// It is kept in lock-step with the active tab: phaseRunning ↔ the overview
// tab, phaseApprove ↔ an agent tab, and the four summary sub-states ↔ the
// summary tab.
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

	phase overlayPhase
	// tabs is the tab-bar model: overview, one per output agent, then
	// summary. activeTab indexes it and is kept consistent with phase.
	tabs      []reviewTab
	activeTab int
	// done flips true once AdoptDraft installs the runner's final draft
	// (the pipeline finished). Per-finding posting is only enabled after
	// this — the repo arbiter runs after the specialists and can suppress
	// or demote findings, so posting earlier could publish something the
	// arbiter would have removed.
	done bool
	// posted flips true once the summary / approve review has been posted
	// (or skipped) so re-focusing the summary tab restores the receipt
	// instead of re-rendering the post form.
	posted bool
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

// digitKey reports whether the key message is a single ASCII digit and,
// if so, its numeric value (1-9, plus 0). Used for jump-to-tab shortcuts.
func digitKey(msg tea.KeyMsg) (int, bool) {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return 0, false
	}
	r := msg.Runes[0]
	if r < '0' || r > '9' {
		return 0, false
	}
	return int(r - '0'), true
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
		tabs:                buildReviewTabs(),
		activeTab:           0,
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
// reviewBodySepH counts the five non-viewport rows of the body's
// JoinVertical layout: title, tab bar, blank, blank, help.
const (
	reviewChromeFrameW = 2
	reviewChromeFrameH = 4
	reviewBodyPadW     = 4
	reviewBodyPadH     = 2
	reviewBodySepH     = 5
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
// AdoptDraft installs the runner's final draft, builds approval cards,
// hydrates the per-agent tabs, and (when the user is still on the overview
// tab) auto-focuses the summary tab. Returns a tea.Cmd that callers MUST
// include in their tea.Batch — focusing the summary tab may dispatch a
// vibe-coach regeneration when the draft is missing a cached result. The
// runner normally produces VibeCoach in the pipeline, so the typical path
// is a nil cmd straight to the summary.
// hydrateAgentRowsFromDraft backfills the overlay's agent rows from a
// completed draft. Rows already marked done/skipped/errored by streamed
// progress events keep richer state; rows still pending (the reopen case,
// where no progress was observed) get their summary, findings, and
// done-phase from the draft so every agent tab and the overview render
// correctly.
func (m *Model) hydrateAgentRowsFromDraft(d *review.Draft) {
	if d == nil {
		return
	}
	for _, sr := range d.Specialists {
		i := m.agentIndex(sr.Specialist)
		if i < 0 {
			continue
		}
		row := &m.agents[i]
		if row.phase == oaPending {
			row.phase = oaDone
		}
		if strings.TrimSpace(row.summary) == "" {
			row.summary = sr.Summary
		}
		if len(row.findings) == 0 {
			row.findings = sr.Findings
		}
		if row.findingsN == 0 {
			row.findingsN = len(sr.Findings)
		}
	}
	if d.RepoArbiter != nil {
		if i := m.agentIndex(overlayAgentRepoArbiter); i >= 0 {
			row := &m.agents[i]
			if row.phase == oaPending {
				row.phase = oaDone
			}
			if strings.TrimSpace(row.summary) == "" {
				row.summary = formatArbiterRowSummary(d.RepoArbiter)
			}
		}
	}
	if d.VibeCoach != nil {
		if i := m.agentIndex(review.SpecVibeCoach); i >= 0 {
			row := &m.agents[i]
			if row.phase == oaPending {
				row.phase = oaDone
			}
			if strings.TrimSpace(row.summary) == "" {
				row.summary = d.VibeCoach.Summary
			}
			if row.verdict == "" {
				row.verdict = d.VibeCoach.Verdict
			}
		}
	}
}

func (m *Model) AdoptDraft(d *review.Draft) tea.Cmd {
	m.draft = d
	m.approveAfterSkipDisagree = false
	m.noFindingsApprove = false
	if d == nil {
		return nil
	}
	m.files = review.ParseDiff(d.Diff)
	flat := d.FlatPostableFindingsForPost()
	m.cards = make([]approvalCard, 0, len(flat))
	for _, f := range flat {
		card := approvalCard{finding: f}
		anchorCardToDiff(&card, m.files)
		m.cards = append(m.cards, card)
	}
	m.idx = 0
	m.done = true
	m.existingCommentsLoading = false
	// Make the agent rows reflect the final draft so the overview tab and
	// the per-agent tab glyphs are correct even when the per-agent
	// progress events weren't observed (e.g. a reopened approval).
	m.hydrateAgentRowsFromDraft(d)
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
	// Every agent came back clean — the summary tab's confirm-approve
	// sub-state explains the no-findings approval rather than dropping the
	// user on a near-empty post-summary screen.
	m.noFindingsApprove = d.HasNoFindings()
	// On completion, auto-focus the summary tab (the natural end state)
	// when the user is still parked on the overview tab. If they've
	// already navigated into an agent tab to watch a result, leave them
	// there so the jump isn't disorienting; the summary tab stays one
	// keystroke away.
	if m.tabs[m.activeTab].kind == tabOverview {
		return m.focusTab(m.summaryTabIndex())
	}
	// Refresh whichever agent tab they're on now that cards exist.
	if m.tabs[m.activeTab].kind == tabAgent {
		m.idx = m.firstCardForAgent(m.activeAgent())
	}
	m.vp.GotoTop()
	m.rebuildBody()
	return nil
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
	// Re-point the focused card on whichever agent tab is active so the
	// first pending finding for that agent is highlighted (no auto-jump to
	// the summary tab — the user navigates there themselves).
	if m.tabs[m.activeTab].kind == tabAgent {
		m.idx = m.firstCardForAgent(m.activeAgent())
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
		if m.phase == phaseApprove && m.idx >= 0 && m.idx < len(m.cards) {
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
		// The refreshed summary belongs on the summary tab. Focus it (no
		// further vibe-coach re-run is needed now that the cache is warm)
		// and show the post-summary body.
		m.activeTab = m.summaryTabIndex()
		m.phase = phaseSummary
		m.vp.GotoTop()
		m.rebuildBody()
		return m, nil

	case data.DryRunPayloadMsg:
		// In dry-run, both the approve flow and summary post route here.
		var advCmd tea.Cmd
		switch {
		case m.phase == phaseApprove && m.idx >= 0 && m.idx < len(m.cards):
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
	// Tab navigation works from every phase: the user can browse the
	// overview, any finished agent, and (once ready) the summary at will.
	switch msg.String() {
	case "tab", "]", ">":
		return m, m.nextTab()
	case "shift+tab", "[", "<":
		return m, m.prevTab()
	}
	if n, ok := digitKey(msg); ok && n >= 1 && n <= len(m.tabs) {
		return m, m.focusTab(n - 1)
	}
	switch m.phase {
	case phaseRunning:
		switch msg.String() {
		case "j", "down":
			if m.cursor < len(m.agents)-1 {
				m.cursor++
			}
			m.rebuildBody()
			return m, nil
		case "k", "up":
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
		// Tab-bar clicks switch tabs from any phase.
		for i := range m.tabs {
			if z := zone.Get(zones.ReviewTab(i)); z != nil && z.InBounds(msg) {
				return m, m.focusTab(i)
			}
		}
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
