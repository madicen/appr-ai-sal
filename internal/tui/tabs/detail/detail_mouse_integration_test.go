package detail

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/zones"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
)

// waitBubbleZone blocks until bubblezone's async worker registers id (Scan posts
// zone bounds on a channel; Get before the worker runs returns nil).
func waitBubbleZone(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for zone.Get(id) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for zone %q", id)
		}
		runtime.Gosched()
	}
}

// threeFileDiff produces three files so the tree is shorter than the viewport
// height and bubbles/viewport pads with blank lines (the regression case).
const threeFileDiff = `diff --git a/aa.go b/aa.go
--- /dev/null
+++ b/aa.go
@@ -0,0 +1 @@
+a
diff --git a/bb.go b/bb.go
--- /dev/null
+++ b/bb.go
@@ -0,0 +1 @@
+b
diff --git a/cc.go b/cc.go
--- /dev/null
+++ b/cc.go
@@ -0,0 +1 @@
+c
`

// Detail PR body must stay within chromeBodyHeight so the composed View never
// exceeds the terminal (status bar stays visible; zones stay aligned).
func TestDetailRenderBodyFitsChromeBudget(t *testing.T) {
	m := detailFixtureModel(t)
	budget := m.host.ChromeBodyHeight()
	got := lipgloss.Height(m.View())
	if got > budget {
		t.Fatalf("renderBody height %d > chromeBodyHeight %d", got, budget)
	}
}

func detailFixtureModel(t *testing.T) *Model {
	t.Helper()
	zone.NewGlobal()
	host := newTestHost(160, 42)
	host.pr = &gh.PR{
		Repository: "o/r", Number: 1, Title: "title", Author: "a",
		BaseRef: "main", HeadRef: "feat", URL: "https://example.com", HeadSHA: "abc",
	}
	host.diff = review.ParseDiff(threeFileDiff)
	m := New(host, keys.Default())
	m.Resize(host.Width(), host.ChromeBodyHeight())
	m.OnPRLoaded(host.diff, nil)
	if len(m.treeRows) > 0 {
		m.selectedFilePath = m.treeRows[0].Path
	}
	m.focusedPane = paneTree
	m.RefreshViews()
	_ = zone.Scan(m.View())
	waitBubbleZone(t, zones.PaneTreeBody)
	return m
}

func clickBottomOfZone(t *testing.T, id string) tea.MouseMsg {
	t.Helper()
	waitBubbleZone(t, id)
	z := zone.Get(id)
	if z == nil {
		t.Fatalf("zone %q not registered — call View() first", id)
	}
	x := (z.StartX + z.EndX) / 2
	y := z.EndY
	return tea.MouseMsg{
		X:      x,
		Y:      y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
}

// clickCenterOfZone synthesizes a press at the middle screen row of a zone.
func clickCenterOfZone(t *testing.T, id string) tea.MouseMsg {
	t.Helper()
	waitBubbleZone(t, id)
	z := zone.Get(id)
	if z == nil {
		t.Fatalf("zone %q not registered — call View() first", id)
	}
	x := (z.StartX + z.EndX) / 2
	y := (z.StartY + z.EndY) / 2
	return tea.MouseMsg{
		X:      x,
		Y:      y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
}

// Bottom-of-viewport clicks often land in padded blank lines below the last item,
// so they exercise treeRowFromMouse's zones.PaneTreeBody + Pos path — not per-row
// zones.TreeFile bounds. Use clickCenterOfZone(zones.TreeFile(i)) to test row hit boxes.
// Clicks on the far right of the tree viewport must still resolve the row: bubblezone
// row zones must span full content width (padCellVisual), not only the styled glyphs.
func TestDetailMouseFarRightInTreeBodySelectsFirstFile(t *testing.T) {
	m := detailFixtureModel(t)
	m.treeIdx = len(m.treeRows) - 1
	m.selectedFilePath = m.treeRows[m.treeIdx].Path
	m.refreshDetailViews()
	_ = m.View()
	waitBubbleZone(t, zones.PaneTreeBody)
	waitBubbleZone(t, zones.TreeFile(0))
	zb := zone.Get(zones.PaneTreeBody)
	zr := zone.Get(zones.TreeFile(0))
	if zb == nil || zr == nil {
		t.Fatal("missing zones")
	}
	msg := tea.MouseMsg{
		X:      zb.EndX - 1,
		Y:      (zr.StartY + zr.EndY) / 2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	out, _ := m.handleMouse(msg, false)
	m2 := out.(*Model)
	if m2.treeIdx != 0 {
		t.Fatalf("far-right click should select first file; treeIdx=%d", m2.treeIdx)
	}
}

func TestDetailMouseCenterFirstTreeRowSelectsFirstFile(t *testing.T) {
	m := detailFixtureModel(t)
	firstPath := m.treeRows[0].Path
	m.treeIdx = len(m.treeRows) - 1
	m.selectedFilePath = m.treeRows[m.treeIdx].Path
	m.refreshDetailViews()
	_ = m.View()
	waitBubbleZone(t, zones.TreeFile(0))
	msg := clickCenterOfZone(t, zones.TreeFile(0))
	out, _ := m.handleMouse(msg, false)
	m2 := out.(*Model)
	if m2.treeIdx != 0 {
		t.Fatalf("treeIdx got %d want 0 (row zone misaligned?)", m2.treeIdx)
	}
	if m2.selectedFilePath != firstPath {
		t.Fatalf("selectedFilePath got %q want %q", m2.selectedFilePath, firstPath)
	}
}

func TestDetailMouseBottomTreeRowSelectsLastFile(t *testing.T) {
	m := detailFixtureModel(t)
	lastPath := m.treeRows[len(m.treeRows)-1].Path
	msg := clickBottomOfZone(t, zones.PaneTreeBody)
	out, _ := m.handleMouse(msg, false)
	m2 := out.(*Model)
	if m2.treeIdx != len(m2.treeRows)-1 {
		t.Fatalf("treeIdx got %d want last index %d", m2.treeIdx, len(m2.treeRows)-1)
	}
	if m2.selectedFilePath != lastPath {
		t.Fatalf("selectedFilePath got %q want %q", m2.selectedFilePath, lastPath)
	}
	if !strings.HasSuffix(lastPath, "cc.go") {
		t.Fatalf("expected last path cc.go, got %q", lastPath)
	}
}

func TestDetailMouseBottomDiffPaneFocusesDiff(t *testing.T) {
	m := detailFixtureModel(t)
	m.focusedPane = paneTree
	msg := clickBottomOfZone(t, zones.PaneDiffBody)
	out, _ := m.handleMouse(msg, false)
	m2 := out.(*Model)
	if m2.focusedPane != paneDiff {
		t.Fatalf("focusedPane got %v want paneDiff", m2.focusedPane)
	}
}
