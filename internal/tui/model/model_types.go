package model

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/commands"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
	"github.com/madicen/appr-ai-sal/internal/tui/util/dropdown"
)

// mode is the root TUI's active top-level screen. It is a type alias for
// state.ViewMode — there is no separate root enum anymore; the state
// package owns the single canonical definition and the root's tab
// registry is keyed off it (map[mode]Tab). The short modeX constants
// below are convenience aliases for the state.ModeX values so existing
// call sites read the same as before.
type mode = state.ViewMode

const (
	modeList       = state.ModeList
	modeDetail     = state.ModeDetail
	modeSettings   = state.ModeSettings
	modeRepoAgents = state.ModeRepoAgents
	modeLangAgents = state.ModeLangAgents
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

// Model is the root Bubble Tea model. It owns the PR list and detail
// screens directly and delegates the settings / repo-agents / lang-agents
// screens to Tab sub-models held in the tabs registry.
type Model struct {
	opts Options
	mode mode

	// keys is the central keymap (Phase 5 item 1): the single source of
	// truth for every list/detail/global binding. Handlers match against
	// it (key.Matches), and the status bar + `?` help overlay + command
	// palette all read their labels from it so hints can't drift.
	keys keys.Map

	// palette is the command registry (Phase 5 item 6) shared by the
	// keymap and the ctrl+k fuzzy palette. Built once in New(); commands
	// wire their Run closures to existing model handlers.
	palette *commands.Registry

	// tabs is the registry of full-screen sub-model tabs, keyed by the
	// ViewMode they own (settings, repo-agents, lang-agents). Exactly one
	// entry exists at a time — the tab is constructed on open and dropped
	// on NavBack. modeList / modeDetail are root-native and never appear
	// here. Root routes every message for the active mode through the one
	// entry m.tabs[m.mode] instead of hand-forwarding to concrete pointers.
	tabs map[mode]Tab

	// tabPrevMode is the mode to restore when the active tab emits NavBack.
	// Only one tab is open at a time, so a single field suffices.
	tabPrevMode mode

	// controlsProfileDD is the AI-profile dropdown in the PR-detail
	// "Review controls" pane. It is positioned from the trigger's
	// bubblezone-scanned (absolute) coordinates, cached here so the panel
	// stays put while open even though the trigger sits under the overlay.
	controlsProfileDD    *dropdown.Host
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

	// Phase 5 item 4 (diff upgrades). hl syntax-highlights the diff pane
	// (chroma, lazily built, NO_COLOR-aware). diffAnchors indexes the rendered
	// rows carrying an inline finding tag for n/p jumping; diffSearch indexes
	// rows matching the active in-diff search; diffSearchQuery / diffSearchInput
	// / diffSearching drive the `/` search prompt. All are recomputed whenever
	// the diff content is rebuilt (refreshDetailViews).
	hl               *diffview.Highlighter
	diffAnchors      diffview.AnchorIndex
	diffSearch       diffview.SearchIndex
	diffSearchQuery  string
	diffSearching    bool
	diffSearchInput  textinput.Model
	diffContentLines []string // full wrapped diff-pane rows (for nav/search/jump)

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

	// Phase 5 item 8 (thread browsing). prComments / prThreads are the PR's
	// existing inline review comments + threads (fetched once, lazily, on the
	// first `t`/`H` press). showThreads toggles rendering them inline in the
	// diff; threadsLoaded / threadsLoading gate the fetch. historyCursor is the
	// selected thread in the review-history pane; replyInput / replyingTo drive
	// the in-pane reply prompt (item 8.3, replies via data.ReplyToThreadCmd →
	// Backend.ReplyToThread / gh.ReplyToReviewThread).
	prComments     []gh.PullReviewComment
	prThreads      []gh.ReviewThread
	showThreads    bool
	threadsLoaded  bool
	threadsLoading bool
	historyCursor  int
	replyInput     textinput.Model
	replyingTo     string // thread ID currently being replied to; "" when idle
	replyStatus    string // transient status after a reply attempt

	spinner    spinner.Model
	progressCh <-chan review.Progress

	// reviewCancel cancels the context threaded into the active interactive
	// review run (Phase 5 item 3). Set when startReviewOverlay kicks off a
	// run; invoked (and nilled) by cancelReview when the review overlay closes
	// or another run starts, so the runner goroutine stops instead of leaking.
	reviewCancel context.CancelFunc

	// queue holds the Phase 5 item 10 "run review on all listed PRs" state.
	queue queueState

	err error

	// Active review overlay reference (for direct state pokes when posting).
	currentReviewOverlay *reviewtab.Model

	// pendingResume holds a saved review session (U2) loaded when the user
	// reopened a PR whose current head SHA has an in-progress approval session.
	// It is stashed here while the resume-prompt overlay is up so the
	// DismissMsg handler can rehydrate (yes) or discard (no) it. Nil when no
	// resume is being offered.
	pendingResume *review.SessionState

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
