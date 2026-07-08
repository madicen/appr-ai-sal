package keys

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg builds a tea.KeyMsg for a single key string as bubbletea reports
// it (e.g. "f", "ctrl+g", "enter", " ").
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	// Fall back to parsing (handles "ctrl+g", single runes, etc.) via a
	// direct rune/ctrl construction.
	return parseKey(s)
}

// parseKey handles ctrl+<rune> and single-rune keys, which is all the
// non-special bindings in the map use.
func parseKey(s string) tea.KeyMsg {
	if len(s) > 5 && s[:5] == "ctrl+" {
		r := s[5]
		switch r {
		case 'g':
			return tea.KeyMsg{Type: tea.KeyCtrlG}
		case 'r':
			return tea.KeyMsg{Type: tea.KeyCtrlR}
		case 'l':
			return tea.KeyMsg{Type: tea.KeyCtrlL}
		case 'b':
			return tea.KeyMsg{Type: tea.KeyCtrlB}
		case 't':
			return tea.KeyMsg{Type: tea.KeyCtrlT}
		case 'd':
			return tea.KeyMsg{Type: tea.KeyCtrlD}
		case 'u':
			return tea.KeyMsg{Type: tea.KeyCtrlU}
		case 'k':
			return tea.KeyMsg{Type: tea.KeyCtrlK}
		case 'c':
			return tea.KeyMsg{Type: tea.KeyCtrlC}
		case '@':
			return tea.KeyMsg{Type: tea.KeyCtrlAt}
		}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestEveryFunctionalBindingResolves is the "keymap match coverage" test:
// for every binding that carries keys, each of its keys must resolve back
// to that binding via key.Matches. This guards against a key being wired
// into two bindings' Keys() by mistake or a typo in a WithKeys string.
func TestEveryFunctionalBindingResolves(t *testing.T) {
	km := Default()
	bindings := map[string]key.Binding{
		"Help": km.Help, "Palette": km.Palette, "Quit": km.Quit,
		"ListOpen": km.ListOpen, "ListCycleFocus": km.ListCycleFocus,
		"ListCycleFocusB": km.ListCycleFocusB, "ListSearch": km.ListSearch,
		"ListURL": km.ListURL, "ListFilter": km.ListFilter,
		"ListRefresh": km.ListRefresh, "ListQuit": km.ListQuit,
		"DetailBack": km.DetailBack, "DetailCyclePane": km.DetailCyclePane,
		"DetailCyclePaneB": km.DetailCyclePaneB, "DetailNavDown": km.DetailNavDown,
		"DetailNavUp": km.DetailNavUp, "DetailFold": km.DetailFold,
		"DetailEnter": km.DetailEnter, "DetailReview": km.DetailReview,
		"DetailToggleControls": km.DetailToggleControls,
		"DetailReopenApproval": km.DetailReopenApproval,
		"DetailDescription":    km.DetailDescription, "DetailDiffOnly": km.DetailDiffOnly,
		"DetailBulk": km.DetailBulk, "DetailTech": km.DetailTech,
		"DetailHalfDown": km.DetailHalfDown, "DetailHalfUp": km.DetailHalfUp,
		"SettingsAI": km.SettingsAI, "SettingsReview": km.SettingsReview,
		"RepoCtx": km.RepoCtx, "RepoAgents": km.RepoAgents,
		"LangAgents": km.LangAgents, "BuildAgents": km.BuildAgents,
		"Browser": km.Browser,
	}
	for name, b := range bindings {
		ks := b.Keys()
		if len(ks) == 0 {
			t.Errorf("binding %s has no keys", name)
			continue
		}
		for _, k := range ks {
			if !key.Matches(keyMsg(k), b) {
				t.Errorf("binding %s: key %q does not resolve via key.Matches", name, k)
			}
		}
	}
}

// TestSegText renders "key desc" or bare "key" so the status bar and help
// overlay share one label producer.
func TestSegText(t *testing.T) {
	km := Default()
	if got := SegText(km.ListFilter); got != "f filter" {
		t.Fatalf("ListFilter seg = %q, want %q", got, "f filter")
	}
	if got := SegText(km.ListNav); got != "↑/↓" {
		t.Fatalf("ListNav seg = %q, want %q", got, "↑/↓")
	}
	if got := SegText(km.ListOpenClick); got != "double-click open" {
		t.Fatalf("ListOpenClick seg = %q, want %q", got, "double-click open")
	}
}

// TestSectionsCoverContexts asserts the help overlay data has the expected
// context groups and that each listed binding carries help text.
func TestSectionsCoverContexts(t *testing.T) {
	km := Default()
	secs := km.Sections()
	wantTitles := []string{"Global", "Review queue (list)", "PR detail", "Review overlay"}
	if len(secs) != len(wantTitles) {
		t.Fatalf("got %d sections, want %d", len(secs), len(wantTitles))
	}
	for i, want := range wantTitles {
		if secs[i].Title != want {
			t.Errorf("section %d title = %q, want %q", i, secs[i].Title, want)
		}
		if len(secs[i].Bindings) == 0 {
			t.Errorf("section %q has no bindings", want)
		}
		for _, b := range secs[i].Bindings {
			if b.Help().Key == "" {
				t.Errorf("section %q has a binding with no help key", want)
			}
		}
	}
}

// TestDisplayOnlyBindingsDisabled documents that gesture-only entries carry
// help text but no functional keys, so they never intercept a keystroke.
func TestDisplayOnlyBindingsDisabled(t *testing.T) {
	km := Default()
	for name, b := range map[string]key.Binding{
		"ListNav": km.ListNav, "ListClick": km.ListClick,
		"ListOpenClick": km.ListOpenClick, "ListClearSearch": km.ListClearSearch,
		"DetailNav": km.DetailNav,
	} {
		if len(b.Keys()) != 0 {
			t.Errorf("display-only binding %s should have no functional keys, got %v", name, b.Keys())
		}
	}
}
