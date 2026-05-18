package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// panelFixtureModel returns a Model parked in modeList with a sized
// window, prsLoaded=true, and three canned PRs spanning authors and
// repos so the search-filter assertions have something to chew on.
func panelFixtureModel(t *testing.T) *Model {
	t.Helper()
	zone.NewGlobal()
	m := New(Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(data.PRListMsg{PRs: []gh.PR{
		{Number: 1, Title: "Add caching layer", Repository: "acme/api", Owner: "acme", Repo: "api", Author: "alice"},
		{Number: 2, Title: "Refactor auth", Repository: "acme/web", Owner: "acme", Repo: "web", Author: "bob"},
		{Number: 3, Title: "Bump deps", Repository: "acme/api", Owner: "acme", Repo: "api", Author: "alice"},
	}})
	return m
}

// TestFilterCycleAdvancesThroughAllModes confirms `f` rotates the
// filter through teams → explicit → authored → teams, and that the
// title and listMode track the chip every step.
func TestFilterCycleAdvancesThroughAllModes(t *testing.T) {
	m := panelFixtureModel(t)
	if m.filter != filterReviewTeams {
		t.Fatalf("initial filter want filterReviewTeams; got %v", m.filter)
	}
	if got := m.listMode(); got != gh.ListModeReviewTeams {
		t.Fatalf("initial listMode want ReviewTeams; got %v", got)
	}

	steps := []struct {
		want filterMode
		mode gh.ListMode
	}{
		{filterReviewExplicit, gh.ListModeReviewExplicit},
		{filterAuthored, gh.ListModeAuthored},
		{filterReviewTeams, gh.ListModeReviewTeams},
	}
	for i, s := range steps {
		out, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
		m = out.(*Model)
		if m.filter != s.want {
			t.Fatalf("step %d: filter want %v, got %v", i, s.want, m.filter)
		}
		if got := m.listMode(); got != s.mode {
			t.Fatalf("step %d: listMode want %v, got %v", i, s.mode, got)
		}
	}
}

// TestPanelRendersAllFilterChips confirms the panel emits each filter
// chip with its corresponding zone marker. Catches regressions where a
// chip is added to the descriptor table but the render loop skips it.
func TestPanelRendersAllFilterChips(t *testing.T) {
	m := panelFixtureModel(t)
	_ = m.View()
	for _, c := range filterChips {
		waitBubbleZone(t, c.zone)
		if z := zone.Get(c.zone); z == nil {
			t.Fatalf("filter chip zone %q not registered", c.zone)
		}
	}
	waitBubbleZone(t, zones.SearchField)
	waitBubbleZone(t, zones.URLField)
}

// TestChipClickJumpsToFilter clicks the "my PRs" chip and asserts the
// filter switched directly to filterAuthored (no cycle), and that a
// click on the already-active chip is a no-op (no refresh cmd).
func TestChipClickJumpsToFilter(t *testing.T) {
	m := panelFixtureModel(t)
	_ = m.View()
	waitBubbleZone(t, zones.FilterAuthored)

	out, cmd := m.handleMouse(clickCenterOfZone(t, zones.FilterAuthored))
	m = out.(*Model)
	if m.filter != filterAuthored {
		t.Fatalf("click on authored chip: filter want filterAuthored; got %v", m.filter)
	}
	if cmd == nil {
		t.Fatalf("click on inactive chip should kick a refresh cmd")
	}
	if !strings.Contains(m.list.Title, "authored by you") {
		t.Fatalf("authored filter title not updated; got %q", m.list.Title)
	}

	// Repeat click on the now-active chip — should not refresh.
	_ = m.View()
	out2, cmd2 := m.handleMouse(clickCenterOfZone(t, zones.FilterAuthored))
	m = out2.(*Model)
	if cmd2 != nil {
		t.Fatalf("click on active chip should be a no-op; got cmd")
	}
	if m.filter != filterAuthored {
		t.Fatalf("filter shouldn't change on repeat click; got %v", m.filter)
	}
}

// TestSearchFiltersListItems pushes a query that should match exactly
// one PR title and confirms the bubbles list now holds just that one
// item — i.e. applySearchFilter ran on every keystroke.
func TestSearchFiltersListItems(t *testing.T) {
	m := panelFixtureModel(t)
	if got := len(m.list.Items()); got != 3 {
		t.Fatalf("preload items want 3; got %d", got)
	}

	// Focus the search input via "/".
	out, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = out.(*Model)
	if m.listFocus != focusSearch {
		t.Fatalf("after `/`: listFocus want focusSearch; got %v", m.listFocus)
	}

	// Type "auth" — matches only PR #2.
	for _, r := range "auth" {
		out, _ = m.handleListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(*Model)
	}
	if got := len(m.list.Items()); got != 1 {
		t.Fatalf("after typing 'auth': items want 1; got %d (query=%q)", got, m.searchQuery)
	}

	// Author search.
	m.searchInput.SetValue("alice")
	m.searchQuery = "alice"
	m.applySearchFilter()
	if got := len(m.list.Items()); got != 2 {
		t.Fatalf("author filter alice: want 2, got %d", got)
	}

	// Repo search.
	m.searchInput.SetValue("web")
	m.searchQuery = "web"
	m.applySearchFilter()
	if got := len(m.list.Items()); got != 1 {
		t.Fatalf("repo filter web: want 1, got %d", got)
	}

	// PR number search.
	m.searchInput.SetValue("#3")
	m.searchQuery = "#3"
	m.applySearchFilter()
	if got := len(m.list.Items()); got != 1 {
		t.Fatalf("number filter #3: want 1, got %d", got)
	}
}

// TestSlashFocusesSearchAndEscRestores covers the focus state machine:
// `/` puts focus on the search input, esc with text clears it, second
// esc (now empty) returns focus to the list.
func TestSlashFocusesSearchAndEscRestores(t *testing.T) {
	m := panelFixtureModel(t)

	out, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = out.(*Model)
	if m.listFocus != focusSearch {
		t.Fatalf("/ should focus search; got %v", m.listFocus)
	}
	if !m.searchInput.Focused() {
		t.Fatalf("/ should focus searchInput")
	}

	// Type one char so esc has something to clear.
	out, _ = m.handleListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = out.(*Model)
	if m.searchInput.Value() != "a" {
		t.Fatalf("typed char not in input; got %q", m.searchInput.Value())
	}

	// First esc clears the value, keeps focus.
	out, _ = m.handleListKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(*Model)
	if m.searchInput.Value() != "" {
		t.Fatalf("esc should clear; got %q", m.searchInput.Value())
	}
	if m.listFocus != focusSearch {
		t.Fatalf("esc with text should keep focus on search; got %v", m.listFocus)
	}

	// Second esc (now empty) blurs.
	out, _ = m.handleListKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(*Model)
	if m.listFocus != focusList {
		t.Fatalf("esc on empty search should return focus to list; got %v", m.listFocus)
	}
}

// TestUKeyFocusesURLInput swaps over to the URL field with `u`.
func TestUKeyFocusesURLInput(t *testing.T) {
	m := panelFixtureModel(t)
	out, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = out.(*Model)
	if m.listFocus != focusURL {
		t.Fatalf("u should focus URL; got %v", m.listFocus)
	}
	if !m.urlInput.Focused() {
		t.Fatalf("u should focus urlInput")
	}
}

// TestTabCyclesFocusForwardAndBackward asserts the tab keybinding
// rotates the inline focus through list → search → url → list.
func TestTabCyclesFocusForwardAndBackward(t *testing.T) {
	m := panelFixtureModel(t)
	if m.listFocus != focusList {
		t.Fatalf("initial focus want focusList; got %v", m.listFocus)
	}
	steps := []listFocus{focusSearch, focusURL, focusList}
	for i, want := range steps {
		out, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyTab})
		m = out.(*Model)
		if m.listFocus != want {
			t.Fatalf("tab step %d: want %v, got %v", i, want, m.listFocus)
		}
	}
	// Shift-tab from focusList goes to URL first.
	out, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = out.(*Model)
	if m.listFocus != focusURL {
		t.Fatalf("shift+tab from list want focusURL; got %v", m.listFocus)
	}
}

// TestURLSubmitParsesAndDispatchesDetailLoad confirms enter on a valid
// URL clears the field, blurs the input, and returns a non-nil cmd
// (the detail loader). A blank input is a no-op.
func TestURLSubmitParsesAndDispatchesDetailLoad(t *testing.T) {
	m := panelFixtureModel(t)
	m.opts.Demo = true

	out, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = out.(*Model)
	m.urlInput.SetValue("acme/api#1")

	out, cmd := m.handleListKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(*Model)
	if cmd == nil {
		t.Fatalf("enter on valid URL should return a load cmd")
	}
	if m.urlInput.Value() != "" {
		t.Fatalf("enter should reset URL input; got %q", m.urlInput.Value())
	}
	if m.listFocus != focusList {
		t.Fatalf("enter should blur the URL input; got listFocus=%v", m.listFocus)
	}
}
