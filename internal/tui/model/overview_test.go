package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// TestPRItemDescriptionRendersDiffStatsAndChecksChips guards the new chips
// the queue rows learned to carry. We assert presence (additions, deletions,
// file count, rollup state token) rather than exact ANSI so style tweaks
// don't break the test.
func TestPRItemDescriptionRendersDiffStatsAndChecksChips(t *testing.T) {
	pr := gh.PR{
		Repository:   "owner/repo",
		Author:       "alice",
		Additions:    142,
		Deletions:    37,
		ChangedFiles: 6,
		ChecksState:  "FAILURE",
	}
	desc := prItem{pr: pr}.Description()
	wants := []string{"+142", "-37", "6 files", "checks fail"}
	for _, w := range wants {
		if !strings.Contains(desc, w) {
			t.Fatalf("description missing %q; got: %s", w, desc)
		}
	}
}

// TestPRItemDescriptionDropsZeroChips covers the "older self-hosted GitHub
// installs return 0 additions" case — when every counter is zero we don't
// want to advertise a phantom "+0/-0 · 0 files" chip on the row.
func TestPRItemDescriptionDropsZeroChips(t *testing.T) {
	pr := gh.PR{Repository: "o/r", Author: "a"}
	desc := prItem{pr: pr}.Description()
	if strings.Contains(desc, "+0") || strings.Contains(desc, "-0") {
		t.Fatalf("zero-stats row should not include +0/-0; got: %s", desc)
	}
}

