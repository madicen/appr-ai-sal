package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
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

// Clicking the "Parallel specialists" toggle in the Run options list must
// flip the on-disk repoconfig knob in place — not navigate the user to the
// Settings tab. The earlier "open settings" routing was confusing because
// it broke the muscle memory the sibling toggles ("Dry run", "Start review
// minimized") already established (click → flip → done).
func TestControlsClickToggleParallelFlipsRepoConfigInline(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", t.TempDir())
	// Defensively neutralize any APPR_AI_SAL_PARALLEL_SPECIALISTS the
	// dev shell may have set: it would otherwise win at read time and
	// the next test run could observe a different on-disk start value.
	t.Setenv("APPR_AI_SAL_PARALLEL_SPECIALISTS", "")

	m := detailFixtureModel(t)
	if m.controlsHidden {
		t.Fatalf("controls pane unexpectedly hidden at fixture width")
	}
	_ = m.View()

	startCfg, err := repoconfig.Load()
	if err != nil {
		t.Fatalf("repoconfig.Load: %v", err)
	}
	startVal := startCfg.ParallelSpecialists

	msg := clickCenterOfZone(t, zones.ControlsToggleParallel)
	out, _ := m.detailHandleMouse(msg, false)
	m2 := out.(*Model)

	if m2.mode != modeDetail {
		t.Fatalf("toggle click must keep us in detail mode (was %v); regression: navigated to settings", m2.mode)
	}
	got, err := repoconfig.Load()
	if err != nil {
		t.Fatalf("repoconfig.Load after toggle: %v", err)
	}
	if got.ParallelSpecialists == startVal {
		t.Fatalf("ParallelSpecialists did not flip on disk: got %v, want %v", got.ParallelSpecialists, !startVal)
	}

	// Click again: must flip back. This pins the round-trip behavior so
	// future refactors of the toggle helper can't silently degrade to
	// "set, never unset".
	msg = clickCenterOfZone(t, zones.ControlsToggleParallel)
	out, _ = m2.detailHandleMouse(msg, false)
	m3 := out.(*Model)
	got2, err := repoconfig.Load()
	if err != nil {
		t.Fatalf("repoconfig.Load after second toggle: %v", err)
	}
	if got2.ParallelSpecialists != startVal {
		t.Fatalf("second toggle did not return to start: got %v, want %v", got2.ParallelSpecialists, startVal)
	}
	if m3.mode != modeDetail {
		t.Fatalf("second toggle click must keep us in detail mode (was %v)", m3.mode)
	}
}

