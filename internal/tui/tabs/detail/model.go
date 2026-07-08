// Package detail implements the PR detail view as a first-class Tab (F5 item 4).
package detail

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/util/dropdown"
)

// Opts configures a fresh detail tab model.
type Opts struct {
	Host Host
	Keys keys.Map
}

// Model is the PR detail three-pane view (tree / diff / controls).
type Model struct {
	host Host
	keys keys.Map

	width      int
	bodyH      int
	contentTop int

	// PR detail layout: tree + diff.
	treeRows         []treeRow
	treeIdx          int
	focusedPane      pane
	selectedFilePath string
	diffOnly         bool

	treeViewRows         []treeViewRow
	treeFileToLine       []int
	treeLineToFile       []int
	collapsedFolders     map[string]bool
	scrollToSelectedFile bool

	treeView     viewport.Model
	diffView     viewport.Model
	controlsView viewport.Model

	hl               *diffview.Highlighter
	diffAnchors      diffview.AnchorIndex
	diffSearch       diffview.SearchIndex
	diffSearchQuery  string
	diffSearching    bool
	diffSearchInput  textinput.Model
	diffContentLines []string

	treePaneWidth     int
	controlsPaneWidth int
	paneDrag          paneDrag

	controlsHidden       bool
	controlsUserHidden   bool
	startReviewMinimized bool

	treeScrollLines int
	centerView      centerView

	checks            *gh.ChecksReport
	checksLoading     bool
	checksErr         error
	discussion        []gh.DiscussionEvent
	discussionLoading bool
	discussionErr     error

	prComments     []gh.PullReviewComment
	prThreads      []gh.ReviewThread
	showThreads    bool
	threadsLoaded  bool
	threadsLoading bool
	historyCursor  int
	replyInput     textinput.Model
	replyingTo     string
	replyStatus    string

	controlsProfileDD    *dropdown.Host
	controlsProfileDDRow int
	controlsProfileDDCol int

	treeClickArmed bool
	treeClickIndex int
	treeClickAt    time.Time
}

// New constructs the detail tab wired to the root host.
func New(host Host, km keys.Map) *Model {
	tv := viewport.New(0, 0)
	dv := viewport.New(0, 0)
	cv := viewport.New(0, 0)
	for _, vp := range []*viewport.Model{&tv, &dv, &cv} {
		vp.SetHorizontalStep(4)
		vp.MouseWheelEnabled = false
	}
	return &Model{
		host:              host,
		keys:              km,
		treeView:          tv,
		diffView:          dv,
		controlsView:      cv,
		focusedPane:       paneTree,
		treePaneWidth:     defaultTreePaneWidth,
		controlsPaneWidth: defaultControlsPaneWidth,
		collapsedFolders:  map[string]bool{},
	}
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		_, cmd := m.handleKey(msg)
		return m, cmd
	case tea.MouseMsg:
		_, cmd := m.handleMouse(msg, false)
		return m, cmd
	case tea.WindowSizeMsg:
		m.Resize(msg.Width, 0)
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) View() string {
	if m.host.Width() == 0 || m.host.Height() == 0 {
		return "loading…"
	}
	body := m.renderPRDetailBody(m.bodyH)
	return m.overlayControlsProfile(body)
}

func (m *Model) Resize(w, bodyH int) {
	if w > 0 {
		m.width = w
	}
	if bodyH > 0 {
		m.bodyH = bodyH
	}
	m.relayout()
}

func (m *Model) SetContentOrigin(top int) {
	m.contentTop = top
}

// OnPRLoaded resets detail UI state when the root loads a new PR.
func (m *Model) OnPRLoaded(parsedDiff []review.FileDiff, draft *review.Draft) {
	m.collapsedFolders = map[string]bool{}
	m.treeRows = buildTreeRows(parsedDiff, draft)
	m.treeIdx = 0
	m.diffOnly = false
	m.centerView = centerDiff
	m.resetOverviewData()
	m.focusedPane = paneTree
	m.selectedFilePath = ""
	if len(parsedDiff) > 0 {
		m.selectedFilePath = parsedDiff[0].Path
	}
	m.recomputeTreeView()
	m.scrollToSelectedFile = true
}

// OnDraftUpdated rebuilds tree rows when the review draft changes.
func (m *Model) OnDraftUpdated(parsedDiff []review.FileDiff, draft *review.Draft) {
	m.recomputeTreeRows(parsedDiff, draft)
}

func (m *Model) recomputeTreeRows(parsedDiff []review.FileDiff, draft *review.Draft) {
	m.treeRows = buildTreeRows(parsedDiff, draft)
	m.recomputeTreeView()
}

// RefreshViews repopulates viewport content after layout or data changes.
func (m *Model) RefreshViews() { m.refreshDetailViews() }

// JumpToFinding scrolls the diff pane to a finding anchor (review overlay).
func (m *Model) JumpToFinding(path string, line int) bool {
	return m.jumpToFinding(path, line)
}

