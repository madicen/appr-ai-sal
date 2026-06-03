package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	overlay "github.com/madicen/bubble-overlay"

	reviewtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/review"
)

func init() { zone.NewGlobal() }

// TestReviewWindowConfigEnablesDraggableChrome locks every WindowChrome
// feature the review modal opts into. The library exposes a long menu of
// chrome capabilities (drag, close, resize, keyboard nudges) and we want a
// loud failure if any future refactor silently disables one.
func TestReviewWindowConfigEnablesDraggableChrome(t *testing.T) {
	cfg := reviewWindowConfig()
	wc := cfg.WindowChrome
	if !wc.Enabled {
		t.Fatalf("expected WindowChrome to be enabled")
	}
	if !wc.Draggable {
		t.Fatalf("expected WindowChrome.Draggable=true")
	}
	if !wc.ShowCloseButton {
		t.Fatalf("expected WindowChrome.ShowCloseButton=true")
	}
	if !wc.AutoWrap {
		// Without AutoWrap the library returns the model view unchanged
		// (no tab, no border) — we'd be back to drawing the modal frame
		// ourselves.
		t.Fatalf("expected WindowChrome.AutoWrap=true")
	}
	if !wc.Resizable {
		t.Fatalf("expected WindowChrome.Resizable=true")
	}
	if !wc.Keyboard {
		t.Fatalf("expected WindowChrome.Keyboard=true (Alt+arrow move, Alt+Shift+arrow resize)")
	}
	if wc.KeyStep <= 0 {
		t.Fatalf("expected positive WindowChrome.KeyStep (got %d)", wc.KeyStep)
	}
	if wc.MinWidth <= 0 || wc.MinHeight <= 0 {
		t.Fatalf("expected MinWidth/MinHeight to be set so the resize handle has a floor (got %d × %d)", wc.MinWidth, wc.MinHeight)
	}
	// We no longer set ChromeMaskRune explicitly — the bubble-overlay
	// default is now U+E000 (Private Use Area), which can't appear in
	// real content and so never triggers the transparent-mergeMaskRune
	// hole-punching we used to defend against by overriding the rune
	// ourselves. If the upstream default ever regresses to U+FFFC
	// (OBJECT REPLACEMENT CHARACTER) we want a loud failure here so we
	// remember to re-set it locally.
	effectiveMask := wc.ChromeMaskRune
	if effectiveMask == 0 {
		effectiveMask = overlay.DefaultChromeMaskRune
	}
	if effectiveMask == '\uFFFC' {
		t.Fatalf("ChromeMaskRune effective value must not be U+FFFC — real content occasionally emits that rune, which would punch holes through the modal (got %U)", effectiveMask)
	}
	if cfg.CloseOnEscape {
		// The review model owns its own esc/q handling; the chrome must not
		// short-circuit it or in-flight reviews would dismiss on a stray esc.
		t.Fatalf("expected CloseOnEscape=false")
	}
	if cfg.CloseOnClickOutside {
		t.Fatalf("expected CloseOnClickOutside=false")
	}
	if got := wc.Title; got == "" {
		t.Fatalf("expected non-empty WindowChrome.Title (got %q)", got)
	}
	if !wc.ShowMinimizeButton {
		// ShowMinimizeButton renders [-]/[+] in the chrome tab so the
		// user can collapse the review modal to its title bar while
		// the AI review runs in the background. Without it the only
		// way to clear the modal is closing it entirely, which kills
		// the in-flight review.
		t.Fatalf("expected WindowChrome.ShowMinimizeButton=true")
	}
	if cfg.DimOpacity != 0 {
		// The minimize affordance is paired with a non-dimmed
		// background — once the user collapses the modal, anything
		// behind it should remain bright and clickable (see
		// shouldPassMouseToBackground for the click side).
		t.Fatalf("expected DimOpacity=0 so the background stays bright behind / around the modal (got %v)", cfg.DimOpacity)
	}
}

