package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// TestAnthropicSuccessHeadersAndUsage covers the happy path: x-api-key +
// anthropic-version headers are sent, the text content is returned, and
// usage.input_tokens/output_tokens map into Result.Usage.
func TestAnthropicSuccessHeadersAndUsage(t *testing.T) {
	const secret = "sk-ant-secret"
	var gotKey, gotVersion, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"looks good"}],"model":"claude-x","usage":{"input_tokens":321,"output_tokens":45}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-sonnet-4", APIKey: secret}
	res, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "sys", User: "user"})
	if err != nil {
		t.Fatalf("anthropic Complete error: %v", err)
	}
	if res.Text != "looks good" {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Usage.InputTokens != 321 || res.Usage.OutputTokens != 45 {
		t.Fatalf("usage = %+v, want in=321 out=45", res.Usage)
	}
	if res.Model != "claude-x" {
		t.Fatalf("model = %q, want claude-x", res.Model)
	}
	if gotKey != secret {
		t.Fatalf("x-api-key = %q, want %q", gotKey, secret)
	}
	if gotVersion != anthropicVersion {
		t.Fatalf("anthropic-version = %q, want %q", gotVersion, anthropicVersion)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
}

// TestAnthropicToolUseJSONForcing proves that in JSON mode the provider forces
// a tool call (tool_choice) with the caller's schema as input_schema, and that
// the tool call's `input` object becomes the returned text.
func TestAnthropicToolUseJSONForcing(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		w.Header().Set("Content-Type", "application/json")
		// Respond with a tool_use block carrying the structured object.
		_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","name":"emit_json","input":{"findings":[],"verdict":"approve"}}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer srv.Close()

	schema := json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}}}`)
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k"}
	res, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", JSONSchema: schema})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	// The returned text is the tool call's input object as JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(res.Text), &parsed); err != nil {
		t.Fatalf("returned text is not JSON: %q (%v)", res.Text, err)
	}
	if parsed["verdict"] != "approve" {
		t.Fatalf("expected verdict=approve in tool output, got %v", parsed)
	}
	// The request must have forced the tool.
	tc, ok := reqBody["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "tool" || tc["name"] != anthropicJSONToolName {
		t.Fatalf("expected tool_choice forcing %q, got %v", anthropicJSONToolName, reqBody["tool_choice"])
	}
	tools, ok := reqBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected exactly one forced tool, got %v", reqBody["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != anthropicJSONToolName {
		t.Fatalf("tool name = %v", tool["name"])
	}
	if _, ok := tool["input_schema"].(map[string]any); !ok {
		t.Fatalf("expected the caller schema as input_schema, got %v", tool["input_schema"])
	}
}

// TestAnthropicJSONModeNoSchemaStillForcesTool proves WantJSON without a
// schema still forces a tool (with a permissive object schema) so R5 native
// JSON works for Anthropic even before per-agent schemas exist.
func TestAnthropicJSONModeNoSchemaStillForcesTool(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","name":"emit_json","input":{"ok":true}}],"usage":{}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k"}
	if _, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", WantJSON: true}); err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if _, ok := reqBody["tools"].([]any); !ok {
		t.Fatalf("expected a forced tool even without a schema, got %v", reqBody["tools"])
	}
}

// TestAnthropic529Retryable proves an HTTP 529 (overloaded) becomes a typed,
// retryable APIHTTPError so the shared retry policy backs off and retries.
func TestAnthropic529Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(529)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k"}
	_, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"})
	if err == nil {
		t.Fatal("expected error on 529")
	}
	if !IsRetryableCompleteError(err) {
		t.Fatalf("529 should be retryable, got %v", err)
	}
}

// TestAnthropicRetriesThroughProviderFor proves ProviderFor's retry wrapper
// retries a transient 529 and then succeeds — no CLI involved.
func TestAnthropicRetriesThroughProviderFor(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(529)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"recovered"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{
		Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k",
		RetryMaxAttempts: 3, RetryBaseMS: 1, RetryMaxMS: 5,
	}
	p, err := ProviderFor(cfg)
	if err != nil {
		t.Fatalf("ProviderFor: %v", err)
	}
	res, err := p.Complete(context.Background(), Request{System: "s", User: "u"})
	if err != nil {
		t.Fatalf("Complete after retry: %v", err)
	}
	if res.Text != "recovered" {
		t.Fatalf("text = %q, want recovered", res.Text)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls (1 fail + 1 success), got %d", got)
	}
}

// TestAnthropicKeyIndirection proves the R8 api_key_env indirection resolves
// the key (no explicit key on the profile).
func TestAnthropicKeyIndirection(t *testing.T) {
	const secret = "sk-from-env"
	t.Setenv("ANTHROPIC_TEST_KEY", secret)
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKeyEnv: "ANTHROPIC_TEST_KEY"}
	if _, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"}); err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if gotKey != secret {
		t.Fatalf("resolved key = %q, want %q", gotKey, secret)
	}
}

// TestAnthropicMissingKeyErrors proves a missing key fails clearly (not a nil
// deref) before any HTTP call.
func TestAnthropicMissingKeyErrors(t *testing.T) {
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, Model: "claude-x"}
	_, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected a missing-key error, got %v", err)
	}
}