// ApplyThreadsLoaded stores fetched thread data.
func (m *Model) ApplyThreadsLoaded(comments []gh.PullReviewComment, threads []gh.ReviewThread) {
	m.prComments = comments
	m.prThreads = threads
	m.threadsLoaded = true
	m.threadsLoading = false
	m.refreshDetailViews()
}

// ApplyThreadReply records reply outcome.
func (m *Model) ApplyThreadReply(status string) {
	m.replyStatus = status
	m.refreshDetailViews()
}

// ApplyChecks stores checks fetch result.
func (m *Model) ApplyChecks(report *gh.ChecksReport, err error) {
	m.checksLoading = false
	m.checksErr = err
	if err == nil {
		m.checks = report
	}
	m.refreshDetailViews()
}

// ApplyDiscussion stores discussion fetch result.
func (m *Model) ApplyDiscussion(events []gh.DiscussionEvent, err error) {
	m.discussionLoading = false
	m.discussionErr = err
	if err == nil {
		m.discussion = events
		if m.discussion == nil {
			m.discussion = []gh.DiscussionEvent{}
		}
	}
	m.refreshDetailViews()
}

// --- test accessors (integration tests in model package) ---

func (m *Model) CenterView() centerView            { return m.centerView }
func (m *Model) TreeIdx() int                      { return m.treeIdx }
func (m *Model) FocusedPane() pane                 { return m.focusedPane }
func (m *Model) SelectedFilePath() string          { return m.selectedFilePath }
func (m *Model) DiffOnly() bool                    { return m.diffOnly }
func (m *Model) ControlsHidden() bool              { return m.controlsHidden }
func (m *Model) ControlsUserHidden() bool          { return m.controlsUserHidden }
func (m *Model) TreePaneWidth() int                { return m.treePaneWidth }
func (m *Model) ControlsPaneWidth() int            { return m.controlsPaneWidth }
func (m *Model) TreeViewRows() []treeViewRow       { return m.treeViewRows }
func (m *Model) CollapsedFolders() map[string]bool { return m.collapsedFolders }
func (m *Model) DiffView() viewport.Model          { return m.diffView }
func (m *Model) ShowThreads() bool                 { return m.showThreads }
func (m *Model) DiffSearchQuery() string           { return m.diffSearchQuery }
func (m *Model) DiffSearching() bool               { return m.diffSearching }
func (m *Model) ReplyingTo() string                { return m.replyingTo }
func (m *Model) Checks() *gh.ChecksReport          { return m.checks }
func (m *Model) ChecksLoading() bool               { return m.checksLoading }
func (m *Model) Discussion() []gh.DiscussionEvent  { return m.discussion }
func (m *Model) DiscussionLoading() bool           { return m.discussionLoading }

func (m *Model) currentPR() *gh.PR             { return m.host.CurrentPR() }
func (m *Model) parsedDiff() []review.FileDiff { return m.host.ParsedDiff() }
func (m *Model) draft() *review.Draft          { return m.host.Draft() }

func (m *Model) SetCenterView(v centerView)            { m.centerView = v }
func (m *Model) SetTreeIdx(i int)                      { m.treeIdx = i }
func (m *Model) SetSelectedFilePath(p string)          { m.selectedFilePath = p }
func (m *Model) SetChecksLoading(v bool)               { m.checksLoading = v }
func (m *Model) SetChecks(c *gh.ChecksReport)          { m.checks = c }
func (m *Model) SetDiscussion(d []gh.DiscussionEvent)  { m.discussion = d }
func (m *Model) SetDiscussionLoading(v bool)           { m.discussionLoading = v }
func (m *Model) SetFocusedPane(p pane)                 { m.focusedPane = p }
func (m *Model) SetCollapsedFolders(f map[string]bool) { m.collapsedFolders = f }
func (m *Model) SetTreePaneWidth(w int)                { m.treePaneWidth = w }
func (m *Model) EnsureCenterDataLoaded() tea.Cmd       { return m.ensureCenterDataLoaded() }
func (m *Model) TreeRows() []TreeRow {
	out := make([]TreeRow, len(m.treeRows))
	copy(out, m.treeRows)
	return out
}
func (m *Model) ScrollToSelectedFile() bool { return m.scrollToSelectedFile }

func (m *Model) StartReviewMinimized() bool     { return m.startReviewMinimized }
func (m *Model) SetStartReviewMinimized(v bool) { m.startReviewMinimized = v }

// LeftColumnIndexFor exports navigation index math for integration tests.
func LeftColumnIndexFor(v CenterView, treeIdx int) int {
	return leftColumnIndexFor(centerView(v), treeIdx)
}

// RenderTreePane exports tree pane rendering for zone layout tests.
func RenderTreePane(viewRows []TreeViewRow, fileRows []TreeRow, collapsed map[string]bool, selectedIdx int, contentCols int, focused bool) string {
	vr := make([]treeViewRow, len(viewRows))
	copy(vr, viewRows)
	fr := make([]treeRow, len(fileRows))
	copy(fr, fileRows)
	return renderTreePane(vr, fr, collapsed, selectedIdx, contentCols, focused)
}
