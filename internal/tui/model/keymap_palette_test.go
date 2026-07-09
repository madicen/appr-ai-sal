package model

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/tui/commands"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
)

// mk builds the tea.KeyMsg bubbletea reports for a key string.
func mk(s string) tea.KeyMsg {
	switch s {
	case " ", "space":
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
	case "?":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+g":
		return tea.KeyMsg{Type: tea.KeyCtrlG}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "ctrl+@":
		return tea.KeyMsg{Type: tea.KeyCtrlAt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestOldBindingsStillResolve is the migration-preservation guard: every
// raw-string key the pre-migration list/detail handlers switched on must
// still resolve to a binding in the central keymap. If a migration drops
// or fat-fingers a key, key.Matches returns false here and the test fails.
func TestOldBindingsStillResolve(t *testing.T) {
	km := keys.Default()

	// (key, binding) pairs enumerating the pre-migration raw cases.
	type pair struct {
		key string
		bnd key.Binding
	}
	cases := []pair{
		// Global.
		{"?", km.Help}, {"ctrl+k", km.Palette}, {"ctrl+c", km.Quit},
		// List screen.
		{"q", km.ListQuit}, {"f", km.ListFilter}, {"/", km.ListSearch},
		{"u", km.ListURL}, {"tab", km.ListCycleFocus}, {"shift+tab", km.ListCycleFocusB},
		{"R", km.ListRefresh}, {"enter", km.ListOpen},
		// Detail screen.
		{"esc", km.DetailBack}, {"q", km.DetailBack},
		{"tab", km.DetailCyclePane}, {"shift+tab", km.DetailCyclePaneB},
		{"g", km.DetailDescription}, {"d", km.DetailDiffOnly},
		{"r", km.DetailReview}, {"c", km.DetailToggleControls},
		{"a", km.DetailReopenApproval}, {"P", km.DetailBulk},
		{"j", km.DetailNavDown}, {"down", km.DetailNavDown},
		{"k", km.DetailNavUp}, {"up", km.DetailNavUp},
		{"space", km.DetailFold}, {"enter", km.DetailEnter},
		{"ctrl+d", km.DetailHalfDown}, {"ctrl+u", km.DetailHalfUp},
		{"ctrl+t", km.DetailTech},
		// Shared navigation.
		{"o", km.SettingsAI}, {",", km.SettingsReview}, {"ctrl+@", km.SettingsReview},
		{"ctrl+g", km.RepoCtx}, {"ctrl+r", km.RepoAgents}, {"ctrl+l", km.LangAgents},
		{"ctrl+b", km.BuildAgents}, {"O", km.Browser},
		// Review overlay (B1 x resurface, B4 c challenge must survive).
		{"y", km.ReviewPost}, {"n", km.ReviewSkip}, {"s", km.ReviewSkip},
		{"x", km.ReviewResurface}, {"c", km.ReviewChallenge},
		{"right", km.ReviewNext}, {"l", km.ReviewNext},
		{"left", km.ReviewPrev}, {"h", km.ReviewPrev},
		{"F", km.ReviewFileLevel},
	}
	for _, c := range cases {
		if !key.Matches(mk(c.key), c.bnd) {
			t.Errorf("pre-migration key %q no longer resolves to its binding %q",
				c.key, c.bnd.Help().Key)
		}
	}
}

// TestHelpOverlayToggle: `?` on the list screen (list-focused) opens the
// full-screen help overlay; `?` (or esc) again dismisses it.
func TestHelpOverlayToggle(t *testing.T) {
	m := New(Options{})
	m.width, m.height = 120, 44

	out, _ := m.Update(mk("?"))
	m = out.(*Model)
	top := m.overlayStack.Top()
	if _, ok := top.(overlays.HelpOverlay); !ok {
		t.Fatalf("? did not open the help overlay; top = %T", top)
	}

	// The overlay owns input now; a `?` routed through the model reaches
	// it and it emits DismissMsg, which the root pops.
	out, cmd := m.Update(mk("?"))
	m = out.(*Model)
	if cmd != nil {
		// The overlay's dismiss cmd is a func returning DismissMsg; run it
		// and feed it back so the root actually pops.
		out, _ = m.Update(cmd())
		m = out.(*Model)
	}
	if top := m.overlayStack.Top(); top != nil {
		if _, ok := top.(overlays.HelpOverlay); ok {
			t.Fatalf("help overlay was not dismissed by ?")
		}
	}
}

// TestCommandPaletteOpensOnCtrlK: ctrl+k on a root-native screen pushes the
// modal palette.
func TestCommandPaletteOpensOnCtrlK(t *testing.T) {
	m := New(Options{})
	m.width, m.height = 120, 44

	out, _ := m.Update(mk("ctrl+k"))
	m = out.(*Model)
	if _, ok := m.overlayStack.Top().(*overlays.PaletteOverlay); !ok {
		t.Fatalf("ctrl+k did not open the palette; top = %T", m.overlayStack.Top())
	}
}

// TestGlobalKeysSuppressedWhileTyping: with the inline search field focused,
// `?` must type into the field rather than open help (globalKeysActive gate).
func TestGlobalKeysSuppressedWhileTyping(t *testing.T) {
	m := New(Options{})
	m.width, m.height = 120, 44
	m.focusSearchInput() // listFocus = focusSearch

	out, _ := m.Update(mk("?"))
	m = out.(*Model)
	if _, ok := m.overlayStack.Top().(overlays.HelpOverlay); ok {
		t.Fatalf("? opened help while the search field was focused")
	}
}

// TestPaletteEnableGating: the registry only offers commands whose context
// predicate accepts the current screen.
func TestPaletteEnableGating(t *testing.T) {
	m := New(Options{})
	reg := m.palette

	listIDs := idSet(reg.Enabled(commands.Context{Mode: "list", HasSelection: true}))
	if !listIDs["list.refresh"] || !listIDs["list.filter"] {
		t.Errorf("list context missing list commands: %v", listIDs)
	}
	if listIDs["detail.review"] {
		t.Errorf("list context should not enable detail.review")
	}

	detailIDs := idSet(reg.Enabled(commands.Context{Mode: "detail", HasPR: true}))
	if !detailIDs["detail.review"] {
		t.Errorf("detail context (with PR) should enable detail.review: %v", detailIDs)
	}
	if detailIDs["list.refresh"] {
		t.Errorf("detail context should not enable list.refresh")
	}

	// A draft-gated command is hidden without a draft, shown with one.
	noDraft := idSet(reg.Enabled(commands.Context{Mode: "detail", HasPR: true}))
	if noDraft["detail.bulk-post"] {
		t.Errorf("detail.bulk-post should be gated off without a draft")
	}
	withDraft := idSet(reg.Enabled(commands.Context{Mode: "detail", HasPR: true, HasDraft: true}))
	if !withDraft["detail.bulk-post"] {
		t.Errorf("detail.bulk-post should be enabled with a draft")
	}
}

// TestPaletteRunsSameActionAsKey: running the "list.filter" command performs
// the same state change as pressing its key (f) — both advance the filter.
func TestPaletteRunsSameActionAsKey(t *testing.T) {
	// Via the key.
	byKey := New(Options{})
	byKey.width, byKey.height = 120, 44
	startFilter := byKey.filter
	out, _ := byKey.Update(mk("f"))
	byKey = out.(*Model)
	wantFilter := byKey.filter
	if wantFilter == startFilter {
		t.Fatalf("pressing f did not change the filter")
	}

	// Via the palette command's wired Run.
	byCmd := New(Options{})
	byCmd.width, byCmd.height = 120, 44
	cmd, ok := byCmd.palette.Find("list.filter")
	if !ok {
		t.Fatalf("registry missing list.filter command")
	}
	_ = cmd.Run() // returns the refresh cmd; the filter mutation is the assertion
	if byCmd.filter != wantFilter {
		t.Errorf("palette list.filter left filter=%v, key press produced %v", byCmd.filter, wantFilter)
	}
}

// TestStatusBarDerivedFromKeymap: the list status bar segments are produced
// from the keymap's help text, so each hint matches keys.SegText(binding).
func TestStatusBarDerivedFromKeymap(t *testing.T) {
	m := New(Options{})
	m.width, m.height = 160, 44

	texts := map[string]bool{}
	for _, s := range m.statusSegs() {
		texts[s.text] = true
	}
	km := m.keys
	for _, want := range []key.Binding{km.ListFilter, km.ListSearch, km.ListRefresh, km.Palette, km.Help} {
		if !texts[keys.SegText(want)] {
			t.Errorf("status bar missing keymap-derived segment %q", keys.SegText(want))
		}
	}
}

func idSet(cmds []commands.Command) map[string]bool {
	out := map[string]bool{}
	for _, c := range cmds {
		out[c.ID] = true
	}
	return out
}
