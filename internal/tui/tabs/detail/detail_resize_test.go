package detail

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// seamPressMsg synthesises a left-button press at (x, y). All resize
// tests share this helper so the press / motion / release sequence is
// uniform and easy to read.
func seamPressMsg(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func seamMotionMsg(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft}
}

func seamReleaseMsg(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}
}

// dragSeamY returns a Y coordinate guaranteed to be inside the pane
// row of the detail view (below the mini-header, above the status bar).
// We use the midpoint of the [top, bottom] range so tests are tolerant
// of small mini-header height changes between fixtures.
func dragSeamY(t *testing.T, m *Model) int {
	t.Helper()
	top, bottom, ok := m.detailPaneRowYRange()
	if !ok {
		t.Fatalf("detailPaneRowYRange unavailable: width=%d height=%d", m.width, m.host.Height())
	}
	return (top + bottom) / 2
}

// TestSeamAtPointIdentifiesTreeDiffBoundary confirms that the 2-column
// hit zone straddles the boundary at X = treeOuter and that one column
// to either side falls back to dividerNone (so a click on a tree row's
// last column doesn't accidentally arm a drag).
func TestSeamAtPointIdentifiesTreeDiffBoundary(t *testing.T) {
	m := detailFixtureModel(t)
	y := dragSeamY(t, m)
	treeOuter, _ := m.detailSeamColumns()

	if got := m.seamAtPoint(treeOuter, y); got != dividerTreeDiff {
		t.Fatalf("X=treeOuter (%d): want dividerTreeDiff, got %v", treeOuter, got)
	}
	if got := m.seamAtPoint(treeOuter-1, y); got != dividerTreeDiff {
		t.Fatalf("X=treeOuter-1 (%d): want dividerTreeDiff (right border of tree), got %v", treeOuter-1, got)
	}
	if got := m.seamAtPoint(treeOuter-2, y); got != dividerNone {
		t.Fatalf("X=treeOuter-2 (%d): want dividerNone (inside tree pane), got %v", treeOuter-2, got)
	}
	if got := m.seamAtPoint(treeOuter+1, y); got != dividerNone {
		t.Fatalf("X=treeOuter+1 (%d): want dividerNone (inside diff pane), got %v", treeOuter+1, got)
	}
}

// TestSeamAtPointIdentifiesDiffControlsBoundary mirrors the above for
// the second seam, confirming dividerDiffControls fires at both border
// columns and only there.
func TestSeamAtPointIdentifiesDiffControlsBoundary(t *testing.T) {
	m := detailFixtureModel(t)
	y := dragSeamY(t, m)
	_, controlsLeft := m.detailSeamColumns()

	if got := m.seamAtPoint(controlsLeft, y); got != dividerDiffControls {
		t.Fatalf("X=controlsLeft (%d): want dividerDiffControls, got %v", controlsLeft, got)
	}
	if got := m.seamAtPoint(controlsLeft-1, y); got != dividerDiffControls {
		t.Fatalf("X=controlsLeft-1 (%d): want dividerDiffControls (right border of diff), got %v", controlsLeft-1, got)
	}
	if got := m.seamAtPoint(controlsLeft+1, y); got != dividerNone {
		t.Fatalf("X=controlsLeft+1 (%d): want dividerNone (inside controls pane), got %v", controlsLeft+1, got)
	}
}

// TestSeamAtPointReturnsNoneOutsidePaneRow verifies the Y-range guard:
// a click in the mini-header strip (above the panes) or the status row
// (below them) must not arm a drag even if the X column is on a seam.
func TestSeamAtPointReturnsNoneOutsidePaneRow(t *testing.T) {
	m := detailFixtureModel(t)
	top, bottom, ok := m.detailPaneRowYRange()
	if !ok {
		t.Fatalf("pane row range unavailable")
	}
	treeOuter, _ := m.detailSeamColumns()

	if got := m.seamAtPoint(treeOuter, top-1); got != dividerNone {
		t.Fatalf("Y above pane row: want dividerNone, got %v", got)
	}
	if got := m.seamAtPoint(treeOuter, bottom+1); got != dividerNone {
		t.Fatalf("Y below pane row: want dividerNone, got %v", got)
	}
}

