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

// TestReviewOverlayBodyClickReachesReviewModelViaFullUpdate is the
// regression test for "mouse no longer works inside the review window
// while the review is running". The existing
// TestReviewOverlayClickThroughChromeDeliversMouseToContent dispatches
// the mouse message DIRECTLY to overlayStack.Update, which bypasses
// the model-level shouldPassMouseToBackground gate entirely. That
// gate is the one piece of routing the user's clicks actually flow
// through from the IDE — if it incorrectly forwards inside-modal
// clicks to the detail mouse handler, the agent rows look completely
// unresponsive even though the lib-level tests pass.
//
// We click inside the agent-row body area, AND do it through
// m.Update (the same entry point Bubble Tea hands user mouse events
// to). The AgentCursor moving to the target index proves the click
// reached the review model, which is only possible if
// shouldPassMouseToBackground kept the event with the overlay stack.
//
// We click TWICE on different rows to also catch state-corruption
// regressions where the first click works but a second click on a
// different row doesn't (e.g. if the chrome handler accidentally
// latches Dragging=true on a non-tab cell, or if a stale zone-iteration
// reference makes subsequent zone.Get lookups miss).
func TestReviewOverlayBodyClickReachesReviewModelViaFullUpdate(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{Demo: true})
	m.width = 160
	m.height = 50
	m.mode = modeDetail
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})

	// Render the full model view (detail page + composited overlay) —
	// this is what the user's TUI actually paints, so zone bounds
	// scanned from this output are the ones bubblezone uses for hit
	// testing in production.
	//
	// IMPORTANT: m.View() already calls zone.Scan internally on the
	// composited output (see view.go), so we DO NOT wrap it in another
	// zone.Scan — bubblezone increments an iteration counter on every
	// Scan and async-deletes zones from prior iterations. A redundant
	// outer Scan would scan the (already-stripped) output, find no
	// markers, bump the iteration, and silently wipe every zone we
	// just registered. That race is what caused this test to flake
	// while debugging — the production TUI calls View() once per
	// render cycle, so the bug only surfaces with the double-scan
	// pattern.
	out := m.View()

	const targetIdx = 2
	targetName := reviewtab.AgentZoneName(targetIdx)
	// bubblezone's Scan ships zones to a buffered channel and a
	// background goroutine commits them to the lookup map. The
	// commit can lag the Scan call by enough scheduler slop that an
	// immediate zone.Get returns nil — bubblezone's docs flag this
	// explicitly, recommending Get for mouse handlers (which arrive
	// well after the View flush). In tests, View → Get is back-to-back,
	// so we wait briefly for the worker to drain rather than reading
	// raw output. This is *not* the bug under test — see waitBubbleZone
	// for the same pattern in TestReviewOverlayMousePassThroughToDetailTree.
	waitBubbleZone(t, targetName)
	target := zone.Get(targetName)
	if target == nil || target.IsZero() {
		t.Fatalf("expected zone %q to be registered after View(); got %+v\noutput:\n%s", targetName, target, out)
	}
	clickX := (target.StartX + target.EndX) / 2
	clickY := (target.StartY + target.EndY) / 2

	clickMsg := tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Type:   tea.MouseLeft,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}

	// The bug surfaces here: if MouseTargetsTop reports the click is
	// outside the modal (false), m.Update routes it to handleMouse
	// (detail mouse handler) which has no agent-row logic, so the
	// click is silently dropped and AgentCursor never moves.
	if !m.overlayStack.MouseTargetsTop(clickMsg, m.width, m.height) {
		top, left, _, ok := m.overlayStack.TopChromeLayout(m.width, m.height)
		t.Fatalf("MouseTargetsTop returned false for click (%d,%d) inside zone %q (X[%d..%d] Y[%d..%d]) — chrome top=%d left=%d (ok=%v). The shouldPassMouseToBackground gate will forward this to the detail handler and the click will look unresponsive to the user",
			clickX, clickY, targetName, target.StartX, target.EndX, target.StartY, target.EndY, top, left, ok)
	}

	if got := ro.AgentCursor(); got == targetIdx {
		t.Fatalf("precondition failed: AgentCursor already at target idx %d before any click — choose a different row", targetIdx)
	}

	m.Update(clickMsg)

	if got := ro.AgentCursor(); got != targetIdx {
		t.Fatalf("click at (%d,%d) inside agent row %d via m.Update did not move AgentCursor (got %d). The model-level pass-through gate likely forwarded the click to the detail mouse handler instead of the overlay stack",
			clickX, clickY, targetIdx, got)
	}

	// Now click a DIFFERENT row to verify subsequent clicks still
	// route correctly. If a previous click leaves chrome state in a
	// bad spot (e.g. Dragging=true) MouseTargetsTop will continue
	// returning true (chrome won't release until a Release event)
	// but the chrome handler will treat motion/press as drag and the
	// review tab will never see them.
	const secondIdx = 1
	secondName := reviewtab.AgentZoneName(secondIdx)
	// Re-render so zone bounds reflect any state change after the
	// first click (e.g. expanded row reflowing). Wait for the
	// bubblezone worker to commit the new bounds — same race as
	// above, made explicit so the test isn't flaky.
	_ = m.View()
	waitBubbleZone(t, secondName)
	secondZone := zone.Get(secondName)
	if secondZone == nil || secondZone.IsZero() {
		t.Fatalf("expected zone %q to be re-registered after first click + View(); got %+v", secondName, secondZone)
	}
	secondClick := tea.MouseMsg{
		X:      (secondZone.StartX + secondZone.EndX) / 2,
		Y:      (secondZone.StartY + secondZone.EndY) / 2,
		Type:   tea.MouseLeft,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	m.Update(secondClick)
	if got := ro.AgentCursor(); got != secondIdx {
		t.Fatalf("second click at (%d,%d) inside agent row %d via m.Update did not move AgentCursor (got %d). The first click likely left chrome state in a bad spot — check Dragging / Resizing flags in LayerState",
			secondClick.X, secondClick.Y, secondIdx, got)
	}
}

