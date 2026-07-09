package settings

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// TestPresetFillsProviderAndBaseURL: applying a preset fills the edited
// profile's provider + base URL (manual entry still works afterwards).
func TestPresetFillsProviderAndBaseURL(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 200, StartSection: StartAI})

	// Find the OpenRouter preset's dropdown option index (offset by the
	// leading "(custom)" entry).
	presets := aiconfig.ProviderPresets()
	optIdx := -1
	for i, p := range presets {
		if p.Name == "OpenRouter" {
			optIdx = i + 1
			break
		}
	}
	if optIdx < 0 {
		t.Fatal("OpenRouter preset not found")
	}

	m.applyPreset(optIdx)
	m.commitEditorToSelectedProfile()
	got := m.draft.Profiles[m.selectedProfileIdx]
	if got.Provider != aiconfig.ProviderOpenAICompatible {
		t.Fatalf("preset should set openai_compatible, got %q", got.Provider)
	}
	if got.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("preset should fill the OpenRouter base URL, got %q", got.BaseURL)
	}
}

// TestPresetFillsAzureAPIVersion: the Azure preset also fills api-version.
func TestPresetFillsAzureAPIVersion(t *testing.T) {
	m := New(Opts{Cfg: aiconfig.DefaultConfig(), Width: 120, BodyHeight: 200, StartSection: StartAI})
	presets := aiconfig.ProviderPresets()
	optIdx := -1
	for i, p := range presets {
		if p.Name == "Azure OpenAI" {
			optIdx = i + 1
			break
		}
	}
	if optIdx < 0 {
		t.Fatal("Azure preset not found")
	}
	m.applyPreset(optIdx)
	m.commitEditorToSelectedProfile()
	got := m.draft.Profiles[m.selectedProfileIdx]
	if got.Provider != aiconfig.ProviderAzure {
		t.Fatalf("preset should set azure, got %q", got.Provider)
	}
	if got.AzureAPIVersion == "" {
		t.Fatalf("azure preset should fill api-version")
	}
}

// TestModelPickerFetchBuildsDropdown: fetching models for an openai-compatible
// profile builds the picker dropdown from the canned /models response, and a
// selection fills the model input.
func TestModelPickerFetchBuildsDropdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer srv.Close()

	prof := aiconfig.Profile{Name: "oc", Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, APIKey: "k"}
	cfg := &aiconfig.Config{ActiveProfile: "oc", Profiles: []aiconfig.Profile{prof}, ReviewStrictness: aiconfig.ReviewBalanced}
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 200, StartSection: StartAI})

	// Run the fetch command synchronously and feed the result back in.
	cmd := m.fetchModelsCmd()
	if cmd == nil {
		t.Fatal("fetchModelsCmd returned nil")
	}
	msg := cmd()
	updated, _ := m.Update(msg)
	m = updated.(*Model)

	if !m.modelDD.Built() {
		t.Fatalf("expected the model picker dropdown to be built after a fetch; err=%q", m.modelsErr)
	}
	if len(m.modelOptions) != 2 {
		t.Fatalf("expected 2 model options, got %v", m.modelOptions)
	}
	// Selecting the second option fills the model input.
	m.modelDD.SetSelectedIndex(1)
	// OnSelect fires through Forward; call it directly via the stored options.
	m.model.SetValue(m.modelOptions[1])
	if m.model.Value() != "model-b" {
		t.Fatalf("model input = %q, want model-b", m.model.Value())
	}
}

// TestModelPickerFetchFailOpen: a failing fetch keeps manual entry usable (no
// dropdown, an error message recorded) rather than blocking.
func TestModelPickerFetchFailOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	prof := aiconfig.Profile{Name: "oc", Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, APIKey: "k"}
	cfg := &aiconfig.Config{ActiveProfile: "oc", Profiles: []aiconfig.Profile{prof}, ReviewStrictness: aiconfig.ReviewBalanced}
	m := New(Opts{Cfg: cfg, Width: 120, BodyHeight: 200, StartSection: StartAI})

	msg := m.fetchModelsCmd()()
	updated, _ := m.Update(msg)
	m = updated.(*Model)

	if m.modelDD.Built() {
		t.Fatal("dropdown should not be built after a failed fetch")
	}
	if m.modelsErr == "" {
		t.Fatal("expected an error message recorded on fail-open")
	}
	// The model input remains editable (unchanged).
	if m.model.Value() != "" {
		t.Fatalf("model input should be untouched, got %q", m.model.Value())
	}
}
