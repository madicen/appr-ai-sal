package aiconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withConfigDir runs fn with APPR_AI_SAL_CONFIG_DIR set to a fresh temp
// directory so Load / Save touch a sandbox rather than the user's home.
func withConfigDir(t *testing.T, fn func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	// Make sure no env overrides leak into the test cases.
	for _, k := range []string{
		"APPR_AI_SAL_AI_PROVIDER",
		"APPR_AI_SAL_AI_BASE_URL",
		"APPR_AI_SAL_AI_MODEL",
		"APPR_AI_SAL_AI_API_KEY",
		"APPR_AI_SAL_AI_TIMEOUT_SEC",
		"APPR_AI_SAL_REVIEW_STRICTNESS",
		"APPR_AI_SAL_AI_RETRY_MAX_ATTEMPTS",
		"APPR_AI_SAL_AI_RETRY_BASE_MS",
		"APPR_AI_SAL_AI_RETRY_MAX_MS",
		"APPR_AI_SAL_MODEL",
	} {
		t.Setenv(k, "")
	}
	fn(dir)
}

func TestDefaultConfigHasOneProfile(t *testing.T) {
	c := DefaultConfig()
	if len(c.Profiles) != 1 {
		t.Fatalf("default config: expected 1 profile, got %d", len(c.Profiles))
	}
	if c.Profiles[0].Name != DefaultProfileName {
		t.Fatalf("default profile name: got %q, want %q", c.Profiles[0].Name, DefaultProfileName)
	}
	if c.ActiveProfile != DefaultProfileName {
		t.Fatalf("active profile: got %q, want %q", c.ActiveProfile, DefaultProfileName)
	}
	if got := c.Active(); got.Name != DefaultProfileName {
		t.Fatalf("Active(): got %q, want %q", got.Name, DefaultProfileName)
	}
}

func TestLoadLegacyFlatShapeMigratesToProfile(t *testing.T) {
	withConfigDir(t, func(dir string) {
		// Write a legacy ai.json with no profiles list.
		legacy := []byte(`{
  "provider": "openai_compatible",
  "base_url": "http://localhost:1234",
  "model": "qwen-coder",
  "api_key": "sk-test",
  "timeout_sec": 120,
  "review_strictness": "strict"
}`)
		if err := os.WriteFile(filepath.Join(dir, "ai.json"), legacy, 0o600); err != nil {
			t.Fatalf("write legacy: %v", err)
		}
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(c.Profiles) != 1 {
			t.Fatalf("expected 1 profile after migration, got %d", len(c.Profiles))
		}
		if c.Profiles[0].Name != DefaultProfileName {
			t.Fatalf("migrated profile name: got %q, want %q", c.Profiles[0].Name, DefaultProfileName)
		}
		if c.Profiles[0].Provider != ProviderOpenAICompatible {
			t.Fatalf("migrated provider: got %q, want openai_compatible", c.Profiles[0].Provider)
		}
		if c.Profiles[0].Model != "qwen-coder" {
			t.Fatalf("migrated model: got %q", c.Profiles[0].Model)
		}
		if c.Profiles[0].APIKey != "sk-test" {
			t.Fatalf("migrated apikey not preserved")
		}
		if c.ReviewStrictness != ReviewStrict {
			t.Fatalf("strictness: got %q", c.ReviewStrictness)
		}
		// Top-level fields should mirror the active profile.
		if c.Provider != ProviderOpenAICompatible || c.Model != "qwen-coder" {
			t.Fatalf("top-level fields not mirrored: %+v", *c)
		}
	})
}

