package langagents

import (
	"context"
	"runtime"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	la "github.com/madicen/appr-ai-sal/internal/review/langagents"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

func waitZone(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for zone.Get(id) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for zone %q", id)
		}
		runtime.Gosched()
	}
}

func clickCenter(t *testing.T, id string) tea.MouseMsg {
	t.Helper()
	waitZone(t, id)
	z := zone.Get(id)
	return tea.MouseMsg{
		X:      (z.StartX + z.EndX) / 2,
		Y:      (z.StartY + z.EndY) / 2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
}

func clickFixture(t *testing.T) *Model {
	t.Helper()
	setEmptyCacheDir(t)
	complete := func(_ context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		return "stub brief", nil
	}
	m := New(Opts{
		AICfg:       aiconfig.DefaultConfig(),
		Complete:    la.CompleteFunc(complete),
		PRLanguages: []la.Language{"go", "python"},
		Width:       120,
		BodyHeight:  30,
	}).(*Model)
	zone.Scan(m.View())
	return m
}

// TestCloseClickNavigatesBack: the footer Close button mirrors esc.
func TestCloseClickNavigatesBack(t *testing.T) {
	m := clickFixture(t)
	cmd := m.handleMouse(clickCenter(t, ZoneClose))
	if cmd == nil {
		t.Fatal("Close click should emit a navigate command")
	}
	if _, ok := cmd().(state.NavigateMsg); !ok {
		t.Fatalf("Close click should emit state.NavigateMsg, got %T", cmd())
	}
}

// TestRowClickSelectsRow: clicking a language row mirrors ↑/↓ selection.
func TestRowClickSelectsRow(t *testing.T) {
	m := clickFixture(t)
	if m.idx != 0 {
		t.Fatalf("initial idx = %d, want 0", m.idx)
	}
	_ = m.handleMouse(clickCenter(t, zoneRow("python")))
	if m.idx != 1 {
		t.Fatalf("after clicking python row idx = %d, want 1", m.idx)
	}
}

// TestGenerateClickSelectsRowAndRuns: the per-row Generate button mirrors
// the g/r keys — it selects the row and kicks off generation.
func TestGenerateClickSelectsRowAndRuns(t *testing.T) {
	m := clickFixture(t)
	cmd := m.handleMouse(clickCenter(t, zoneRowGen("python")))
	if m.idx != 1 {
		t.Fatalf("Generate click should select its row; idx = %d want 1", m.idx)
	}
	if cmd == nil {
		t.Fatal("Generate click should return a non-nil command")
	}
}
