package aiconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// fakeClaudeOnPath creates a temp directory containing an executable named
// "claude" and points PATH at it, so ValidateForProvider's exec.LookPath
// succeeds deterministically regardless of the CI host. On Windows the shim
// gets a .bat name (skipped: this repo targets unix-like dev/CI).
func fakeClaudeOnPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("claude PATH shim assumes a unix-like host")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir)
}

// noClaudeOnPath points PATH at an empty directory so exec.LookPath("claude")
// fails deterministically.
func noClaudeOnPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestValidateForProvider(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		setup   func(t *testing.T)
		wantErr bool
	}{
		{
			name:    "openai_compatible missing base url",
			profile: Profile{Name: "oc", Provider: ProviderOpenAICompatible},
			wantErr: true,
		},
		{
			name:    "openai_compatible malformed base url",
			profile: Profile{Name: "oc", Provider: ProviderOpenAICompatible, BaseURL: "not a url"},
			wantErr: true,
		},
		{
			name:    "openai_compatible non-http scheme",
			profile: Profile{Name: "oc", Provider: ProviderOpenAICompatible, BaseURL: "ftp://example.com/v1"},
			wantErr: true,
		},
		{
			name:    "openai_compatible ok",
			profile: Profile{Name: "oc", Provider: ProviderOpenAICompatible, BaseURL: "https://api.openai.com/v1"},
			wantErr: false,
		},
		{
			name:    "gemini missing key",
			profile: Profile{Name: "g", Provider: ProviderGemini},
			wantErr: true,
		},
		{
			name:    "gemini explicit key ok",
			profile: Profile{Name: "g", Provider: ProviderGemini, APIKey: "sk-x"},
			wantErr: false,
		},
		{
			name:    "gemini via api_key_cmd ok",
			profile: Profile{Name: "g", Provider: ProviderGemini, APIKeyCmd: "echo k"},
			wantErr: false,
		},
		{
			name:    "gemini via api_key_env set",
			profile: Profile{Name: "g", Provider: ProviderGemini, APIKeyEnv: "R8_TEST_GEMINI_KEY"},
			setup:   func(t *testing.T) { t.Setenv("R8_TEST_GEMINI_KEY", "sk-env") },
			wantErr: false,
		},
		{
			name:    "gemini via api_key_env unset",
			profile: Profile{Name: "g", Provider: ProviderGemini, APIKeyEnv: "R8_TEST_GEMINI_KEY_UNSET"},
			setup:   func(t *testing.T) { t.Setenv("R8_TEST_GEMINI_KEY_UNSET", "") },
			wantErr: true,
		},
		{
			name:    "gemini bad base url",
			profile: Profile{Name: "g", Provider: ProviderGemini, APIKey: "sk-x", BaseURL: "http://"},
			wantErr: true,
		},
		{
			name:    "ollama no base url ok",
			profile: Profile{Name: "o", Provider: ProviderOllama},
			wantErr: false,
		},
		{
			name:    "ollama bad base url",
			profile: Profile{Name: "o", Provider: ProviderOllama, BaseURL: "://nope"},
			wantErr: true,
		},
		{
			name:    "ollama good base url ok",
			profile: Profile{Name: "o", Provider: ProviderOllama, BaseURL: "http://127.0.0.1:11434/v1"},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			err := tc.profile.ValidateForProvider()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateForProviderClaudePath(t *testing.T) {
	t.Run("claude on PATH", func(t *testing.T) {
		fakeClaudeOnPath(t)
		p := Profile{Name: "c", Provider: ProviderClaude}
		if err := p.ValidateForProvider(); err != nil {
			t.Fatalf("expected nil with claude on PATH, got %v", err)
		}
	})
	t.Run("claude not on PATH", func(t *testing.T) {
		noClaudeOnPath(t)
		p := Profile{Name: "c", Provider: ProviderClaude}
		if err := p.ValidateForProvider(); err == nil {
			t.Fatalf("expected error with claude missing from PATH")
		}
	})
}

// TestConfigValidateForProviderUsesFlatEffective proves the Config-level
// validation looks at the effective (flat) settings — including a one-shot
// key — not just the (post-provenance) profile slot.
func TestConfigValidateForProviderUsesFlatEffective(t *testing.T) {
	c := &Config{Provider: ProviderGemini, ActiveProfile: "g", APIKey: "sk-oneshot"}
	if err := c.ValidateForProvider(); err != nil {
		t.Fatalf("expected active gemini with a flat key to validate, got %v", err)
	}
	c2 := &Config{Provider: ProviderGemini, ActiveProfile: "g"}
	if err := c2.ValidateForProvider(); err == nil {
		t.Fatalf("expected active gemini without a key to fail validation")
	}
}

func TestMergeEnvWarnsOnInvalidValue(t *testing.T) {
	withConfigDir(t, func(dir string) {
		// Route applog to a temp file so we can assert the warning was
		// emitted (rather than the value being silently dropped).
		logDir := t.TempDir()
		t.Setenv("APPR_AI_SAL_LOG_DIR", logDir)
		t.Setenv("APPR_AI_SAL_LOG_LEVEL", "info")
		if err := applog.Init("test"); err != nil {
			t.Fatalf("applog.Init: %v", err)
		}
		// A valid model plus an invalid (non-integer) timeout.
		t.Setenv("APPR_AI_SAL_AI_MODEL", "some-model")
		t.Setenv("APPR_AI_SAL_AI_TIMEOUT_SEC", "not-a-number")

		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		// Valid override still applied.
		if c.Model != "some-model" {
			t.Fatalf("valid model override not applied: got %q", c.Model)
		}
		// Invalid override dropped (default kept).
		if c.TimeoutSec != 300 {
			t.Fatalf("invalid timeout should have been dropped, kept default 300; got %d", c.TimeoutSec)
		}
		logBytes, err := os.ReadFile(filepath.Join(logDir, applog.LogFileName))
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		log := string(logBytes)
		if !strings.Contains(log, "APPR_AI_SAL_AI_TIMEOUT_SEC") || !strings.Contains(log, "ignoring invalid") {
			t.Fatalf("expected a warning about the invalid timeout env, log was:\n%s", log)
		}
	})
}

func TestEffectiveAPIKeyIndirectionPrecedence(t *testing.T) {
	// Explicit key wins over env and cmd.
	t.Setenv("R8_KEY_ENV", "sk-env")
	c := &Config{APIKey: "sk-explicit", APIKeyEnv: "R8_KEY_ENV", APIKeyCmd: "echo sk-r8-cmd-a"}
	if got := c.EffectiveAPIKey(); got != "sk-explicit" {
		t.Fatalf("explicit should win: got %q", got)
	}
	// api_key_env wins over api_key_cmd when no explicit key.
	c.APIKey = ""
	if got := c.EffectiveAPIKey(); got != "sk-env" {
		t.Fatalf("api_key_env should win over cmd: got %q", got)
	}
	// api_key_cmd used when explicit + env absent.
	c.APIKeyEnv = ""
	if got := c.EffectiveAPIKey(); got != "sk-r8-cmd-a" {
		t.Fatalf("api_key_cmd not resolved: got %q", got)
	}
	// Empty everywhere.
	empty := &Config{}
	if got := empty.EffectiveAPIKey(); got != "" {
		t.Fatalf("expected empty key, got %q", got)
	}
}

func TestEffectiveAPIKeyEnvUnsetFallsThrough(t *testing.T) {
	t.Setenv("R8_KEY_ENV_UNSET", "")
	c := &Config{APIKeyEnv: "R8_KEY_ENV_UNSET", APIKeyCmd: "echo sk-r8-cmd-b"}
	if got := c.EffectiveAPIKey(); got != "sk-r8-cmd-b" {
		t.Fatalf("unset env var should fall through to cmd: got %q", got)
	}
}

func TestRedaction(t *testing.T) {
	c := DefaultConfig()
	c.Provider = ProviderGemini
	c.APIKey = "sk-supersecret"
	c.Profiles[0].APIKey = "sk-supersecret"
	c.Profiles[0].APIKeyEnv = "SOME_ENV"

	r := c.Redacted()
	if r.APIKey == "sk-supersecret" || strings.Contains(r.APIKey, "supersecret") {
		t.Fatalf("flat APIKey not redacted: %q", r.APIKey)
	}
	if r.Profiles[0].APIKey == "sk-supersecret" || strings.Contains(r.Profiles[0].APIKey, "supersecret") {
		t.Fatalf("profile APIKey not redacted: %q", r.Profiles[0].APIKey)
	}
	// Original is untouched.
	if c.APIKey != "sk-supersecret" {
		t.Fatalf("Redacted mutated the original config")
	}
	// Non-secret indirection reference kept.
	if r.Profiles[0].APIKeyEnv != "SOME_ENV" {
		t.Fatalf("api_key_env reference should be preserved, got %q", r.Profiles[0].APIKeyEnv)
	}
	js := c.RedactedJSON()
	if strings.Contains(js, "supersecret") {
		t.Fatalf("RedactedJSON leaked key material:\n%s", js)
	}
	if !strings.Contains(js, "gemini") {
		t.Fatalf("RedactedJSON should still show non-secret fields:\n%s", js)
	}
}

// TestOneShotEnvKeyNotPersisted proves a one-shot APPR_AI_SAL_AI_API_KEY is
// used for the run but never written into the profile on Save.
func TestOneShotEnvKeyNotPersisted(t *testing.T) {
	withConfigDir(t, func(dir string) {
		t.Setenv("APPR_AI_SAL_AI_API_KEY", "sk-oneshot-env")
		seed := []byte(`{"active_profile":"default","profiles":[{"name":"default","provider":"gemini","model":"g","api_key":"sk-file"}]}`)
		if err := os.WriteFile(filepath.Join(dir, "ai.json"), seed, 0o600); err != nil {
			t.Fatalf("write seed: %v", err)
		}
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		// The one-shot key is what the run uses.
		if c.EffectiveAPIKey() != "sk-oneshot-env" {
			t.Fatalf("run should use one-shot env key, got %q", c.EffectiveAPIKey())
		}
		// But the profile slot keeps the file value.
		if c.Profiles[0].APIKey != "sk-file" {
			t.Fatalf("profile slot should keep the file key, got %q", c.Profiles[0].APIKey)
		}
		if err := Save(c, ""); err != nil {
			t.Fatalf("Save: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "ai.json"))
		if err != nil {
			t.Fatalf("read saved: %v", err)
		}
		if strings.Contains(string(raw), "sk-oneshot-env") {
			t.Fatalf("one-shot env key leaked into the saved profile:\n%s", string(raw))
		}
		if !strings.Contains(string(raw), "sk-file") {
			t.Fatalf("saved profile lost its real key:\n%s", string(raw))
		}
	})
}

// TestOneShotFlagKeyNotPersisted proves a one-shot --ai-api-key (via
// MergeFlags) is used for the run but never written into the profile on Save.
func TestOneShotFlagKeyNotPersisted(t *testing.T) {
	withConfigDir(t, func(dir string) {
		seed := []byte(`{"active_profile":"default","profiles":[{"name":"default","provider":"gemini","model":"g","api_key":"sk-file"}]}`)
		if err := os.WriteFile(filepath.Join(dir, "ai.json"), seed, 0o600); err != nil {
			t.Fatalf("write seed: %v", err)
		}
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if err := c.MergeFlags("", "", "", "sk-oneshot-flag", "", -1); err != nil {
			t.Fatalf("MergeFlags: %v", err)
		}
		if c.EffectiveAPIKey() != "sk-oneshot-flag" {
			t.Fatalf("run should use one-shot flag key, got %q", c.EffectiveAPIKey())
		}
		if c.Profiles[0].APIKey != "sk-file" {
			t.Fatalf("profile slot should keep the file key, got %q", c.Profiles[0].APIKey)
		}
		if err := Save(c, ""); err != nil {
			t.Fatalf("Save: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "ai.json"))
		if err != nil {
			t.Fatalf("read saved: %v", err)
		}
		if strings.Contains(string(raw), "sk-oneshot-flag") {
			t.Fatalf("one-shot flag key leaked into the saved profile:\n%s", string(raw))
		}
		if !strings.Contains(string(raw), "sk-file") {
			t.Fatalf("saved profile lost its real key:\n%s", string(raw))
		}
	})
}

// TestOneShotOverrideOtherFieldsNotPersisted covers provider/model/base_url
// one-shots, ensuring provenance exclusion is not key-specific.
func TestOneShotOverrideOtherFieldsNotPersisted(t *testing.T) {
	withConfigDir(t, func(dir string) {
		t.Setenv("APPR_AI_SAL_AI_MODEL", "one-shot-model")
		t.Setenv("APPR_AI_SAL_AI_PROVIDER", "ollama")
		seed := []byte(`{"active_profile":"default","profiles":[{"name":"default","provider":"gemini","model":"file-model","api_key":"sk-file"}]}`)
		if err := os.WriteFile(filepath.Join(dir, "ai.json"), seed, 0o600); err != nil {
			t.Fatalf("write seed: %v", err)
		}
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if c.Provider != ProviderOllama || c.Model != "one-shot-model" {
			t.Fatalf("run should see one-shot provider/model: %q / %q", c.Provider, c.Model)
		}
		if err := Save(c, ""); err != nil {
			t.Fatalf("Save: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "ai.json"))
		if err != nil {
			t.Fatalf("read saved: %v", err)
		}
		if strings.Contains(string(raw), "one-shot-model") {
			t.Fatalf("one-shot model leaked into saved profile:\n%s", string(raw))
		}
		if !strings.Contains(string(raw), "file-model") {
			t.Fatalf("saved profile lost its file model:\n%s", string(raw))
		}
		if !strings.Contains(string(raw), `"provider": "gemini"`) {
			t.Fatalf("one-shot provider leaked; saved profile should stay gemini:\n%s", string(raw))
		}
	})
}
