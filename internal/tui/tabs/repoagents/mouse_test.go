package repoagents

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

// TestMain initializes bubblezone once for the whole test binary. Calling
// zone.NewGlobal() per-test races with the package's async scanner and can
// drop zones that were just rendered, so we set it up once here.
func TestMain(m *testing.M) {
	zone.NewGlobal()
	os.Exit(m.Run())
}

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

func renderAndScan(m *Model) {
	zone.Scan(m.View())
}

func newTestModel(t *testing.T, repos []string) *Model {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")
	complete := func(_ context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		return "stub-context", nil
	}
	return New(Opts{
		AICfg:        aiconfig.DefaultConfig(),
		RC:           repoconfig.Default(),
		Width:        140,
		BodyHeight:   30,
		Complete:     ra.CompleteFunc(complete),
		InitialRepos: repos,
	})
}

func TestMouseClickCloseSendsNavigateMsg(t *testing.T) {
	m := newTestModel(t, []string{"acme/widget"})
	renderAndScan(m)
	msg := clickCenter(t, ZoneClose)
	cmd := m.handleMouse(msg)
	if cmd == nil {
		t.Fatalf("expected NavigateMsg cmd from Close click")
	}
	switch v := cmd().(type) {
	case state.NavigateMsg:
		if v.Target.Kind != state.NavBack {
			t.Fatalf("Close should emit NavBack, got %v", v.Target.Kind)
		}
		if v.Target.Cancelled {
			t.Fatalf("mouse-driven Close should not flag Cancelled")
		}
	default:
		t.Fatalf("expected state.NavigateMsg, got %T", v)
	}
}

func TestMouseClickPrevNextRotatesRepoIdx(t *testing.T) {
	m := newTestModel(t, []string{"a/b", "c/d", "e/f"})
	renderAndScan(m)
	if m.repoIdx != 0 {
		t.Fatalf("initial repoIdx should be 0, got %d", m.repoIdx)
	}

	// Click "next →" twice.
	for i := 0; i < 2; i++ {
		_ = m.handleMouse(clickCenter(t, ZoneNextRepo))
		renderAndScan(m)
	}
	if m.repoIdx != 2 {
		t.Fatalf("after 2 next clicks repoIdx=%d want 2", m.repoIdx)
	}
	// Wrap-around with one more next.
	_ = m.handleMouse(clickCenter(t, ZoneNextRepo))
	renderAndScan(m)
	if m.repoIdx != 0 {
		t.Fatalf("wraparound: repoIdx=%d want 0", m.repoIdx)
	}
	// Prev wraps to last.
	_ = m.handleMouse(clickCenter(t, ZonePrevRepo))
	renderAndScan(m)
	if m.repoIdx != 2 {
		t.Fatalf("prev-wraparound: repoIdx=%d want 2", m.repoIdx)
	}
}

func TestMouseClickAddRepoOpensInputAndSaveAddsRepo(t *testing.T) {
	m := newTestModel(t, []string{"a/b"})
	renderAndScan(m)
	_ = m.handleMouse(clickCenter(t, ZoneAddRepoOpen))
	if !m.addingRepo {
		t.Fatal("expected addingRepo to flip true")
	}
	m.addInput.SetValue("globex/engine")
	renderAndScan(m)
	cmd := m.handleMouse(clickCenter(t, ZoneAddRepoSave))
	if cmd != nil {
		// commitAddRepo can return a load command; that's fine.
	}
	if m.addingRepo {
		t.Fatal("addingRepo should be reset after save click")
	}
	found := false
	for _, r := range m.repos {
		if r == "globex/engine" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("globex/engine should be in repos: %v", m.repos)
	}
}

func TestMouseClickAgentRegenerateMarksBusy(t *testing.T) {
	m := newTestModel(t, []string{"a/b"})
	renderAndScan(m)
	cmd := m.handleMouse(clickCenter(t, zoneAgentRegen("testing")))
	if cmd == nil {
		t.Fatalf("expected non-nil cmd from regenerate click")
	}
	if !m.busy[busyKey("a", "b", "testing")] {
		t.Fatalf("regenerate click should mark busy[%q] true", busyKey("a", "b", "testing"))
	}
}

func TestMouseClickEditOpensTextarea(t *testing.T) {
	m := newTestModel(t, []string{"a/b"})
	// Seed an existing brief so Edit has something to load.
	if err := ra.SaveAgent("a", "b", ra.Agent{Specialist: "testing", Context: "hand-edit me"}); err != nil {
		t.Fatal(err)
	}
	got, _ := ra.Load("a", "b")
	m.agents["a/b"] = got
	renderAndScan(m)
	_ = m.handleMouse(clickCenter(t, zoneAgentEdit("testing")))
	if !m.editing || m.editSpecialist != "testing" {
		t.Fatalf("expected editing=testing got editing=%v editSpecialist=%q", m.editing, m.editSpecialist)
	}
	if m.editArea.Value() == "" {
		t.Fatalf("edit area should be pre-populated from saved agent")
	}
}

func TestMouseEditCancelExitsWithoutSaving(t *testing.T) {
	m := newTestModel(t, []string{"a/b"})
	m.editing = true
	m.editSpecialist = "design"
	m.editArea.SetValue("draft body")
	renderAndScan(m)
	if cmd := m.handleMouse(clickCenter(t, ZoneEditCancel)); cmd != nil {
		t.Fatalf("Cancel click should not emit a command, got %T", cmd())
	}
	if m.editing {
		t.Fatalf("editing should be false after cancel")
	}
}
