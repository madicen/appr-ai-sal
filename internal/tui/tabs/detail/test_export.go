package detail

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// Exported aliases for integration tests in the root model package.
type CenterView = centerView

const (
	CenterDiff        = centerDiff
	CenterDescription = centerDescription
	CenterChecks      = centerChecks
	CenterDiscussion  = centerDiscussion
	CenterHistory     = centerHistory
)

// TreeRow is exported for tests.
type TreeRow = treeRow

// TreeViewRow is exported for tests.
type TreeViewRow = treeViewRow

func (r TreeViewRow) IsFile() bool { return r.isFile }

func (r TreeViewRow) Indent() int      { return r.indent }
func (r TreeViewRow) Name() string     { return r.name }
func (r TreeViewRow) FullPath() string { return r.fullPath }
func (r TreeViewRow) FileIndex() int   { return r.fileIndex }

type Pane = pane

const (
	PaneTree     = paneTree
	PaneDiff     = paneDiff
	PaneControls = paneControls
)

// MinTreePaneWidth is exported for layout tests.
const MinTreePaneWidth = minTreePaneWidth

// BuildTreeRows exports tree row building for tests.
func BuildTreeRows(files []review.FileDiff, draft *review.Draft) []TreeRow {
	return buildTreeRows(files, draft)
}

// BuildTreeView exports hierarchical tree view building for tests.
func BuildTreeView(rows []TreeRow, collapsed map[string]bool) (view []TreeViewRow, fileToLine []int, lineToFile []int) {
	tr := make([]treeRow, len(rows))
	copy(tr, rows)
	return buildTreeView(tr, collapsed)
}

func (m *Model) BackToList() { m.detailBackToList() }

func (m *Model) HandleKey(msg tea.KeyMsg) tea.Cmd {
	_, cmd := m.handleKey(msg)
	return cmd
}

func (m *Model) HandleMouse(msg tea.MouseMsg, wheel bool) tea.Cmd {
	_, cmd := m.handleMouse(msg, wheel)
	return cmd
}

func (m *Model) BeginDiffSearch() tea.Cmd   { return m.beginDiffSearch() }
func (m *Model) JumpDiffForward()           { m.jumpDiffForward() }
func (m *Model) JumpDiffBackward()          { m.jumpDiffBackward() }
func (m *Model) ToggleThreads() tea.Cmd     { return m.toggleThreads() }
func (m *Model) OpenReviewHistory() tea.Cmd { return m.openReviewHistory() }

func (m *Model) RenderMiniHeader() string { return m.renderDetailMiniHeader() }