// TestReviewModalRendersWithTabChrome composites the review overlay through the
// stack and asserts the rendered output contains the tab title, the close
// glyph, and the box-border characters (Resizable=true uses tab-on-border so
// `┴` should appear at the tab/box junction). Without the chrome wiring the
// rendered modal would just be the unframed review body.
func TestReviewModalRendersWithTabChrome(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 120
	m.height = 44

	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	cfg := reviewWindowConfig()
	m.overlayStack.Push(ro, cfg)
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})

	out := zone.Scan(m.overlayStack.View("base", m.width, m.height))
	if !strings.Contains(out, cfg.WindowChrome.Title) {
		t.Fatalf("rendered overlay should contain tab title %q\nGOT:\n%s", cfg.WindowChrome.Title, out)
	}
	if !strings.Contains(out, "[x]") {
		t.Fatalf("rendered overlay should contain the chrome close glyph [x]\nGOT:\n%s", out)
	}
	// Resizable=true draws the tab on top of the box border, so the right
	// edge of the tab merges with the top border via `┴`. The frame uses
	// the bubble-overlay default rounded border, so the four corners are
	// `╭ ╮ ╰ ╯`. If Resizable accidentally regresses to false, the body
	// will float without a box border and these glyphs will disappear.
	for _, glyph := range []string{"┴", "╭", "╮", "╰", "╯"} {
		if !strings.Contains(out, glyph) {
			t.Fatalf("rendered resizable overlay should contain box-border glyph %q\nGOT:\n%s", glyph, out)
		}
	}
}

// TestReviewOverlayCloseFromChromeEmitsCloseMsg pins the contract between the
// chrome's [x] click (which the library handles by calling Pop) and the root
// model's cleanup. The library calls OnOverlayClose on the popped model AFTER
// removing it from the stack; the returned cmd must yield a CloseMsg so the
// root's existing CloseMsg handler clears m.currentReviewOverlay.
func TestReviewOverlayCloseFromChromeEmitsCloseMsg(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 80
	m.height = 30
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro

	// Pop calls OnOverlayClose on the popped entry and returns its cmd. The
	// chrome's close button takes the same path internally when the user
	// clicks [x].
	_, cmd := m.overlayStack.Pop()
	if cmd == nil {
		t.Fatalf("expected a non-nil cmd from Pop (review.OnOverlayClose should emit CloseMsg)")
	}
	msg := cmd()
	if _, ok := msg.(reviewtab.CloseMsg); !ok {
		t.Fatalf("expected CloseMsg from OnOverlayClose, got %T", msg)
	}
}

// TestReviewOverlayDynamicTabTitle locks in the OverlayTitler integration —
// the chrome's tab strip should reflect the review phase ("running" while the
// pipeline is in flight) rather than the static fallback string the config
// was constructed with. Without OverlayTitler the user couldn't tell from
// the chrome whether the modal was still running, awaiting approval, or
// showing the final summary.
func TestReviewOverlayDynamicTabTitle(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 120
	m.height = 44
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})

	out := m.overlayStack.View("base", m.width, m.height)
	// titleForPhase / chromeSubtitleForPhase emit "appr-ai-sal · review · running"
	// while phase == phaseRunning, so the chrome should paint that.
	const wantSubstring = "running"
	if !strings.Contains(out, wantSubstring) {
		t.Fatalf("expected phase-aware tab title containing %q while review is running, got:\n%s", wantSubstring, out)
	}
}

// TestReviewOverlayBubblezoneMarkersSurviveChrome is the regression test
// for the bubble-overlay opaque-row splice fix. Before the fix, the chrome's
// auto-wrap path routed every modal row through cellbuf, which silently
// stripped zero-width CSI sequences like bubblezone's `\x1B[<id>z`
// markers — meaning the running-phase agent rows had zones registered in
// bubblezone's global state but `zone.Scan(stack.View(...))` couldn't find
// any of them, so clicks landed nowhere.
func TestReviewOverlayBubblezoneMarkersSurviveChrome(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 120
	m.height = 44
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})

	out := zone.Scan(m.overlayStack.View("base", m.width, m.height))
	// Agent rows register zones named "zone:overlay:agent:N" in the running
	// phase. If chrome compositing strips bubblezone markers, none of those
	// zones will be discoverable via zone.Get → its returned ZoneInfo will
	// be the "uninitialised" sentinel that reports IsZero() == true.
	z := zone.Get(reviewtab.AgentZoneName(0))
	if z == nil || z.IsZero() {
		t.Fatalf("expected bubblezone to find agent-row zone after chrome compositing (got %+v); raw output for context:\n%s", z, out)
	}
}

