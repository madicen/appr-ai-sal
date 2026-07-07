package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// captureBody spins up an httptest server that records the request body and
// replies with a fixed, provider-appropriate success envelope.
func captureBody(t *testing.T, reply string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

const openAIReply = `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`
const geminiReply = `{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}]}`

// TestOpenAIJSONModeSetsResponseFormat asserts that a JSON-mode request adds
// response_format:{"type":"json_object"} to the OpenAI-compatible body, and a
// plain request omits it entirely.
func TestOpenAIJSONModeSetsResponseFormat(t *testing.T) {
	t.Run("wantJSON", func(t *testing.T) {
		srv, got := captureBody(t, openAIReply)
		cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen"}
		if _, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", WantJSON: true}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		rf, ok := (*got)["response_format"].(map[string]any)
		if !ok {
			t.Fatalf("response_format missing or wrong type: %#v", (*got)["response_format"])
		}
		if rf["type"] != "json_object" {
			t.Fatalf("response_format.type = %v, want json_object", rf["type"])
		}
	})

	t.Run("schemaImpliesJSON", func(t *testing.T) {
		srv, got := captureBody(t, openAIReply)
		cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen"}
		req := Request{System: "s", User: "u", JSONSchema: json.RawMessage(`{"type":"object"}`)}
		if _, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), req); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, ok := (*got)["response_format"]; !ok {
			t.Fatal("a non-empty JSONSchema should imply JSON mode (response_format) on OpenAI")
		}
	})

	t.Run("plainOmitsResponseFormat", func(t *testing.T) {
		srv, got := captureBody(t, openAIReply)
		cfg := &aiconfig.Config{Provider: aiconfig.ProviderOllama, BaseURL: srv.URL, Model: "qwen"}
		if _, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, ok := (*got)["response_format"]; ok {
			t.Fatalf("plain request must not set response_format, got %#v", (*got)["response_format"])
		}
	})
}

// TestGeminiJSONModeSetsGenerationConfig asserts responseMimeType is set for a
// JSON-mode request, responseSchema is added when a schema is supplied, and a
// plain request omits generationConfig entirely.
func TestGeminiJSONModeSetsGenerationConfig(t *testing.T) {
	t.Run("wantJSONNoSchema", func(t *testing.T) {
		srv, got := captureBody(t, geminiReply)
		cfg := &aiconfig.Config{Provider: aiconfig.ProviderGemini, BaseURL: srv.URL, Model: "gemini-2.0-flash", APIKey: "k"}
		if _, err := (&geminiProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", WantJSON: true}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		gc, ok := (*got)["generationConfig"].(map[string]any)
		if !ok {
			t.Fatalf("generationConfig missing: %#v", (*got)["generationConfig"])
		}
		if gc["responseMimeType"] != "application/json" {
			t.Fatalf("responseMimeType = %v, want application/json", gc["responseMimeType"])
		}
		if _, ok := gc["responseSchema"]; ok {
			t.Fatalf("responseSchema must be absent without a schema, got %#v", gc["responseSchema"])
		}
	})

	t.Run("withSchema", func(t *testing.T) {
		srv, got := captureBody(t, geminiReply)
		cfg := &aiconfig.Config{Provider: aiconfig.ProviderGemini, BaseURL: srv.URL, Model: "gemini-2.0-flash", APIKey: "k"}
		schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`)
		if _, err := (&geminiProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", JSONSchema: schema}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		gc, ok := (*got)["generationConfig"].(map[string]any)
		if !ok {
			t.Fatalf("generationConfig missing: %#v", (*got)["generationConfig"])
		}
		if gc["responseMimeType"] != "application/json" {
			t.Fatalf("responseMimeType = %v, want application/json", gc["responseMimeType"])
		}
		rs, ok := gc["responseSchema"].(map[string]any)
		if !ok {
			t.Fatalf("responseSchema missing or wrong type: %#v", gc["responseSchema"])
		}
		if rs["type"] != "object" {
			t.Fatalf("responseSchema.type = %v, want object", rs["type"])
		}
	})

	t.Run("plainOmitsGenerationConfig", func(t *testing.T) {
		srv, got := captureBody(t, geminiReply)
		cfg := &aiconfig.Config{Provider: aiconfig.ProviderGemini, BaseURL: srv.URL, Model: "gemini-2.0-flash", APIKey: "k"}
		if _, err := (&geminiProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, ok := (*got)["generationConfig"]; ok {
			t.Fatalf("plain request must not set generationConfig, got %#v", (*got)["generationConfig"])
		}
	})
}

// TestJSONModeContext round-trips the WithJSONMode context signal that the
// review.Complete shim uses to translate a stage's JSON intent into
// Request.WantJSON.
func TestJSONModeContext(t *testing.T) {
	if JSONModeFromContext(context.Background()) {
		t.Fatal("plain context should not report JSON mode")
	}
	if !JSONModeFromContext(WithJSONMode(context.Background())) {
		t.Fatal("WithJSONMode context should report JSON mode")
	}
	//nolint:staticcheck // exercising the nil-context guard deliberately.
	if JSONModeFromContext(nil) {
		t.Fatal("nil context should not report JSON mode")
	}
}
