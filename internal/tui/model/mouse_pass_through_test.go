package model

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/zones"

	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
)

// TestReviewOverlayMousePassThroughToDetailTree verifies the headline UX
// for this feature: while a review overlay is open over the PR detail
// page, clicks on the detail file tree (outside the modal rect) reach
// the detail mouse handler so the user can browse files in parallel
// with the AI review running in the background.
//
// Without the shouldPassMouseToBackground branch in the model dispatch,
// the click would be swallowed by overlayStack.Update and the treeIdx
// would stay pinned wherever the user left it.
//
// We deliberately widen the viewport before pushing the overlay: the
// review modal is clamped to 140 cells (set in resizeFromScreen), so in
// a 320-wide terminal it occupies columns ~90-230 and leaves the
// left-anchored tree pane completely clear of the modal rect. The
// detail fixture's default 160-wide viewport doesn't have that margin —
// the modal there covers the tree zone end-marker, so bubblezone can't
// register TreeFile(0) after the overlay is on top.
func TestReviewOverlayMousePassThroughToDetailTree(t *testing.T) {
	m := detailFixtureModel(t)
	m.Update(tea.WindowSizeMsg{Width: 320, Height: 50})
	// Pin the tree pane narrow so it stays well to the left of the
	// centered modal. Without this the layout sometimes hands the
	// tree a wider slot and the row-end marker lands under the modal.
	m.treePaneWidth = minTreePaneWidth
	m.relayout()
	m.treeIdx = len(m.treeRows) - 1
	m.selectedFilePath = m.treeRows[m.treeIdx].Path
	m.refreshDetailViews()
	_ = m.View()
	waitBubbleZone(t, zones.TreeFile(0))

	// Push the review overlay just like startReviewOverlay would. We
	// don't need the runner to be actually live; we just need the
	// stack to be non-empty with the review modal on top so the
	// pass-through gate (reviewOverlayOnTop) sees it.
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	_ = m.View()
	// The composite View() above re-runs zone.Scan, which means the
	// TreeFile zone bounds we read next come from the post-overlay
	// render. As long as the modal sits to the right of the tree
	// pane, those bounds are unchanged from the pre-overlay scan.
	waitBubbleZone(t, zones.TreeFile(0))

	clickMsg := clickCenterOfZone(t, zones.TreeFile(0))
	if m.overlayStack.MouseTargetsTop(clickMsg, m.width, m.height) {
		t.Fatalf("test precondition: click at TreeFile(0) (%d, %d) should be outside the review modal — viewport=%dx%d, treePaneWidth=%d", clickMsg.X, clickMsg.Y, m.width, m.height, m.treePaneWidth)
	}

	out, _ := m.Update(clickMsg)
	m2 := out.(*Model)
	if m2.treeIdx != 0 {
		t.Fatalf("expected pass-through click to set treeIdx=0 (first file), got %d — the overlay stack is still swallowing background clicks", m2.treeIdx)
	}
	if m2.selectedFilePath != m2.treeRows[0].Path {
		t.Fatalf("selectedFilePath did not follow the click: got %q want %q", m2.selectedFilePath, m2.treeRows[0].Path)
	}
}

// TestReviewOverlayMouseStillTrappedInsideModal makes sure the
// pass-through only affects clicks that land OUTSIDE the modal — clicks
// inside (chrome tab, [x], body content) must still route to the
// overlay so the user can drag, close, expand agent rows, etc.
//
// We verify this by clicking the chrome's [x] button and asserting the
// overlay was popped. If the routing accidentally treated inside-modal
// clicks as pass-through, the [x] click would land on the underlying
// detail page instead and the modal would stay open.
func TestReviewOverlayMouseStillTrappedInsideModal(t *testing.T) {
	m := detailFixtureModel(t)
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	_ = m.View()

	if m.overlayStack.Depth() != 1 {
		t.Fatalf("precondition: expected one overlay on stack, got %d", m.overlayStack.Depth())
	}

	// Reach into the library for the painted close-button position so
	// the test isn't coupled to the modal's exact placement on screen.
	top, left, regs, ok := m.overlayStack.TopChromeLayout(m.width, m.height)
	if !ok {
		t.Fatal("expected TopChromeLayout to report ok for the pushed review overlay")
	}
	closeMsg := tea.MouseMsg{
		X:      left + regs.CloseX + regs.CloseW/2,
		Y:      top + regs.CloseY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	if !m.overlayStack.MouseTargetsTop(closeMsg, m.width, m.height) {
		t.Fatalf("test precondition: click at (%d, %d) should land on the chrome close button — MouseTargetsTop returned false", closeMsg.X, closeMsg.Y)
	}

	m.Update(closeMsg)
	if m.overlayStack.Depth() != 0 {
		t.Fatalf("expected chrome [x] click to pop the overlay (depth want 0, got %d) — inside-modal clicks must not pass through to the detail handler", m.overlayStack.Depth())
	}
}

// TestReviewOverlayPassThroughOnlyInDetailMode pins the gating decision
// from shouldPassMouseToBackground: pass-through only fires in
// modeDetail. Clicks while we're on the PR list MUST stay with the
// overlay even when they land outside the modal — letting a click
// fall through to the PR list handler could trigger a second review or
// switch PRs while the first review is still in flight.
func TestReviewOverlayPassThroughOnlyInDetailMode(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 42})
	// Stay in modeList (the default) — DON'T flip to modeDetail.

	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	_ = m.View()

	// (0, 0) is unambiguously outside the centered review modal.
	outsideClick := tea.MouseMsg{
		X:      0,
		Y:      0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	if m.shouldPassMouseToBackground(outsideClick) {
		t.Fatalf("shouldPassMouseToBackground returned true in modeList — clicks must stay with the overlay outside of modeDetail")
	}
}

// TestReviewOverlayPassThroughGatedOnReviewOverlayOnly guards against
// the pass-through accidentally activating for non-review overlays
// (error overlays, confirm prompts, bulk-post dialogs, etc.). Those
// overlays demand the user's full attention; falling through them to
// the detail handler would be confusing at best and dangerous at worst.
func TestReviewOverlayPassThroughGatedOnReviewOverlayOnly(t *testing.T) {
	m := detailFixtureModel(t)
	// Push something that is decidedly NOT a *reviewtab.Model.
	m.overlayStack.Push(staticOverlayModel{view: "decoy modal"}, reviewWindowConfig())
	_ = m.View()

	outsideClick := tea.MouseMsg{
		X:      0,
		Y:      0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	if m.shouldPassMouseToBackground(outsideClick) {
		t.Fatalf("shouldPassMouseToBackground returned true for a non-review overlay — only the review modal opts into pass-through")
	}
}

// staticOverlayModel is a do-nothing stack entry used for negative-case
// gating tests. It deliberately is not a *reviewtab.Model so the
// reviewOverlayOnTop() type assertion returns nil.
type staticOverlayModel struct{ view string }

func (staticOverlayModel) Init() tea.Cmd                         { return nil }
func (m staticOverlayModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m staticOverlayModel) View() string                        { return m.view }
