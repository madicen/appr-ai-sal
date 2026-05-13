package settings

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
	bubblepicker "github.com/madicen/bubble-color-picker"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/theme"
)

func init() { zone.NewGlobal() }

// TestThemeTabRendersAllSlots checks that opening the Theme tab produces a
// view containing every configurable colour label, the three group
// headers, and the action buttons. This protects against accidental
// renames/removals from theme.Slots() that would silently disappear from
// the UI.
func TestThemeTabRendersAllSlots(t *testing.T) {
	cfg := aiconfig.DefaultConfig()
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 40, StartSection: StartTheme})
	if m.panelTab != 2 {
		t.Fatalf("StartTheme should select panel tab 2; got %d", m.panelTab)
	}
	view := m.View()

	for _, s := range theme.Slots() {
		if !strings.Contains(view, s.Label) {
			t.Errorf("Theme tab missing label for slot %s (%q)", s.Key, s.Label)
		}
	}
	for _, header := range []string{"Specialists", "Context injection", "Severities"} {
		if !strings.Contains(view, header) {
			t.Errorf("Theme tab missing group header %q", header)
		}
	}
	for _, btn := range []string{"Save", "Cancel", "Reset to defaults"} {
		if !strings.Contains(view, btn) {
			t.Errorf("Theme tab missing button %q", btn)
		}
	}
}

// TestThemeTabKeyboardNavMovesFocus checks that tab/shift+tab cycle the
// focused swatch and that Enter opens it.
func TestThemeTabKeyboardNavMovesFocus(t *testing.T) {
	cfg := aiconfig.DefaultConfig()
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 40, StartSection: StartTheme})
	_ = m.View() // populate swatch geometry

	if m.theme == nil {
		t.Fatal("theme panel not initialised on StartTheme")
	}
	if got, want := m.theme.focus, 0; got != want {
		t.Fatalf("initial focus: got %d want %d", got, want)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(*Model)
	if got, want := m.theme.focus, 1; got != want {
		t.Errorf("after tab: focus got %d want %d", got, want)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(*Model)
	if got, want := m.theme.focus, 0; got != want {
		t.Errorf("after shift+tab: focus got %d want %d", got, want)
	}

	// Enter should ask the swatch to open. We can't easily assert the modal
	// itself appears without a renderer, but openSwatchIndex flips to the
	// focused swatch when the swatch's internal state opens.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if idx := m.theme.openSwatchIndex(); idx != 0 {
		t.Errorf("after enter, openSwatchIndex got %d want 0", idx)
	}
}

// TestThemeTabAppliesChosenColor exercises the bubblepicker.ColorChangedMsg
// path: the focused swatch should record the new colour in the draft, and
// the live theme should not be touched until the user saves.
func TestThemeTabAppliesChosenColor(t *testing.T) {
	original := theme.Current()
	defer theme.Apply(original)

	cfg := aiconfig.DefaultConfig()
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 40, StartSection: StartTheme})
	_ = m.View()

	// Open the formatting swatch (index 0).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if m.theme.openSwatchIndex() != 0 {
		t.Fatalf("expected swatch 0 to be open after Enter")
	}

	updated, _ = m.Update(bubblepicker.ColorChangedMsg{Color: "#abcdef", Dismiss: true})
	m = updated.(*Model)

	if got := m.theme.draft.Color(theme.KeyTagFormatting); got != "#abcdef" {
		t.Errorf("draft theme not updated; got %q want %q", got, "#abcdef")
	}
	// Live theme should remain at its previous (default) value.
	if got, want := theme.Color(theme.KeyTagFormatting), original.Color(theme.KeyTagFormatting); got != want {
		t.Errorf("live theme should not change before Save; got %q want %q", got, want)
	}
}

// TestThemeTabResetClearsDraft proves the `r` hotkey reverts the draft to
// the built-in defaults regardless of any in-progress edits.
func TestThemeTabResetClearsDraft(t *testing.T) {
	cfg := aiconfig.DefaultConfig()
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 40, StartSection: StartTheme})
	_ = m.View()

	m.theme.draft.Set(theme.KeyTagFormatting, "#000001")
	m.theme.draft.Set(theme.KeyTagDesign, "#000002")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(*Model)

	for _, k := range []theme.Key{theme.KeyTagFormatting, theme.KeyTagDesign} {
		if got, want := m.theme.draft.Color(k), theme.DefaultColor(k); got != want {
			t.Errorf("after reset, %s draft should equal default; got %q want %q", k, got, want)
		}
	}
}