func TestSaveAndLoadRoundTripPreservesMultipleProfiles(t *testing.T) {
	withConfigDir(t, func(dir string) {
		c := DefaultConfig()
		c.Profiles[0].Model = "sonnet"
		if err := c.AddProfile(Profile{
			Name:     "local",
			Provider: ProviderOllama,
			Model:    "qwen2.5-coder:7b",
		}); err != nil {
			t.Fatalf("AddProfile: %v", err)
		}
		if err := c.AddProfile(Profile{
			Name:     "openrouter",
			Provider: ProviderOpenAICompatible,
			BaseURL:  "https://openrouter.ai/api/v1",
			Model:    "anthropic/claude-3.5-sonnet",
			APIKey:   "sk-or-foo",
		}); err != nil {
			t.Fatalf("AddProfile openrouter: %v", err)
		}
		if err := c.SetActive("local"); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
		if err := Save(c, ""); err != nil {
			t.Fatalf("Save: %v", err)
		}

		// Inspect the on-disk JSON: should NOT have flat top-level
		// provider/model/etc., only profiles + active_profile.
		raw, err := os.ReadFile(filepath.Join(dir, "ai.json"))
		if err != nil {
			t.Fatalf("read saved file: %v", err)
		}
		var onDisk map[string]any
		if err := json.Unmarshal(raw, &onDisk); err != nil {
			t.Fatalf("unmarshal saved file: %v", err)
		}
		if _, has := onDisk["provider"]; has {
			t.Fatalf("saved file should not contain top-level 'provider' field; got %s", string(raw))
		}
		if _, has := onDisk["profiles"]; !has {
			t.Fatalf("saved file missing 'profiles': %s", string(raw))
		}

		// Round-trip
		c2, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(c2.Profiles) != 3 {
			t.Fatalf("expected 3 profiles round-tripped, got %d", len(c2.Profiles))
		}
		if c2.ActiveProfile != "local" {
			t.Fatalf("active profile: got %q, want local", c2.ActiveProfile)
		}
		// Active profile mirrored on top-level fields.
		if c2.Provider != ProviderOllama {
			t.Fatalf("top-level provider: got %q, want ollama", c2.Provider)
		}
		if c2.Model != "qwen2.5-coder:7b" {
			t.Fatalf("top-level model: got %q", c2.Model)
		}
	})
}

func TestSetActiveSwitchesAndMirrors(t *testing.T) {
	c := DefaultConfig()
	c.Profiles[0].Model = "sonnet"
	if err := c.AddProfile(Profile{Name: "fast", Provider: ProviderOllama, Model: "phi3"}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := c.SetActive("fast"); err != nil {
		t.Fatalf("SetActive fast: %v", err)
	}
	if c.Provider != ProviderOllama || c.Model != "phi3" {
		t.Fatalf("top-level not mirrored after SetActive: %+v", *c)
	}
	if err := c.SetActive("nope"); err == nil {
		t.Fatalf("SetActive nope: expected error for unknown profile")
	}
}

func TestAddDuplicateProfileFails(t *testing.T) {
	c := DefaultConfig()
	if err := c.AddProfile(Profile{Name: DefaultProfileName, Provider: ProviderClaude}); err == nil {
		t.Fatalf("expected error adding duplicate profile name")
	}
	if err := c.AddProfile(Profile{Name: ""}); err == nil {
		t.Fatalf("expected error adding empty profile name")
	}
}

func TestDeleteProfileBlockedOnLast(t *testing.T) {
	c := DefaultConfig()
	if err := c.DeleteProfile(DefaultProfileName); err == nil {
		t.Fatalf("expected error deleting last profile")
	}

	if err := c.AddProfile(Profile{Name: "extra", Provider: ProviderClaude}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	_ = c.SetActive("extra")
	if err := c.DeleteProfile("extra"); err != nil {
		t.Fatalf("DeleteProfile extra: %v", err)
	}
	if c.ActiveProfile != DefaultProfileName {
		t.Fatalf("active profile after delete: got %q, want %q", c.ActiveProfile, DefaultProfileName)
	}
}

func TestRenameProfile(t *testing.T) {
	c := DefaultConfig()
	if err := c.AddProfile(Profile{Name: "extra", Provider: ProviderClaude}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := c.RenameProfile("extra", "renamed"); err != nil {
		t.Fatalf("RenameProfile: %v", err)
	}
	if _, ok := c.findProfileIndex("renamed"); !ok {
		t.Fatalf("renamed profile not found")
	}
	// Rename collision should fail.
	if err := c.RenameProfile("renamed", DefaultProfileName); err == nil {
		t.Fatalf("expected collision error")
	}
}

func TestCloneIsDeep(t *testing.T) {
	c := DefaultConfig()
	if err := c.AddProfile(Profile{Name: "x", Provider: ProviderClaude, Model: "first"}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	cp := c.Clone()
	cp.Profiles[1].Model = "mutated"
	if c.Profiles[1].Model == "mutated" {
		t.Fatalf("Clone is not a deep copy of Profiles slice")
	}
}

func TestCycleActiveWraps(t *testing.T) {
	c := DefaultConfig()
	_ = c.AddProfile(Profile{Name: "b", Provider: ProviderClaude})
	_ = c.AddProfile(Profile{Name: "c", Provider: ProviderClaude})
	c.CycleActive(1)
	if c.ActiveProfile != "b" {
		t.Fatalf("CycleActive +1: got %q, want b", c.ActiveProfile)
	}
	c.CycleActive(2)
	if c.ActiveProfile != DefaultProfileName {
		t.Fatalf("CycleActive +2: got %q, want %s", c.ActiveProfile, DefaultProfileName)
	}
	c.CycleActive(-1)
	if c.ActiveProfile != "c" {
		t.Fatalf("CycleActive -1: got %q, want c", c.ActiveProfile)
	}
}