// Clicking the "Parallel PR agents" toggle flips its repoconfig knob in
// place, mirroring the Parallel specialists toggle (click → flip → stay in
// detail), and never navigates to Settings.
func TestControlsClickToggleParallelPRAgentsFlipsRepoConfigInline(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", t.TempDir())
	t.Setenv("APPR_AI_SAL_PARALLEL_PR_AGENTS", "")

	m := detailFixtureModel(t)
	if m.controlsHidden {
		t.Fatalf("controls pane unexpectedly hidden at fixture width")
	}
	_ = m.View()

	startCfg, err := repoconfig.Load()
	if err != nil {
		t.Fatalf("repoconfig.Load: %v", err)
	}
	startVal := startCfg.ParallelPRAgents

	msg := clickCenterOfZone(t, zones.ControlsToggleParallelPRAgents)
	out, _ := m.detailHandleMouse(msg, false)
	m2 := out.(*Model)
	if m2.mode != modeDetail {
		t.Fatalf("toggle click must keep us in detail mode (was %v); regression: navigated to settings", m2.mode)
	}
	got, err := repoconfig.Load()
	if err != nil {
		t.Fatalf("repoconfig.Load after toggle: %v", err)
	}
	if got.ParallelPRAgents == startVal {
		t.Fatalf("ParallelPRAgents did not flip on disk: got %v, want %v", got.ParallelPRAgents, !startVal)
	}

	msg = clickCenterOfZone(t, zones.ControlsToggleParallelPRAgents)
	out, _ = m2.detailHandleMouse(msg, false)
	m3 := out.(*Model)
	got2, err := repoconfig.Load()
	if err != nil {
		t.Fatalf("repoconfig.Load after second toggle: %v", err)
	}
	if got2.ParallelPRAgents != startVal {
		t.Fatalf("second toggle did not return to start: got %v, want %v", got2.ParallelPRAgents, startVal)
	}
	if m3.mode != modeDetail {
		t.Fatalf("second toggle click must keep us in detail mode (was %v)", m3.mode)
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

// TestControlsClickRepoAgentsRowNavigatesWithoutRegen is the regression
// for two intertwined bugs the user hit:
//
//  1. Clicking the "Repo agents" row in the controls pane used to call
//     openRepoAgentsForCurrentPR(true), which fires Regenerate-all on
//     the focused repo as soon as the tab opens. Click is a navigation
//     gesture; LLM regen should be an explicit action (ctrl+b or the
//     in-tab Regenerate button), not a side effect of "let me look at
//     what's there".
//
//  2. The tab landed on the alphabetically first repo instead of the
//     current PR's repo, because the async reposLoadedMsg merge
//     re-sorted the list without re-applying FocusRepo. (Lib-level
//     regression test for that is in tabs/repoagents/focus_test.go;
//     this app-level test pins the click's contract end-to-end.)
//
// Together: click must (a) enter repo-agents mode, (b) focus the PR's
// repo, (c) leave the status line empty (no "regenerating …" banner).
func TestControlsClickRepoAgentsRowNavigatesWithoutRegen(t *testing.T) {
	m := detailFixtureModel(t)
	// detailFixtureModel sets Repository but not Owner/Repo —
	// openRepoAgentsForCurrentPR reads the split fields, so populate
	// them explicitly so the click has a real focus key to land on.
	m.currentPR.Owner = "o"
	m.currentPR.Repo = "r"
	_ = m.View()
	msg := clickCenterOfZone(t, zones.ControlsRepoAgents)
	out, cmd := m.detailHandleMouse(msg, false)
	m2 := out.(*Model)
	if cmd == nil {
		t.Fatal("click on Repo agents row should produce a non-nil cmd (Init of the tab)")
	}
	if m2.mode != modeRepoAgents {
		t.Fatalf("click should enter repo-agents mode, got %v", m2.mode)
	}
	if m2.repoAgents == nil {
		t.Fatal("repoAgents tab should be constructed after the click")
	}
	if got := m2.repoAgents.CurrentRepoKey(); got != "o/r" {
		t.Fatalf("tab should focus the current PR's repo o/r, got %q", got)
	}
	if status := m2.repoAgents.Status(); strings.Contains(strings.ToLower(status), "regenerat") {
		t.Fatalf("click must NOT trigger regeneration; status=%q", status)
	}
}

// TestControlsClickTechExpertsRowNavigatesWithoutRegen mirrors the repo
// agents test for the tech experts row — same underlying handler, same
// regression class.
func TestControlsClickTechExpertsRowNavigatesWithoutRegen(t *testing.T) {
	m := detailFixtureModel(t)
	m.currentPR.Owner = "o"
	m.currentPR.Repo = "r"
	_ = m.View()
	msg := clickCenterOfZone(t, zones.ControlsTechAgents)
	out, _ := m.detailHandleMouse(msg, false)
	m2 := out.(*Model)
	if m2.mode != modeRepoAgents {
		t.Fatalf("click on Tech experts row should enter repo-agents mode, got %v", m2.mode)
	}
	if m2.repoAgents == nil {
		t.Fatal("repoAgents tab should be constructed after the click")
	}
	if got := m2.repoAgents.CurrentRepoKey(); got != "o/r" {
		t.Fatalf("tab should focus the current PR's repo o/r, got %q", got)
	}
	if status := m2.repoAgents.Status(); strings.Contains(strings.ToLower(status), "regenerat") {
		t.Fatalf("click must NOT trigger regeneration; status=%q", status)
	}
}

// TestRepoAgentRowHintAdvertisesNavigateShortcut pins the row's
// keyboard hint to the navigate shortcut (ctrl+r), not the build one
// (ctrl+b). The row's primary action is "open the tab"; advertising
// ctrl+b reinforced the wrong mental model and led users to expect
// (and tolerate) auto-regen on every visit.
func TestRepoAgentRowHintAdvertisesNavigateShortcut(t *testing.T) {
	row := ansi.Strip(repoAgentRow(repoagentsstore.FreshnessUnknown))
	if !strings.Contains(row, "ctrl+r") {
		t.Fatalf("repo agents row should advertise ctrl+r (navigate); got %q", row)
	}
	if strings.Contains(row, "ctrl+b") {
		t.Fatalf("repo agents row should NOT advertise ctrl+b (build) as the primary action; got %q", row)
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
