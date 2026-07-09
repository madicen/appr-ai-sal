package overlays

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/tui/commands"
)

func paletteCmds() []commands.Command {
	return []commands.Command{
		{ID: "refresh", Title: "Refresh PR list", Category: "queue",
			Binding: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))},
		{ID: "review", Title: "Start AI review", Category: "detail"},
		{ID: "browser", Title: "Open PR in browser", Category: "nav"},
	}
}

func typeRunes(p *PaletteOverlay, s string) *PaletteOverlay {
	for _, r := range s {
		out, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		p = out.(*PaletteOverlay)
	}
	return p
}

// TestPaletteFiltersFuzzy: typing narrows and reorders the visible commands.
func TestPaletteFiltersFuzzy(t *testing.T) {
	p := NewPaletteOverlay(paletteCmds(), 100, 40)
	if len(p.filtered) != 3 {
		t.Fatalf("initial palette should show all 3 commands, got %d", len(p.filtered))
	}
	p = typeRunes(p, "review")
	if len(p.filtered) == 0 || p.filtered[0].ID != "review" {
		t.Fatalf("query 'review' should surface the review command, got %v", ids(p.filtered))
	}
}

// TestPaletteEnterEmitsChoice: enter on the selection dismisses the overlay
// carrying the chosen command so the root can run it.
func TestPaletteEnterEmitsChoice(t *testing.T) {
	p := NewPaletteOverlay(paletteCmds(), 100, 40)
	p = typeRunes(p, "browser")

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no dismiss cmd")
	}
	msg := cmd()
	dm, ok := msg.(DismissMsg)
	if !ok {
		t.Fatalf("enter should emit DismissMsg, got %T", msg)
	}
	choice, ok := dm.Result.(PaletteChoice)
	if !ok {
		t.Fatalf("dismiss should carry a PaletteChoice, got %T", dm.Result)
	}
	if choice.Cmd.ID != "browser" {
		t.Errorf("expected chosen command browser, got %q", choice.Cmd.ID)
	}
}

// TestPaletteEscCloses: esc dismisses with no choice (a plain close).
func TestPaletteEscCloses(t *testing.T) {
	p := NewPaletteOverlay(paletteCmds(), 100, 40)
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no dismiss cmd")
	}
	dm, ok := cmd().(DismissMsg)
	if !ok {
		t.Fatalf("esc should emit DismissMsg, got %T", cmd())
	}
	if dm.Result != nil {
		t.Errorf("esc dismiss should carry no choice, got %v", dm.Result)
	}
}

// TestPaletteArrowNavigation: down/up move the selection within bounds.
func TestPaletteArrowNavigation(t *testing.T) {
	p := NewPaletteOverlay(paletteCmds(), 100, 40)
	if p.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", p.cursor)
	}
	out, _ := p.Update(tea.KeyMsg{Type: tea.KeyDown})
	p = out.(*PaletteOverlay)
	if p.cursor != 1 {
		t.Fatalf("down should move cursor to 1, got %d", p.cursor)
	}
	out, _ = p.Update(tea.KeyMsg{Type: tea.KeyUp})
	p = out.(*PaletteOverlay)
	if p.cursor != 0 {
		t.Fatalf("up should move cursor back to 0, got %d", p.cursor)
	}
	// Up at the top is clamped, not wrapped.
	out, _ = p.Update(tea.KeyMsg{Type: tea.KeyUp})
	p = out.(*PaletteOverlay)
	if p.cursor != 0 {
		t.Fatalf("up at top should clamp to 0, got %d", p.cursor)
	}
}

func ids(cmds []commands.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.ID
	}
	return out
}
