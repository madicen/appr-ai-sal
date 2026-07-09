package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

func modelIDs(models []ModelInfo) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}

func containsID(models []ModelInfo, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// TestListModelsOllama parses /api/tags at the API root (not the /v1 prefix).
func TestListModelsOllama(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:7b","model":"qwen2.5-coder:7b"},{"name":"llama3.2:latest","model":"llama3.2:latest"}]}`))
	}))
	defer srv.Close()

	// The base URL includes the OpenAI-compat /v1 suffix, which must be
	// stripped before /api/tags.
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOllama, BaseURL: srv.URL + "/v1"}
	models, err := ListModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/api/tags" {
		t.Fatalf("path = %q, want /api/tags", gotPath)
	}
	if !containsID(models, "qwen2.5-coder:7b") || !containsID(models, "llama3.2:latest") {
		t.Fatalf("unexpected models: %v", modelIDs(models))
	}
}

// TestListModelsOpenAICompatible parses GET /models with the data[] shape.
func TestListModelsOpenAICompatible(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"},{"id":"gpt-4o"}]}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, APIKey: "sk-x"}
	models, err := ListModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/models" {
		t.Fatalf("path = %q, want /models", gotPath)
	}
	if gotAuth != "Bearer sk-x" {
		t.Fatalf("auth = %q, want Bearer sk-x", gotAuth)
	}
	if !containsID(models, "gpt-4o") || !containsID(models, "gpt-4o-mini") {
		t.Fatalf("unexpected models: %v", modelIDs(models))
	}
}

// TestListModelsGemini parses /v1beta/models and strips the "models/" prefix.
func TestListModelsGemini(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-2.0-flash","displayName":"Gemini 2.0 Flash"}]}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderGemini, BaseURL: srv.URL, APIKey: "gk"}
	models, err := ListModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/v1beta/models" {
		t.Fatalf("path = %q, want /v1beta/models", gotPath)
	}
	if gotKey != "gk" {
		t.Fatalf("x-goog-api-key = %q, want gk", gotKey)
	}
	if !containsID(models, "gemini-2.0-flash") {
		t.Fatalf("expected gemini-2.0-flash (prefix stripped), got %v", modelIDs(models))
	}
}

// TestListModelsAnthropic parses /v1/models with the data[] shape.
func TestListModelsAnthropic(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-20250514","display_name":"Claude Sonnet 4"}]}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, APIKey: "ak"}
	models, err := ListModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("path = %q, want /v1/models", gotPath)
	}
	if gotKey != "ak" || gotVersion != anthropicVersion {
		t.Fatalf("headers key=%q version=%q", gotKey, gotVersion)
	}
	if !containsID(models, "claude-sonnet-4-20250514") {
		t.Fatalf("unexpected models: %v", modelIDs(models))
	}
}

// TestListModelsFailOpen proves an unsupported provider (claude CLI) and an
// HTTP error both surface an error the caller can fall back from (fail-open).
func TestListModelsFailOpen(t *testing.T) {
	// Unsupported provider: the subprocess claude backend has no listing API.
	if _, err := ListModels(context.Background(), &aiconfig.Config{Provider: aiconfig.ProviderClaude}); err == nil {
		t.Fatal("expected an error for the unsupported claude CLI provider")
	}

	// HTTP failure: a 500 from the endpoint surfaces an error, not a panic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, APIKey: "k"}
	if _, err := ListModels(context.Background(), cfg); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
}