// TestPaneDragResizesTreeWidth: arm at the tree/diff seam, send motion
// 8 cols right, assert m.treePaneWidth grew by exactly 8 and the diff
// viewport shrunk by the same amount after relayout.
func TestPaneDragResizesTreeWidth(t *testing.T) {
	m := detailFixtureModel(t)
	y := dragSeamY(t, m)
	treeOuter, _ := m.detailSeamColumns()

	origTree := m.treePaneWidth
	origDiffW := m.diffView.Width

	out, _ := m.handleMouse(seamPressMsg(treeOuter, y), false)
	m = out.(*Model)
	if m.paneDrag.target != dividerTreeDiff {
		t.Fatalf("press on tree/diff seam should arm drag; target=%v", m.paneDrag.target)
	}

	out, _ = m.handleMouse(seamMotionMsg(treeOuter+8, y), false)
	m = out.(*Model)
	if got := m.treePaneWidth; got != origTree+8 {
		t.Fatalf("treePaneWidth: want %d, got %d", origTree+8, got)
	}
	if got := m.diffView.Width; got != origDiffW-8 {
		t.Fatalf("diffView.Width: want %d, got %d", origDiffW-8, got)
	}

	out, _ = m.handleMouse(seamReleaseMsg(treeOuter+8, y), false)
	m = out.(*Model)
	if m.paneDrag.target != dividerNone {
		t.Fatalf("release should clear drag; target=%v", m.paneDrag.target)
	}
}

// TestPaneDragResizesControlsWidth: arm at the diff/controls seam,
// drag right by 6 cols, assert controlsPaneWidth shrunk by 6 and the
// diff viewport grew by 6.
func TestPaneDragResizesControlsWidth(t *testing.T) {
	m := detailFixtureModel(t)
	y := dragSeamY(t, m)
	_, controlsLeft := m.detailSeamColumns()

	origCtl := m.controlsPaneWidth
	origDiffW := m.diffView.Width

	out, _ := m.handleMouse(seamPressMsg(controlsLeft, y), false)
	m = out.(*Model)
	if m.paneDrag.target != dividerDiffControls {
		t.Fatalf("press on diff/controls seam should arm drag; target=%v", m.paneDrag.target)
	}

	out, _ = m.handleMouse(seamMotionMsg(controlsLeft+6, y), false)
	m = out.(*Model)
	if got := m.controlsPaneWidth; got != origCtl-6 {
		t.Fatalf("controlsPaneWidth: want %d, got %d", origCtl-6, got)
	}
	if got := m.diffView.Width; got != origDiffW+6 {
		t.Fatalf("diffView.Width: want %d, got %d", origDiffW+6, got)
	}
}

// TestPaneDragClampsBelowMin: drag the tree seam left far enough to
// drive treePaneWidth past minTreePaneWidth, assert clamp at min.
func TestPaneDragClampsBelowMin(t *testing.T) {
	m := detailFixtureModel(t)
	y := dragSeamY(t, m)
	treeOuter, _ := m.detailSeamColumns()

	out, _ := m.handleMouse(seamPressMsg(treeOuter, y), false)
	m = out.(*Model)

	// Aim 100 cells left of the seam — well past min on any fixture width.
	out, _ = m.handleMouse(seamMotionMsg(treeOuter-100, y), false)
	m = out.(*Model)
	if got := m.treePaneWidth; got != minTreePaneWidth {
		t.Fatalf("treePaneWidth clamp: want %d, got %d", minTreePaneWidth, got)
	}
}

// TestPaneDragClampsDiffMin: drag the controls seam left far enough
// that the diff would shrink below controlsAutoHideMinDiffWidth, and
// assert the resulting widths leave the diff at the threshold.
func TestPaneDragClampsDiffMin(t *testing.T) {
	m := detailFixtureModel(t)
	y := dragSeamY(t, m)
	_, controlsLeft := m.detailSeamColumns()

	out, _ := m.handleMouse(seamPressMsg(controlsLeft, y), false)
	m = out.(*Model)

	out, _ = m.handleMouse(seamMotionMsg(controlsLeft-200, y), false)
	m = out.(*Model)
	if m.diffView.Width < controlsAutoHideMinDiffWidth-prDetailPanel.GetHorizontalFrameSize() {
		t.Fatalf("diff viewport squeezed below clamp threshold: width=%d (frame=%d, threshold=%d)",
			m.diffView.Width,
			prDetailPanel.GetHorizontalFrameSize(),
			controlsAutoHideMinDiffWidth-prDetailPanel.GetHorizontalFrameSize())
	}
}

