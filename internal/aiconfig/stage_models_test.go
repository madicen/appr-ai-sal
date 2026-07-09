package aiconfig

import (
	"reflect"
	"strings"
	"testing"
)

// TestStageModelPrecedence pins the Q7 precedence: an explicit stage entry
// beats stage_models["default"], which beats the profile Model.
func TestStageModelPrecedence(t *testing.T) {
	t.Parallel()
	c := &Config{
		Model: "profile-model",
		StageModels: map[string]string{
			"security": "opus",
			"default":  "haiku",
		},
	}
	if got := c.StageModel("security"); got != "opus" {
		t.Fatalf("security stage model = %q, want opus (explicit entry)", got)
	}
	if got := c.StageModel("formatting"); got != "haiku" {
		t.Fatalf("formatting stage model = %q, want haiku (stage_models default)", got)
	}
	// A profile with only an explicit entry (no default) falls through to the
	// profile Model for unlisted stages.
	c2 := &Config{Model: "profile-model", StageModels: map[string]string{"security": "opus"}}
	if got := c2.StageModel("docs"); got != "profile-model" {
		t.Fatalf("docs stage model = %q, want profile-model (fall through)", got)
	}
	// Case/whitespace-insensitive stage lookup.
	c3 := &Config{Model: "m", StageModels: map[string]string{" Security ": "opus"}}
	if got := c3.StageModel("SECURITY"); got != "opus" {
		t.Fatalf("case-insensitive stage lookup = %q, want opus", got)
	}
}

// TestForStageBackwardCompatIdentical is the backward-compat acceptance test: a
// config with no stage_models returns the SAME config for every stage (no
// clone, no model change), so routed behavior is byte-for-byte identical to the
// pre-Q7 single-model path.
func TestForStageBackwardCompatIdentical(t *testing.T) {
	t.Parallel()
	c := &Config{Provider: ProviderClaude, Model: "sonnet"}
	for _, stage := range []string{"security", "formatting", "arbiter", "witness", "vibe-coach", "anything"} {
		if got := c.ForStage(stage); got != c {
			t.Fatalf("ForStage(%q) returned a different config (%p != %p); want the receiver unchanged", stage, got, c)
		}
	}
	// A stage whose routed model equals the profile Model is also a no-op
	// (same pointer), so no needless clone happens.
	c2 := &Config{Model: "sonnet", StageModels: map[string]string{"security": "sonnet"}}
	if got := c2.ForStage("security"); got != c2 {
		t.Fatalf("ForStage with model == profile Model should be a no-op; got %p want %p", got, c2)
	}
}

// TestForStageRoutesModel confirms a routed stage yields a clone with the
// stage model, leaving the original untouched and the provider/key intact.
func TestForStageRoutesModel(t *testing.T) {
	t.Parallel()
	c := &Config{
		Provider:    ProviderOpenAICompatible,
		BaseURL:     "https://api.example.com/v1",
		APIKey:      "secret",
		Model:       "base",
		StageModels: map[string]string{"security": "opus"},
	}
	routed := c.ForStage("security")
	if routed == c {
		t.Fatal("routed stage should return a clone, not the receiver")
	}
	if routed.Model != "opus" {
		t.Fatalf("routed model = %q, want opus", routed.Model)
	}
	if routed.Provider != c.Provider || routed.BaseURL != c.BaseURL || routed.APIKey != c.APIKey {
		t.Fatalf("routing must not change provider/base-url/key: %+v", routed)
	}
	if c.Model != "base" {
		t.Fatalf("original Model mutated to %q; want base", c.Model)
	}
}