// TestReviewOverlayClickThroughChromeDeliversMouseToContent is the
// end-to-end version of the marker-survival test above: it verifies the
// full chain works — chrome composites the modal, bubblezone markers
// survive, the stack delivers the mouse message to the review model when
// the click lands inside the modal body, and the model's mouse handler
// actually maps the screen coordinates back to the right agent row via
// zone.Get(...).InBounds.
//
// This is the test the user was missing when they reported "mouse clicks
// were not going through to the content of the overlay window". The
// previous chain broke at the cellbuf-strips-zone-markers step, so zone
// lookups returned the IsZero sentinel and handleMouse fell through to
// the viewport scroll path silently.
func TestReviewOverlayClickThroughChromeDeliversMouseToContent(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 120
	m.height = 44
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})

	// Render through bubblezone.Scan so the global zone manager registers
	// the markers' on-screen bounds for the next zone.Get call.
	out := zone.Scan(m.overlayStack.View("base", m.width, m.height))

	// Use the 3rd agent row — different from the default cursor (0) — so
	// "the click landed" produces a visible state change. Row 2 is the
	// "repo-experts" context-injection row, which is always rendered in
	// the running phase regardless of demo / agent config.
	const targetIdx = 2
	targetName := reviewtab.AgentZoneName(targetIdx)
	target := zone.Get(targetName)
	if target == nil || target.IsZero() {
		t.Fatalf("expected zone %q to be discoverable after chrome compositing (got %+v); raw output for context:\n%s", targetName, target, out)
	}

	// Aim at the middle of the zone — anywhere inside the cell rect is
	// fine; the middle minimises the chance that a future renderer tweak
	// (e.g. trimming trailing whitespace) shrinks the zone by a column at
	// either edge.
	x0, y0 := target.StartX, target.StartY
	x1, y1 := target.EndX, target.EndY
	clickX := (x0 + x1) / 2
	clickY := (y0 + y1) / 2

	if got := ro.AgentCursor(); got == targetIdx {
		t.Fatalf("precondition failed: AgentCursor already at target idx %d before any click — choose a different row", targetIdx)
	}

	m.overlayStack.Update(tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Type:   tea.MouseLeft,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})

	if got := ro.AgentCursor(); got != targetIdx {
		t.Fatalf("click at (%d, %d) inside zone %q (bounds X[%d..%d] Y[%d..%d]) failed to move AgentCursor to %d (got %d) — the stack either didn't deliver the mouse message to the review model, or the model's zone lookup didn't match the chromed compositor's coordinates",
			clickX, clickY, targetName, x0, x1, y0, y1, targetIdx, got)
	}
}

