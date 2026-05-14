package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	techagentsstore "github.com/madicen/appr-ai-sal/internal/review/techagents"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// The right-hand "Review controls" pane is auto-shown at the wide
// detailFixtureModel size (160 cols). The Start Review button must be
// registered as a clickable zone.
func TestControlsPaneZonesRegistered(t *testing.T) {
	m := detailFixtureModel(t)
	out := m.View()
	if !strings.Contains(ansi.Strip(out), "Strictness") {
		t.Fatalf("controls pane not rendered (missing 'Strictness' header):\n%s", ansi.Strip(out))
	}
	for _, id := range []string{
		zones.PaneControls,
		zones.ControlsStartReview,
		zones.ControlsStrictBalanced,
	} {
		waitBubbleZone(t, id)
		if zone.Get(id) == nil {
			t.Fatalf("expected zone %q to be registered", id)
		}
	}
}

// Clicking the strictness "lenient" row updates m.opts.AIConfig.ReviewStrictness.
func TestControlsClickStrictnessUpdatesConfig(t *testing.T) {
	m := detailFixtureModel(t)
	m.opts.AIConfig = aiconfig.DefaultConfig()
	m.opts.AIConfig.ReviewStrictness = aiconfig.ReviewBalanced
	m.refreshDetailViews()
	_ = m.View()
	msg := clickCenterOfZone(t, zones.ControlsStrictLenient)
	out, _ := m.detailHandleMouse(msg, false)
	m2 := out.(*Model)
	if m2.opts.AIConfig.ReviewStrictness != aiconfig.ReviewLenient {
		t.Fatalf("strictness: got %s, want lenient", m2.opts.AIConfig.ReviewStrictness)
	}
}

// Clicking the profile "next" arrow cycles ActiveProfile.
func TestControlsClickProfileNextCycles(t *testing.T) {
	m := detailFixtureModel(t)
	m.opts.AIConfig = aiconfig.DefaultConfig()
	if err := m.opts.AIConfig.AddProfile(aiconfig.Profile{
		Name:     "fast",
		Provider: aiconfig.ProviderOllama,
		Model:    "phi3",
	}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	m.refreshDetailViews()
	_ = m.View()
	msg := clickCenterOfZone(t, zones.ControlsProfileNext)
	out, _ := m.detailHandleMouse(msg, false)
	m2 := out.(*Model)
	if m2.opts.AIConfig.ActiveProfile != "fast" {
		t.Fatalf("after click next: active profile got %q, want fast", m2.opts.AIConfig.ActiveProfile)
	}
}

// Clicking the "Start review" button kicks off startReviewOverlay (signaled
// by the model entering the review overlay path; for unit testing we just
// assert the click is consumed without a panic and a non-nil command runs).
func TestControlsClickStartReviewIssuesCommand(t *testing.T) {
	m := detailFixtureModel(t)
	_ = m.View()
	msg := clickCenterOfZone(t, zones.ControlsStartReview)
	out, cmd := m.detailHandleMouse(msg, false)
	if out == nil {
		t.Fatalf("expected updated model")
	}
	if cmd == nil {
		t.Fatalf("expected non-nil cmd from Start Review click")
	}
}

// The mini-header should NOT contain the build-repo-agents / lang-experts
// chips anymore; that state lives in the controls pane.
func TestMiniHeaderHasNoAgentChips(t *testing.T) {
	m := detailFixtureModel(t)
	header := ansi.Strip(m.renderDetailMiniHeader())
	if strings.Contains(header, "build repo agents") {
		t.Fatalf("mini-header still contains 'build repo agents': %q", header)
	}
	if strings.Contains(header, "build lang experts") {
		t.Fatalf("mini-header still contains 'build lang experts': %q", header)
	}
}

// Pressing 'c' toggles the controls pane visibility.
func TestKeyCToggleControlsPane(t *testing.T) {
	m := detailFixtureModel(t)
	if m.controlsHidden {
		t.Fatalf("controls pane unexpectedly hidden at fixture width (controlsUserHidden=%v)", m.controlsUserHidden)
	}
	out, _ := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m2 := out.(*Model)
	if !m2.controlsUserHidden {
		t.Fatalf("after pressing c, expected controlsUserHidden=true")
	}
	out, _ = m2.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m3 := out.(*Model)
	if m3.controlsUserHidden {
		t.Fatalf("after second c press, expected controlsUserHidden=false")
	}
}

// On a narrow terminal the controls pane auto-hides so the diff stays
// readable.
func TestControlsAutoHideOnNarrowTerminal(t *testing.T) {
	m := detailFixtureModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if !m.controlsHidden {
		t.Fatalf("controls pane should auto-hide at width=80; controlsUserHidden=%v controlsHidden=%v", m.controlsUserHidden, m.controlsHidden)
	}
}

// Tech experts default to "not configured" framing, not the alarmist
// "missing" used for repo/lang agents. Tech experts are an opt-in
// per-repo feature with no canonical expected set, so absence is the
// normal default state and should read as a hint, not an error.
func TestTechAgentRowMissingShowsKindFraming(t *testing.T) {
	row := ansi.Strip(techAgentRow(techagentsstore.FreshnessMissing))
	if !strings.Contains(row, "not configured") {
		t.Fatalf("expected 'not configured' for missing tech row; got %q", row)
	}
	if strings.Contains(row, "missing") {
		t.Fatalf("missing tech row should not say 'missing' (alarmist); got %q", row)
	}
	// Missing must NOT trigger the data-layer attention nag for tech.
	if techagentsstore.FreshnessMissing.NeedsAttention() {
		t.Fatalf("FreshnessMissing.NeedsAttention() should be false for tech (opt-in feature)")
	}
	if !techagentsstore.FreshnessStale.NeedsAttention() {
		t.Fatalf("FreshnessStale.NeedsAttention() must remain true (opted-in but decaying)")
	}
}