// TestEnsembleModels checks list normalization: not-configured → nil, a
// single/collapsing list → nil, and a valid list de-duplicated in order.
func TestEnsembleModels(t *testing.T) {
	t.Parallel()
	c := &Config{Ensemble: map[string][]string{
		"security": {"m-a", " m-b ", "m-a", ""},
		"docs":     {"only-one"},
		"design":   {"", "  "},
	}}
	if got := c.EnsembleModels("security"); !reflect.DeepEqual(got, []string{"m-a", "m-b"}) {
		t.Fatalf("security ensemble = %v, want [m-a m-b]", got)
	}
	if got := c.EnsembleModels("docs"); got != nil {
		t.Fatalf("single-model ensemble should be nil, got %v", got)
	}
	if got := c.EnsembleModels("design"); got != nil {
		t.Fatalf("all-blank ensemble should be nil, got %v", got)
	}
	if got := c.EnsembleModels("formatting"); got != nil {
		t.Fatalf("unconfigured stage ensemble should be nil, got %v", got)
	}
	if got := (&Config{}).EnsembleModels("security"); got != nil {
		t.Fatalf("no ensemble map should be nil, got %v", got)
	}
}

// TestStageRoutingRoundTripsThroughProfile ensures the maps survive the
// profile mirror (snapshot on save, apply on load / SetActive) like the other
// per-profile knobs.
func TestStageRoutingRoundTripsThroughProfile(t *testing.T) {
	t.Parallel()
	c := DefaultConfig()
	c.StageModels = map[string]string{"security": "opus"}
	c.Ensemble = map[string][]string{"security": {"m-a", "m-b"}}
	c.Profiles[0] = c.snapshotProfile(c.Profiles[0].Name)
	// Scribble the flat fields, then restore from the profile.
	c.StageModels = nil
	c.Ensemble = nil
	c.applyActiveProfile()
	if got := c.StageModel("security"); got != "opus" {
		t.Fatalf("stage model after round-trip = %q, want opus", got)
	}
	if got := c.EnsembleModels("security"); !reflect.DeepEqual(got, []string{"m-a", "m-b"}) {
		t.Fatalf("ensemble after round-trip = %v, want [m-a m-b]", got)
	}
}

// TestCloneDeepCopiesStageMaps proves a clone's maps are independent of the
// original's, so mutating one can never write through to the other.
func TestCloneDeepCopiesStageMaps(t *testing.T) {
	t.Parallel()
	c := &Config{
		StageModels: map[string]string{"security": "opus"},
		Ensemble:    map[string][]string{"security": {"m-a", "m-b"}},
	}
	cp := c.Clone()
	cp.StageModels["security"] = "MUTATED"
	cp.Ensemble["security"][0] = "MUTATED"
	if c.StageModels["security"] != "opus" {
		t.Fatalf("clone shares StageModels backing: %q", c.StageModels["security"])
	}
	if c.Ensemble["security"][0] != "m-a" {
		t.Fatalf("clone shares Ensemble slice backing: %q", c.Ensemble["security"][0])
	}
}

// TestValidateStageRoutingErrors covers the R8-style validation of malformed
// stage_models / ensemble entries.
func TestValidateStageRoutingErrors(t *testing.T) {
	fakeClaudeOnPath(t) // so the claude provider check doesn't fail first
	tests := []struct {
		name    string
		profile Profile
		wantErr string
	}{
		{
			name:    "empty stage model value",
			profile: Profile{Name: "p", StageModels: map[string]string{"security": "  "}},
			wantErr: "empty model id",
		},
		{
			name:    "empty stage key",
			profile: Profile{Name: "p", StageModels: map[string]string{"": "opus"}},
			wantErr: "empty stage name",
		},
		{
			name:    "single-model ensemble",
			profile: Profile{Name: "p", Ensemble: map[string][]string{"security": {"only-one"}}},
			wantErr: "at least two distinct models",
		},
		{
			name:    "empty model in ensemble",
			profile: Profile{Name: "p", Ensemble: map[string][]string{"security": {"m-a", ""}}},
			wantErr: "empty model id",
		},
		{
			name:    "duplicate model in ensemble",
			profile: Profile{Name: "p", Ensemble: map[string][]string{"security": {"m-a", "m-a"}}},
			wantErr: "more than once",
		},
		{
			name:    "valid routing",
			profile: Profile{Name: "p", StageModels: map[string]string{"security": "opus", "default": "haiku"}, Ensemble: map[string][]string{"security": {"m-a", "m-b"}}},
			wantErr: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.profile.ValidateForProvider()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
