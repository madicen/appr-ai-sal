package review

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// TestCompleteJSONWiresNativeJSONMode proves the R5 opt-in path end to end:
// completeJSON marks the context so the review.Complete shim sets
// Request.WantJSON, and the OpenAI-compatible provider then emits
// response_format on the wire — while the plain Complete shim does not. This is
// how JSON stages (specialists, PR agents, arbiter, witness, vibe-coach,
// repair) request native JSON mode without changing the ai.CompleteFunc
// signature.
func TestCompleteJSONWiresNativeJSONMode(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer srv.Close()

	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderOpenAICompatible
	cfg.BaseURL = srv.URL
	cfg.Model = "qwen"

	// JSON stage: response_format present.
	got = nil
	if _, err := completeJSON(context.Background(), cfg, "sys", "user", ""); err != nil {
		t.Fatalf("completeJSON: %v", err)
	}
	if _, ok := got["response_format"]; !ok {
		t.Fatalf("completeJSON should set response_format, body was %#v", got)
	}

	// Markdown/plain stage: response_format absent.
	got = nil
	if _, err := Complete(context.Background(), cfg, "sys", "user", ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := got["response_format"]; ok {
		t.Fatalf("plain Complete must not set response_format, body was %#v", got)
	}
}