// TestNoSeamWhenDiffOnly: with diffOnly=true the pane row is the
// full-width diff and there are no seams to grab.
func TestNoSeamWhenDiffOnly(t *testing.T) {
	m := detailFixtureModel(t)
	m.diffOnly = true
	m.refreshDetailViews()
	y := dragSeamY(t, m)
	// In diffOnly the seam columns from the live layout are nominal —
	// we still must reject the click. Sweep the entire width to confirm.
	for x := 0; x < m.width; x++ {
		if got := m.seamAtPoint(x, y); got != dividerNone {
			t.Fatalf("X=%d: diffOnly should disable all seams, got %v", x, got)
		}
	}
}

// TestNoControlsSeamWhenHidden: when the controls pane is collapsed
// (user hit `c`) the diff/controls seam disappears and only the
// tree/diff seam is reachable.
func TestNoControlsSeamWhenHidden(t *testing.T) {
	m := detailFixtureModel(t)
	m.controlsUserHidden = true
	m.refreshDetailViews()
	y := dragSeamY(t, m)

	treeOuter, controlsLeft := m.detailSeamColumns()
	if got := m.seamAtPoint(treeOuter, y); got != dividerTreeDiff {
		t.Fatalf("tree/diff seam should still arm with controls hidden: got %v", got)
	}
	if got := m.seamAtPoint(controlsLeft-1, y); got != dividerNone {
		t.Fatalf("controls seam should be unreachable when hidden; got %v", got)
	}
	if got := m.seamAtPoint(controlsLeft, y); got != dividerNone {
		t.Fatalf("controls seam should be unreachable when hidden; got %v", got)
	}
}

// TestSpuriousMotionWithoutDragIsNoOp: a motion event arriving with no
// active drag (e.g. the user moved the mouse over the detail view
// without pressing first) must not mutate pane widths.
func TestSpuriousMotionWithoutDragIsNoOp(t *testing.T) {
	m := detailFixtureModel(t)
	y := dragSeamY(t, m)
	origTree := m.treePaneWidth
	origCtl := m.controlsPaneWidth

	out, _ := m.handleMouse(seamMotionMsg(0, y), false)
	m = out.(*Model)
	if m.treePaneWidth != origTree || m.controlsPaneWidth != origCtl {
		t.Fatalf("spurious motion should not resize panes; tree=%d ctl=%d", m.treePaneWidth, m.controlsPaneWidth)
	}
	if m.paneDrag.target != dividerNone {
		t.Fatalf("spurious motion should not arm drag; target=%v", m.paneDrag.target)
	}
}

// TestSeamPressTakesPrecedenceOverTreeRow: clicking exactly on the
// right border of the tree pane (which sits in the same Y range as a
// tree row) must arm a drag rather than fall through to the row's
// click handler.
func TestSeamPressTakesPrecedenceOverTreeRow(t *testing.T) {
	m := detailFixtureModel(t)
	// Pick a Y that sits inside a tree-row zone.
	m.treeIdx = 1
	m.refreshDetailViews()
	_ = m.View()
	top, _, ok := m.detailPaneRowYRange()
	if !ok {
		t.Fatalf("pane row range unavailable")
	}
	treeOuter, _ := m.detailSeamColumns()
	// Y just inside the tree pane content.
	y := top + 3

	out, _ := m.handleMouse(seamPressMsg(treeOuter-1, y), false)
	m = out.(*Model)
	if m.paneDrag.target != dividerTreeDiff {
		t.Fatalf("seam press should arm drag over tree-row click; target=%v", m.paneDrag.target)
	}
	// treeIdx must not have moved as a side-effect of the press.
	if m.treeIdx != 1 {
		t.Fatalf("seam press should not change tree selection; treeIdx=%d", m.treeIdx)
	}
}
