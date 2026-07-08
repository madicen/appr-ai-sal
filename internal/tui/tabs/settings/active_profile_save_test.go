package settings

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

// R8 regression: editing the currently active profile then saving must persist
// the editor changes instead of reverting to the stale top-level mirror.
func TestSavePersistsActiveProfileEdits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)

	cfg := newConfigWithActive(t, aiconfig.Profile{
		Name:       "local",
		Provider:   aiconfig.ProviderOllama,
		Model:      "qwen2.5-coder:7b",
		BaseURL:    "http://127.0.0.1:11434",
		TimeoutSec: 300,
	})
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 120, StartSection: StartAI})

	m.model.SetValue("llama3.1:8b")
	m.baseURL.SetValue("http://127.0.0.1:11435")

	msg := m.submitSave()()
	nav, ok := msg.(state.NavigateMsg)
	if !ok {
		t.Fatalf("expected NavigateMsg, got %T", msg)
	}
	if nav.Target.Err != nil {
		t.Fatalf("save failed: %v", nav.Target.Err)
	}
	if nav.Target.Cfg == nil {
		t.Fatal("expected reloaded config on successful save")
	}
	active := nav.Target.Cfg.Active()
	if active.Model != "llama3.1:8b" {
		t.Fatalf("active profile model = %q, want edited llama3.1:8b", active.Model)
	}
	if active.BaseURL != "http://127.0.0.1:11435" {
		t.Fatalf("active profile base URL = %q, want edited port 11435", active.BaseURL)
	}
}
