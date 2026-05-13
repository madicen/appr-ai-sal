package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
)

// listRefreshFixtureModel returns a Model parked in modeList with a sized
// window and prsLoaded=true so the filter strip (and the refresh chip) is
// rendered (the spinner replaces the strip while loading).
func listRefreshFixtureModel(t *testing.T) *Model {
	t.Helper()
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.prsLoaded = true
	return m
}

// TestRefreshChipRendersAndIsClickable scans a freshly rendered list view for
// the ZoneRefreshList marker, then synthesises a left-click at the chip's
// centre and asserts the model flipped back into the loading state and
// returned a fetch command. This is the single load-bearing assertion that
// catches regressions where the chip is added to zones.go but never marked
// in the rendered view, or where the click handler is removed.
func TestRefreshChipRendersAndIsClickable(t *testing.T) {
	m := listRefreshFixtureModel(t)
	_ = m.View()
	waitBubbleZone(t, ZoneRefreshList)

	msg := clickCenterOfZone(t, ZoneRefreshList)
	out, cmd := m.handleMouse(msg)
	m2 := out.(*Model)

	if m2.prsLoaded {
		t.Fatalf("refresh click should set prsLoaded=false (so spinner replaces list)")
	}
	if cmd == nil {
		t.Fatalf("refresh click should return a non-nil load cmd")
	}
}

// TestRefreshChipShowsLoadingLabelWhileFetching guards the visual feedback
// that distinguishes the steady-state chip from an in-flight refresh — when
// prsLoaded is false the chip label flips to "refreshing…" so a user who
// clicked it (or pressed R) sees confirmation that something is happening.
// In practice the spinner overlay also shows during this window but the
// chip itself stays consistent with the rest of the filter strip when sized
// for measurement (renderFilterLine is also called by listBodyOriginY).
func TestRefreshChipShowsLoadingLabelWhileFetching(t *testing.T) {
	steady := renderFilterLine(false, false)
	if !strings.Contains(steady, "refresh (click or R)") {
		t.Fatalf("steady-state filter line missing refresh chip:\n%s", steady)
	}
	if strings.Contains(steady, "refreshing") {
		t.Fatalf("steady-state filter line should not say 'refreshing':\n%s", steady)
	}
	loading := renderFilterLine(false, true)
	if !strings.Contains(loading, "refreshing") {
		t.Fatalf("loading filter line should show 'refreshing…':\n%s", loading)
	}
}