// TestReviewOverlayMinimizeClickCollapsesAndRestoresChrome is the
// end-to-end test for the chrome's minimize affordance. Clicking the
// [-] button must collapse the modal to its tab strip (painted height
// drops below the expanded body), and a follow-up click on the same
// region (now a [+] glyph) must restore the body to its original size.
//
// We measure painted output via the modal's own RenderEntryModal call
// rather than the full composited view so the line count actually
// reflects the modal's height (stack.View pads to the viewport).
func TestReviewOverlayMinimizeClickCollapsesAndRestoresChrome(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 160
	m.height = 50
	m.mode = modeDetail
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	_ = m.View()

	expandedHeight := topEntryModalHeight(t, &m.overlayStack)

	// Locate the chrome's minimize button via TopChromeLayout — it
	// applies effectiveConfig (so the OverlayTitler-driven dynamic
	// title is reflected in the tab geometry) and returns absolute
	// hit-rect coordinates ready to feed to MouseMsg.
	top, left, regs, ok := m.overlayStack.TopChromeLayout(m.width, m.height)
	if !ok {
		t.Fatal("TopChromeLayout returned ok=false for a pushed chromed review overlay")
	}
	if regs.MinimizeW == 0 {
		t.Fatal("expected a non-zero minimize hit rect — reviewWindowConfig should opt into ShowMinimizeButton")
	}
	minimizeClick := tea.MouseMsg{
		X:      left + regs.MinimizeX + regs.MinimizeW/2,
		Y:      top + regs.MinimizeY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}

	if c := m.overlayStack.Update(minimizeClick); c != nil {
		// Drain any follow-up cmd (notifyMinimize batches the
		// OverlayMinimizedMsg delivery + the model hook).
		_ = c()
	}
	if m.overlayStack.Depth() != 1 {
		t.Fatalf("minimize click should not pop the overlay (depth=%d want 1)", m.overlayStack.Depth())
	}
	minimizedHeight := topEntryModalHeight(t, &m.overlayStack)
	if minimizedHeight >= expandedHeight {
		t.Fatalf("expected modal height to drop after minimize click; expanded=%d minimized=%d", expandedHeight, minimizedHeight)
	}

	// Now click again — the toggle's hit rect stays in the same
	// position (RestoreButtonGlyph has the same width), so we can
	// reuse the original click coords. We rebuild via TopChromeLayout
	// in case the modal's origin shifted as it shrank.
	top2, left2, regs2, _ := m.overlayStack.TopChromeLayout(m.width, m.height)
	restoreClick := tea.MouseMsg{
		X:      left2 + regs2.MinimizeX + regs2.MinimizeW/2,
		Y:      top2 + regs2.MinimizeY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	if c := m.overlayStack.Update(restoreClick); c != nil {
		_ = c()
	}
	restoredHeight := topEntryModalHeight(t, &m.overlayStack)
	if restoredHeight != expandedHeight {
		t.Fatalf("expected modal height to bounce back to %d after restore click, got %d", expandedHeight, restoredHeight)
	}
}

// TestReviewOverlayDoubleClickTitleBarTogglesMinimize exercises the
// OS-style double-click-the-title-bar gesture end-to-end through the
// app's overlay stack. The library handles the gesture entirely
// (recording LastTabPressAt on the first press, then toggling
// Minimized on a second press within DoubleClickThreshold), so this
// test asserts the wiring: clicking the tab drag area twice in quick
// succession must collapse the modal, and clicking it twice more
// must restore it.
//
// We click at column TabLeft+1 — the lead space inside the tab body,
// which is plain background. That coordinate cannot overlap the
// close or minimize button hit rects, so a press there routes to the
// drag-area branch where the double-click logic lives.
func TestReviewOverlayDoubleClickTitleBarTogglesMinimize(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 160
	m.height = 50
	m.mode = modeDetail
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	_ = m.View()

	expandedHeight := topEntryModalHeight(t, &m.overlayStack)

	tabPress := func() tea.MouseMsg {
		top, left, regs, ok := m.overlayStack.TopChromeLayout(m.width, m.height)
		if !ok {
			t.Fatal("TopChromeLayout returned ok=false after pushing the review overlay")
		}
		return tea.MouseMsg{
			X:      left + regs.TabLeft + 1,
			Y:      top + regs.TabTop + 1,
			Action: tea.MouseActionPress,
			Button: tea.MouseButtonLeft,
		}
	}

	// First press: arms the timestamp, starts a drag we'll never use.
	// Second press: within DoubleClickThreshold, fires the toggle.
	// We send both back-to-back with no sleep, so the wall-clock gap
	// is microseconds — well under the 500ms window.
	if c := m.overlayStack.Update(tabPress()); c != nil {
		_ = c()
	}
	if c := m.overlayStack.Update(tabPress()); c != nil {
		_ = c() // drain notifyMinimize batched cmd
	}

	minimizedHeight := topEntryModalHeight(t, &m.overlayStack)
	if minimizedHeight >= expandedHeight {
		t.Fatalf("double-click on title bar should collapse the modal: expanded=%d minimized=%d", expandedHeight, minimizedHeight)
	}
	if m.overlayStack.Depth() != 1 {
		t.Fatalf("double-click must not pop the overlay (depth=%d want 1)", m.overlayStack.Depth())
	}

	// Now double-click again — restores. After the toggle the
	// library cleared LastTabPressAt so the FIRST press here arms a
	// fresh timestamp and the SECOND triggers the restore. (If the
	// library didn't reset the tracker, the very first press here
	// would be treated as the second click of a stale gesture and
	// double-fire the toggle, leaving us collapsed again — that's
	// the regression this assertion guards against.)
	if c := m.overlayStack.Update(tabPress()); c != nil {
		_ = c()
	}
	if c := m.overlayStack.Update(tabPress()); c != nil {
		_ = c()
	}

	restoredHeight := topEntryModalHeight(t, &m.overlayStack)
	if restoredHeight != expandedHeight {
		t.Fatalf("expected modal height to bounce back to %d after double-click restore, got %d", expandedHeight, restoredHeight)
	}
}

// TestReviewOverlayTitleSpinnerGatedOnMinimize verifies the spinner
// only joins the title when BOTH conditions hold:
//
//   - The pipeline is still working (phaseRunning / phaseGeneratingSummary)
//   - The modal is currently minimized
//
// Rationale: while expanded, the running-phase body shows per-agent
// spinners, so duplicating one in the title competes with them for
// attention. While minimized, the tab strip is the only visible
// affordance, so the animation tells the user the review hasn't
// stalled.
func TestReviewOverlayTitleSpinnerGatedOnMinimize(t *testing.T) {
	m := New(Options{Demo: true})
	m.width = 160
	m.height = 50
	m.mode = modeDetail
	ro := reviewtab.New(m.width, m.height, true, false, false, nil, true)
	m.overlayStack.Push(ro, reviewWindowConfig())
	m.currentReviewOverlay = ro
	m.overlayStack.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})

	// Expanded + running: no spinner in the tab.
	expandedTitle := ro.OverlayTitle()
	if !strings.Contains(expandedTitle, "running") {
		t.Fatalf("running-phase title should contain the phase subtitle, got %q", expandedTitle)
	}
	plainWidth := lipgloss.Width(reviewtab.ChromeTitleFallback + " · running")
	if got := lipgloss.Width(expandedTitle); got != plainWidth {
		t.Fatalf("expanded title should match plain %q (width %d); got %q (width %d) — spinner should NOT splice while body is visible",
			reviewtab.ChromeTitleFallback+" · running", plainWidth, expandedTitle, got)
	}

	// Toggle to minimized. After this the same OverlayTitle() call
	// should pick up a spinner glyph in front of the subtitle.
	ro.OnOverlayMinimize(true)
	minimizedTitle := ro.OverlayTitle()
	if !strings.Contains(minimizedTitle, "running") {
		t.Fatalf("minimized title still needs the phase subtitle, got %q", minimizedTitle)
	}
	if got := lipgloss.Width(minimizedTitle); got <= plainWidth {
		t.Fatalf("minimized running title should be wider than the no-spinner baseline (%d) — spinner glyph missing; got %q (width %d)", plainWidth, minimizedTitle, got)
	}

	// Toggle back: spinner disappears again.
	ro.OnOverlayMinimize(false)
	restoredTitle := ro.OverlayTitle()
	if got := lipgloss.Width(restoredTitle); got != plainWidth {
		t.Fatalf("restoring should drop the spinner from the title; got %q (width %d) want plain (width %d)", restoredTitle, got, plainWidth)
	}
}

