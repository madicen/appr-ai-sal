package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// 0.4 fix #7 (preserved through the F1 move): the Gemini API key must travel
// in the x-goog-api-key header, not the ?key= query string, so it never lands
// in URL / proxy / referrer logs.
func TestGeminiSendsKeyInHeaderNotQuery(t *testing.T) {
	const secret = "super-secret-gemini-key"
	var gotHeaderKey, gotRawQuery, gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaderKey = r.Header.Get("x-goog-api-key")
		gotRawQuery = r.URL.RawQuery
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{
		Provider: aiconfig.ProviderGemini,
		BaseURL:  srv.URL,
		Model:    "gemini-2.0-flash",
		APIKey:   secret,
	}
	res, err := (&geminiProvider{cfg: cfg}).Complete(context.Background(), Request{System: "sys", User: "user"})
	if err != nil {
		t.Fatalf("gemini Complete returned error: %v", err)
	}
	if res.Text != "ok" {
		t.Fatalf("unexpected completion output %q", res.Text)
	}
	if gotHeaderKey != secret {
		t.Fatalf("x-goog-api-key header = %q, want %q", gotHeaderKey, secret)
	}
	if strings.Contains(gotRawQuery, "key=") || strings.Contains(gotRawQuery, secret) {
		t.Fatalf("api key must not appear in the query string, got %q", gotRawQuery)
	}
	if strings.Contains(gotURL, secret) {
		t.Fatalf("api key must not appear anywhere in the URL, got %q", gotURL)
	}
}

// TestGeminiUsageParsed confirms usageMetadata is captured into Result.Usage.
func TestGeminiUsageParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":123,"candidatesTokenCount":45}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderGemini, BaseURL: srv.URL, Model: "gemini-2.0-flash", APIKey: "k"}
	res, err := (&geminiProvider{cfg: cfg}).Complete(context.Background(), Request{System: "sys", User: "user"})
	if err != nil {
		t.Fatalf("gemini Complete returned error: %v", err)
	}
	if res.Usage.InputTokens != 123 || res.Usage.OutputTokens != 45 {
		t.Fatalf("usage = %+v, want in=123 out=45", res.Usage)
	}
	if res.Model != "gemini-2.0-flash" {
		t.Fatalf("model = %q, want gemini-2.0-flash", res.Model)
	}
}

// TestOpenAIUsageParsed confirms the OpenAI-compatible `usage` block is
// captured into Result.Usage.
func TestOpenAIUsageParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"result text"}}],"usage":{"prompt_tokens":200,"completion_tokens":50}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen"}
	res, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "sys", User: "user"})
	if err != nil {
		t.Fatalf("openai Complete returned error: %v", err)
	}
	if res.Text != "result text" {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Usage.InputTokens != 200 || res.Usage.OutputTokens != 50 {
		t.Fatalf("usage = %+v, want in=200 out=50", res.Usage)
	}
}

// TestOpenAIHTTPErrorIsAPIHTTPError confirms non-2xx becomes a typed,
// retry-classifiable error carrying the status.
func TestOpenAIHTTPErrorIsAPIHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen"}
	_, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"})
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if !IsRetryableCompleteError(err) {
		t.Fatalf("429 should be retryable, got %v", err)
	}
}

func TestProviderForRegistry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider aiconfig.Provider
		wantName string
		wantErr  bool
	}{
		{aiconfig.ProviderClaude, "claude", false},
		{aiconfig.ProviderGemini, "gemini", false},
		{aiconfig.ProviderOllama, "ollama", false},
		{aiconfig.ProviderOpenAICompatible, "openai_compatible", false},
		{aiconfig.Provider("bogus"), "", true},
	}
	for _, tc := range cases {
		cfg := &aiconfig.Config{Provider: tc.provider}
		p, err := ProviderFor(cfg)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("provider %q: expected error", tc.provider)
			}
			continue
		}
		if err != nil {
			t.Fatalf("provider %q: unexpected error %v", tc.provider, err)
		}
		if p.Name() != tc.wantName {
			t.Fatalf("provider %q: name = %q, want %q", tc.provider, p.Name(), tc.wantName)
		}
	}
}

func TestCapabilitiesPerProvider(t *testing.T) {
	t.Parallel()
	// Only the Claude subprocess exposes repo tools.
	if !CapabilitiesFor(&aiconfig.Config{Provider: aiconfig.ProviderClaude}).RepoTools {
		t.Fatal("claude should have RepoTools=true")
	}
	for _, p := range []aiconfig.Provider{aiconfig.ProviderGemini, aiconfig.ProviderOllama, aiconfig.ProviderOpenAICompatible} {
		caps := CapabilitiesFor(&aiconfig.Config{Provider: p})
		if caps.RepoTools {
			t.Fatalf("%s should have RepoTools=false", p)
		}
		// R5 enabled native JSON mode on the HTTP providers (json_object /
		// responseMimeType); Streaming is still off.
		if !caps.NativeJSON {
			t.Fatalf("%s should have NativeJSON=true after R5, got %+v", p, caps)
		}
		if caps.Streaming {
			t.Fatalf("%s Streaming should default to false, got %+v", p, caps)
		}
	}
	// Claude keeps NativeJSON off (it goes through the CLI subprocess).
	if CapabilitiesFor(&aiconfig.Config{Provider: aiconfig.ProviderClaude}).NativeJSON {
		t.Fatal("claude NativeJSON should stay false")
	}
	// Unknown providers report the zero value rather than panicking.
	if CapabilitiesFor(&aiconfig.Config{Provider: aiconfig.Provider("bogus")}) != (Capabilities{}) {
		t.Fatal("unknown provider should report zero-value capabilities")
	}
}