// TestReviewOverlayBodyClickAfterMinimizeRestore exercises the
// specific user-reported sequence "now the mouse no longer works
// inside the review window while the review is running": the user
// minimizes the modal, sees the spinner ticking in the title strip,
// restores it, then clicks an agent row. The terminal screenshot
// during the report showed the modal in a minimized state, so the
// most likely real path is "restore → click body" — and that's what
// this test pins. If anything about the minimize-then-restore cycle
// leaves chrome state in a bad place (Dragging latched, Resizing
// latched, content size un-resync'd, a stale OriginTop/Left from
// minimized positioning, etc.) the click after restore will get
// swallowed and AgentCursor will stay put.
func TestReviewOverlayBodyClickAfterMinimizeRestore(t *testing.T) {
	zone.NewGlobal()
	m := New(Options{Demo: true})
	m.width = 160
	m.height = 50
	m.mode = modeDetail
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	_ = m.View()

	// Step 1: click the [-] minimize button.
	_, _, regs, ok := m.overlayStack.TopChromeLayout(m.width, m.height)
	if !ok {
		t.Fatal("TopChromeLayout returned ok=false before any interaction")
	}
	topPos, leftPos, _, _ := m.overlayStack.TopChromeLayout(m.width, m.height)
	minimizeClick := tea.MouseMsg{
		X:      leftPos + regs.MinimizeX + regs.MinimizeW/2,
		Y:      topPos + regs.MinimizeY,
		Type:   tea.MouseLeft,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	if mod, _ := m.Update(minimizeClick); mod != nil {
		m = mod.(*Model)
	}
	_ = m.View()

	// Step 2: click the [+] restore button (same screen position;
	// the chrome glyph flipped but the hit rect is the same because
	// MinimizeButtonWidth is fixed). Re-derive coords from the
	// current layout in case the modal moved when it collapsed —
	// EntryClampedOrigin recenters based on the new (smaller) modal
	// when no explicit Origin is set.
	topPos, leftPos, regs, ok = m.overlayStack.TopChromeLayout(m.width, m.height)
	if !ok {
		t.Fatal("TopChromeLayout returned ok=false after minimize")
	}
	restoreClick := tea.MouseMsg{
		X:      leftPos + regs.MinimizeX + regs.MinimizeW/2,
		Y:      topPos + regs.MinimizeY,
		Type:   tea.MouseLeft,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	if mod, _ := m.Update(restoreClick); mod != nil {
		m = mod.(*Model)
	}
	_ = m.View()

	// Step 3: now click on agent row 2 — this is what the user is
	// pressing and reporting as dead.
	const targetIdx = 2
	targetName := reviewtab.AgentZoneName(targetIdx)
	waitBubbleZone(t, targetName)
	target := zone.Get(targetName)
	if target == nil || target.IsZero() {
		t.Fatalf("expected zone %q to be registered after restore + View(); got %+v", targetName, target)
	}
	bodyClick := tea.MouseMsg{
		X:      (target.StartX + target.EndX) / 2,
		Y:      (target.StartY + target.EndY) / 2,
		Type:   tea.MouseLeft,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	preCursor := ro.AgentCursor()
	if preCursor == targetIdx {
		t.Fatalf("precondition failed: AgentCursor already at target idx %d", targetIdx)
	}
	m.Update(bodyClick)
	if got := ro.AgentCursor(); got != targetIdx {
		t.Fatalf("click at (%d,%d) inside agent row %d after minimize/restore cycle did NOT move AgentCursor (%d → %d). This matches the user-reported regression — the minimize/restore left chrome state in a bad spot that swallows body clicks",
			bodyClick.X, bodyClick.Y, targetIdx, preCursor, got)
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
