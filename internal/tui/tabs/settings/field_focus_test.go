package settings

import (
	"runtime"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

func waitFieldZone(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for zone.Get(id) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for zone %q", id)
		}
		runtime.Gosched()
	}
}

func clickFieldZone(t *testing.T, id string) tea.MouseMsg {
	t.Helper()
	waitFieldZone(t, id)
	z := zone.Get(id)
	return tea.MouseMsg{
		X:      (z.StartX + z.EndX) / 2,
		Y:      (z.StartY + z.EndY) / 2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
}

// TestAIFieldClickFocusesField: clicking a profile text field focuses it,
// the click equivalent of tabbing to the field.
func TestAIFieldClickFocusesField(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 120, StartSection: StartAI})
	zone.Scan(m.View())

	_ = m.handleMouse(clickFieldZone(t, ZoneAIFieldModel))
	if m.focus != fieldModel {
		t.Fatalf("clicking the model field should set focus=fieldModel, got %d", m.focus)
	}
	if !m.model.Focused() {
		t.Fatal("model textinput should be focused after click")
	}
}

// TestRepoFieldClickFocusesField: same click-to-focus for a repo-context
// text field.
func TestRepoFieldClickFocusesField(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 200, StartSection: StartRepoContext})
	zone.Scan(m.View())

	_ = m.handleMouse(clickFieldZone(t, ZoneRepoFieldTTL))
	if m.repoFocus != repoFieldTTL {
		t.Fatalf("clicking the ttl field should set repoFocus=repoFieldTTL, got %d", m.repoFocus)
	}
}
