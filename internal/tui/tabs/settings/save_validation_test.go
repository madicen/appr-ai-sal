package settings

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/tui/state"
)

// newConfigWithActive builds a config whose single profile is active.
func newConfigWithActive(t *testing.T, p aiconfig.Profile) *aiconfig.Config {
	t.Helper()
	c := &aiconfig.Config{
		ActiveProfile:    p.Name,
		Profiles:         []aiconfig.Profile{p},
		ReviewStrictness: aiconfig.ReviewBalanced,
	}
	return c
}

// TestSaveBlocksInvalidActiveProfile: saving a settings form whose active
// profile is not runnable (gemini with no key) surfaces a clear error
// instead of persisting a broken config.
func TestSaveBlocksInvalidActiveProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	t.Setenv("APPR_AI_SAL_AI_API_KEY", "") // no ambient key

	cfg := newConfigWithActive(t, aiconfig.Profile{Name: "g", Provider: aiconfig.ProviderGemini, Model: "gemini-2.0-flash"})
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 120, StartSection: StartAI})

	msg := m.submitSave()()
	nav, ok := msg.(state.NavigateMsg)
	if !ok {
		t.Fatalf("expected NavigateMsg, got %T", msg)
	}
	if nav.Target.Err == nil {
		t.Fatalf("expected a validation error for gemini-without-key on save")
	}
}

// TestSaveAllowsValidActiveProfile: a runnable active profile (ollama, which
// needs no key and no base URL) saves cleanly.
func TestSaveAllowsValidActiveProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)

	cfg := newConfigWithActive(t, aiconfig.Profile{Name: "local", Provider: aiconfig.ProviderOllama, Model: "qwen2.5-coder:7b"})
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 120, StartSection: StartAI})

	msg := m.submitSave()()
	nav, ok := msg.(state.NavigateMsg)
	if !ok {
		t.Fatalf("expected NavigateMsg, got %T", msg)
	}
	if nav.Target.Err != nil {
		t.Fatalf("valid ollama profile should save without error, got %v", nav.Target.Err)
	}
	if nav.Target.Cfg == nil {
		t.Fatalf("expected the reloaded config to be returned on a successful save")
	}
}
