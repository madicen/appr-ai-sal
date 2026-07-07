package review

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// 0.4 fix #7: the Gemini API key must travel in the x-goog-api-key header, not
// the ?key= query string, so it never lands in URL / proxy / referrer logs.
func TestCompleteGeminiSendsKeyInHeaderNotQuery(t *testing.T) {
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
	out, err := completeGemini(context.Background(), cfg, "sys", "user")
	if err != nil {
		t.Fatalf("completeGemini returned error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected completion output %q", out)
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