// TestChecksRollupChipMapping covers each rollup state → chip string.
func TestChecksRollupChipMapping(t *testing.T) {
	cases := []struct {
		state string
		want  string
	}{
		{"SUCCESS", "checks pass"},
		{"FAILURE", "checks fail"},
		{"ERROR", "checks error"},
		{"PENDING", "checks pending"},
		{"", ""},
	}
	for _, tc := range cases {
		got := checksRollupChip(tc.state)
		if tc.want == "" {
			if got != "" {
				t.Fatalf("state=%q expected empty chip, got %q", tc.state, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Fatalf("state=%q expected to contain %q, got %q", tc.state, tc.want, got)
		}
	}
}

// TestLeftColumnIndexRoundtrip exercises the leftColumnIndexFor /
// applyLeftColumnIndex pair so navigation across the overview/tree
// boundary stays consistent.
func TestLeftColumnIndexRoundtrip(t *testing.T) {
	cases := []struct {
		v    centerView
		tIdx int
		want int
	}{
		{centerDescription, 0, 0},
		{centerChecks, 0, 1},
		{centerDiscussion, 0, 2},
		{centerDiff, 0, 3},
		{centerDiff, 5, 8},
	}
	for _, tc := range cases {
		got := leftColumnIndexFor(tc.v, tc.tIdx)
		if got != tc.want {
			t.Fatalf("leftColumnIndexFor(%v,%d) = %d, want %d", tc.v, tc.tIdx, got, tc.want)
		}
	}
}

// TestKeyboardJWalksOverviewIntoTree presses j repeatedly on a freshly
// opened detail and asserts that the cursor walks Description → Checks →
// Discussion → first tree row, with centerView snapping back to centerDiff
// once the cursor crosses into the tree.
func TestKeyboardJWalksOverviewIntoTree(t *testing.T) {
	m := detailFixtureModel(t)
	m.centerView = centerDescription

	// j → Checks
	out, _ := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = out.(*Model)
	if m.centerView != centerChecks {
		t.Fatalf("after first j: centerView=%v, want centerChecks", m.centerView)
	}
	// j → Discussion
	out, _ = m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = out.(*Model)
	if m.centerView != centerDiscussion {
		t.Fatalf("after second j: centerView=%v, want centerDiscussion", m.centerView)
	}
	// j → centerDiff at treeIdx 0 (first tree row)
	out, _ = m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = out.(*Model)
	if m.centerView != centerDiff {
		t.Fatalf("after crossing into tree: centerView=%v, want centerDiff", m.centerView)
	}
	if m.treeIdx != 0 {
		t.Fatalf("after crossing into tree: treeIdx=%d, want 0", m.treeIdx)
	}
}

// TestKeyboardKWalksFromTreeBackIntoOverview exercises the reverse direction:
// k from the first tree row pushes the cursor back into the overview rows.
func TestKeyboardKWalksFromTreeBackIntoOverview(t *testing.T) {
	m := detailFixtureModel(t)
	m.centerView = centerDiff
	m.treeIdx = 0

	out, _ := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = out.(*Model)
	if m.centerView != centerDiscussion {
		t.Fatalf("k from first tree row should land on centerDiscussion; got %v", m.centerView)
	}
}

// TestGShortcutJumpsToDescription guards the muscle-memory `g` keybinding:
// pressing it from any state flips centerView to centerDescription.
func TestGShortcutJumpsToDescription(t *testing.T) {
	m := detailFixtureModel(t)
	m.centerView = centerDiff

	out, _ := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = out.(*Model)
	if m.centerView != centerDescription {
		t.Fatalf("g should jump to centerDescription; got %v", m.centerView)
	}
	// Pressing g again toggles back to centerDiff (last-diff selection).
	out, _ = m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = out.(*Model)
	if m.centerView != centerDiff {
		t.Fatalf("g (again) should toggle back to centerDiff; got %v", m.centerView)
	}
}

// TestEnsureCenterDataLoadedFiresChecksOnce verifies the lazy-load gate:
// the first visit to centerChecks emits a tea.Cmd and flips checksLoading;
// subsequent visits while loading or after the report is cached are no-ops.
func TestEnsureCenterDataLoadedFiresChecksOnce(t *testing.T) {
	m := detailFixtureModel(t)
	m.centerView = centerChecks

	cmd := m.ensureCenterDataLoaded()
	if cmd == nil {
		t.Fatalf("first centerChecks visit should return a load cmd")
	}
	if !m.checksLoading {
		t.Fatalf("first centerChecks visit should set checksLoading=true")
	}
	// While loading, the second call must not re-fire.
	if again := m.ensureCenterDataLoaded(); again != nil {
		t.Fatalf("second visit while loading should be a no-op")
	}
	// Land the report; subsequent visits are also no-ops.
	m.checksLoading = false
	m.checks = &gh.ChecksReport{RollupState: "SUCCESS"}
	if cmd := m.ensureCenterDataLoaded(); cmd != nil {
		t.Fatalf("visit after report cached should be a no-op")
	}
}

// TestPRDetailMsgResetsOverviewCache loading a new PR clears any cached
// checks / discussion data so we don't bleed stale state into the new
// context.
func TestPRDetailMsgResetsOverviewCache(t *testing.T) {
	m := detailFixtureModel(t)
	m.checks = &gh.ChecksReport{RollupState: "FAILURE"}
	m.discussion = []gh.DiscussionEvent{{Author: "a"}}
	m.checksLoading = true
	m.discussionLoading = true

	pr := gh.PR{Owner: "o", Repo: "r2", Number: 99}
	out, _ := m.Update(data.PRDetailMsg{PR: &pr, Diff: ""})
	m = out.(*Model)
	if m.checks != nil || m.discussion != nil || m.checksLoading || m.discussionLoading {
		t.Fatalf("PRDetailMsg should reset overview caches; got %+v %+v", m.checks, m.discussion)
	}
	if m.centerView != centerDiff {
		t.Fatalf("PRDetailMsg should reset centerView to centerDiff; got %v", m.centerView)
	}
}

// TestOverviewClickFlipsCenterView is the mouse twin of the keyboard jump:
// a click on the OverviewChecks zone flips the centre pane to Checks.
func TestOverviewClickFlipsCenterView(t *testing.T) {
	m := detailFixtureModel(t)
	_ = m.View()
	waitBubbleZone(t, zones.OverviewChecks)
	msg := clickCenterOfZone(t, zones.OverviewChecks)
	out, _ := m.detailHandleMouse(msg, false)
	m = out.(*Model)
	if m.centerView != centerChecks {
		t.Fatalf("OverviewChecks click should flip centerView to centerChecks; got %v", m.centerView)
	}
}

// TestTreeRowClickRestoresDiffAfterOverviewView verifies the "click a file
// row to go back to the diff" flow: from centerDiscussion, a click on the
// first tree-file zone snaps centerView back to centerDiff and selects
// that file.
func TestTreeRowClickRestoresDiffAfterOverviewView(t *testing.T) {
	m := detailFixtureModel(t)
	m.centerView = centerDiscussion
	m.refreshDetailViews()
	_ = m.View()
	waitBubbleZone(t, zones.TreeFile(0))
	msg := clickCenterOfZone(t, zones.TreeFile(0))
	out, _ := m.detailHandleMouse(msg, false)
	m = out.(*Model)
	if m.centerView != centerDiff {
		t.Fatalf("clicking a tree row should restore centerDiff; got %v", m.centerView)
	}
	if m.selectedFilePath != m.treeRows[0].Path {
		t.Fatalf("clicking a tree row should select that file; got %q", m.selectedFilePath)
	}
}