// topEntryModalHeight returns the painted line count of the top
// overlay's modal string, computed the same way bubble-overlay
// internally measures it (ModalCellSize). Used to assert minimize /
// restore visually changed the modal's footprint without coupling to
// the composited backdrop padding that OverlayStack.View applies.
func topEntryModalHeight(t *testing.T, s *overlay.OverlayStack) int {
	t.Helper()
	// We don't have direct access to OverlayStack's internal entries
	// here, but TopChromeLayout calls ModalCellSize for us as part of
	// the layout math — we just need a public way to read it. The
	// simplest approach: render the top entry's modal string via the
	// View pipeline and count lines.
	//
	// stack.View("base", w, h) returns a viewport-sized composite, so
	// instead we exploit the fact that TopChromeLayout encapsulates
	// ModalCellSize: its caller-visible return values include neither
	// mw nor mh, but we can derive mh from the chrome regions and the
	// expected resizable body height. To keep this test free of those
	// internals, just inspect the line difference in the composited
	// view by comparing the count of non-blank rows.
	view := s.View(strings.Repeat(" ", 160), 160, 50)
	// The modal body is the rectangle of rows that ever contain
	// non-space content. Counting those gives a stable proxy for the
	// modal's height regardless of where the chrome decides to position
	// it inside the viewport.
	lines := strings.Split(view, "\n")
	nonBlank := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonBlank++
		}
	}
	return nonBlank
}
