package aiconfig

import "testing"

// TestParseProviderAnthropicAzure covers the Phase 6 provider aliases.
func TestParseProviderAnthropicAzure(t *testing.T) {
	cases := map[string]Provider{
		"anthropic":     ProviderAnthropic,
		"anthropic_api": ProviderAnthropic,
		"anthropic-api": ProviderAnthropic,
		"azure":         ProviderAzure,
		"azure_openai":  ProviderAzure,
		"azure-openai":  ProviderAzure,
	}
	for in, want := range cases {
		got, err := ParseProvider(in)
		if err != nil {
			t.Fatalf("ParseProvider(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseProvider(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseProvider("nope"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// TestValidateForProviderAnthropicAzure covers the Phase 6 validation rules:
// anthropic needs a key; azure needs base URL + key + deployment (model).
func TestValidateForProviderAnthropicAzure(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		setup   func(t *testing.T)
		wantErr bool
	}{
		{name: "anthropic missing key", profile: Profile{Name: "a", Provider: ProviderAnthropic}, wantErr: true},
		{name: "anthropic explicit key ok", profile: Profile{Name: "a", Provider: ProviderAnthropic, APIKey: "sk"}, wantErr: false},
		{name: "anthropic key_env ok", profile: Profile{Name: "a", Provider: ProviderAnthropic, APIKeyEnv: "P6_ANT_KEY"}, setup: func(t *testing.T) { t.Setenv("P6_ANT_KEY", "sk") }, wantErr: false},
		{name: "anthropic bad base url", profile: Profile{Name: "a", Provider: ProviderAnthropic, APIKey: "sk", BaseURL: "http://"}, wantErr: true},

		{name: "azure missing base url", profile: Profile{Name: "z", Provider: ProviderAzure, APIKey: "k", Model: "dep"}, wantErr: true},
		{name: "azure missing key", profile: Profile{Name: "z", Provider: ProviderAzure, BaseURL: "https://r.openai.azure.com", Model: "dep"}, wantErr: true},
		{name: "azure missing deployment", profile: Profile{Name: "z", Provider: ProviderAzure, BaseURL: "https://r.openai.azure.com", APIKey: "k"}, wantErr: true},
		{name: "azure ok", profile: Profile{Name: "z", Provider: ProviderAzure, BaseURL: "https://r.openai.azure.com", APIKey: "k", Model: "dep"}, wantErr: false},
		{name: "azure bad base url", profile: Profile{Name: "z", Provider: ProviderAzure, BaseURL: "not a url", APIKey: "k", Model: "dep"}, wantErr: true},
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

// TestAzureAPIVersionOrDefault covers the api-version fallback.
func TestAzureAPIVersionOrDefault(t *testing.T) {
	if got := (&Config{}).AzureAPIVersionOrDefault(); got != DefaultAzureAPIVersion {
		t.Fatalf("empty should default, got %q", got)
	}
	if got := (&Config{AzureAPIVersion: "2024-08-01-preview"}).AzureAPIVersionOrDefault(); got != "2024-08-01-preview" {
		t.Fatalf("explicit version not honoured, got %q", got)
	}
}

// TestAnthropicBaseURLDefault covers the default Anthropic origin.
func TestAnthropicBaseURLDefault(t *testing.T) {
	if got := (&Config{Provider: ProviderAnthropic}).AIBaseURLResolved(); got != "https://api.anthropic.com" {
		t.Fatalf("anthropic default base = %q", got)
	}
	if got := (&Config{Provider: ProviderAnthropic, BaseURL: "https://proxy.example/"}).AIBaseURLResolved(); got != "https://proxy.example" {
		t.Fatalf("anthropic custom base = %q", got)
	}
}

// TestProviderPresets sanity-checks the preset data drives the expected
// transports (OpenAI-compatible for the four proxies, azure/anthropic for the
// transport variants).
func TestProviderPresets(t *testing.T) {
	presets := ProviderPresets()
	byName := map[string]ProviderPreset{}
	for _, p := range presets {
		byName[p.Name] = p
	}
	for _, name := range []string{"OpenRouter", "GitHub Models", "Groq", "Together"} {
		p, ok := byName[name]
		if !ok {
			t.Fatalf("missing preset %q", name)
		}
		if p.Provider != ProviderOpenAICompatible {
			t.Fatalf("%s should reuse openai_compatible, got %q", name, p.Provider)
		}
		if p.BaseURL == "" {
			t.Fatalf("%s preset should carry a base URL", name)
		}
	}
	if byName["Azure OpenAI"].Provider != ProviderAzure {
		t.Fatalf("Azure OpenAI preset should select the azure transport")
	}
	if byName["Azure OpenAI"].APIVersion == "" {
		t.Fatalf("Azure preset should pre-fill an api-version")
	}
	if byName["Anthropic API"].Provider != ProviderAnthropic {
		t.Fatalf("Anthropic API preset should select the anthropic transport")
	}
	// Mutating the returned slice must not affect package state.
	presets[0].Name = "mutated"
	if ProviderPresets()[0].Name == "mutated" {
		t.Fatal("ProviderPresets must return a copy")
	}
}
