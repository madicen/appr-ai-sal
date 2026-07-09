package aiconfig

// ProviderPreset is a named starting point for a profile: it fills the
// provider, base URL, and (for Azure) api-version so common endpoints do not
// have to be typed by hand. Presets are pure data — the settings/profile
// editor offers them and copies their fields onto the edited profile, after
// which every field remains freely editable (manual base-URL entry keeps
// working). Presets never carry a secret; the user still supplies the key.
type ProviderPreset struct {
	// Name is the human-facing label shown in the preset picker.
	Name string
	// Provider is the transport the preset selects.
	Provider Provider
	// BaseURL is the endpoint the preset fills. For Azure it is empty because
	// the resource endpoint is account-specific and must be entered by hand.
	BaseURL string
	// APIVersion pre-fills the Azure api-version (ignored for other providers).
	APIVersion string
	// Notes is a one-line hint (auth style, where to get a key, caveats).
	Notes string
}

// providerPresets is the built-in preset list. OpenRouter, GitHub Models,
// Groq, and Together are all OpenAI-compatible, so they reuse the
// openai_compatible transport verbatim (just a base URL + bearer key). Azure
// OpenAI is a real transport variant (api-key header + deployment URL scheme).
var providerPresets = []ProviderPreset{
	{
		Name:     "OpenRouter",
		Provider: ProviderOpenAICompatible,
		BaseURL:  "https://openrouter.ai/api/v1",
		Notes:    "OpenAI-compatible; bearer key from openrouter.ai/keys",
	},
	{
		Name:     "GitHub Models",
		Provider: ProviderOpenAICompatible,
		BaseURL:  "https://models.github.ai/inference",
		Notes:    "OpenAI-compatible; use a GitHub PAT as the bearer key",
	},
	{
		Name:     "Groq",
		Provider: ProviderOpenAICompatible,
		BaseURL:  "https://api.groq.com/openai/v1",
		Notes:    "OpenAI-compatible; bearer key from console.groq.com",
	},
	{
		Name:     "Together",
		Provider: ProviderOpenAICompatible,
		BaseURL:  "https://api.together.xyz/v1",
		Notes:    "OpenAI-compatible; bearer key from api.together.ai",
	},
	{
		Name:       "Azure OpenAI",
		Provider:   ProviderAzure,
		BaseURL:    "",
		APIVersion: DefaultAzureAPIVersion,
		Notes:      "set base URL to https://<resource>.openai.azure.com and model to your deployment; api-key header",
	},
	{
		Name:     "Anthropic API",
		Provider: ProviderAnthropic,
		BaseURL:  "https://api.anthropic.com",
		Notes:    "direct /v1/messages; x-api-key from console.anthropic.com (no CLI needed)",
	},
}

// ProviderPresets returns a copy of the built-in provider presets, safe for
// the caller to read without mutating package state.
func ProviderPresets() []ProviderPreset {
	out := make([]ProviderPreset, len(providerPresets))
	copy(out, providerPresets)
	return out
}
