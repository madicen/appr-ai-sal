package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// TestAzureAPIKeyHeaderAndDeploymentURL proves Azure authenticates with the
// api-key header (not Authorization: Bearer) and addresses the deployment URL
// scheme with the api-version query parameter.
func TestAzureAPIKeyHeaderAndDeploymentURL(t *testing.T) {
	const secret = "azure-secret"
	var gotAPIKey, gotAuth, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("api-key")
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"azure reply"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{
		Provider:        aiconfig.ProviderAzure,
		BaseURL:         srv.URL,
		Model:           "my-deployment",
		APIKey:          secret,
		AzureAPIVersion: "2024-08-01-preview",
	}
	res, err := (&azureProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"})
	if err != nil {
		t.Fatalf("azure Complete error: %v", err)
	}
	if res.Text != "azure reply" {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Usage.InputTokens != 11 || res.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want in=11 out=7", res.Usage)
	}
	if gotAPIKey != secret {
		t.Fatalf("api-key header = %q, want %q", gotAPIKey, secret)
	}
	if gotAuth != "" {
		t.Fatalf("Azure must not send Authorization: Bearer, got %q", gotAuth)
	}
	if gotPath != "/openai/deployments/my-deployment/chat/completions" {
		t.Fatalf("path = %q, want deployment-scoped chat/completions", gotPath)
	}
	if !strings.Contains(gotQuery, "api-version=2024-08-01-preview") {
		t.Fatalf("query = %q, want api-version=2024-08-01-preview", gotQuery)
	}
}

// TestAzureDefaultAPIVersion proves an empty api-version falls back to the
// stable default.
func TestAzureDefaultAPIVersion(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	}))
	defer srv.Close()

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAzure, BaseURL: srv.URL, Model: "dep", APIKey: "k"}
	if _, err := (&azureProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u"}); err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if !strings.Contains(gotQuery, "api-version="+aiconfig.DefaultAzureAPIVersion) {
		t.Fatalf("query = %q, want default api-version %s", gotQuery, aiconfig.DefaultAzureAPIVersion)
	}
}

// TestAzureRegisteredInProviderFor proves the registry builds an azure
// provider and reports NativeJSON capability with no repo tools.
func TestAzureAndAnthropicRegistered(t *testing.T) {
	for _, tc := range []struct {
		prov aiconfig.Provider
		name string
	}{
		{aiconfig.ProviderAzure, "azure"},
		{aiconfig.ProviderAnthropic, "anthropic"},
	} {
		p, err := ProviderFor(&aiconfig.Config{Provider: tc.prov})
		if err != nil {
			t.Fatalf("ProviderFor(%s): %v", tc.prov, err)
		}
		if p.Name() != tc.name {
			t.Fatalf("name = %q, want %q", p.Name(), tc.name)
		}
		caps := p.Capabilities()
		if caps.RepoTools {
			t.Fatalf("%s should not have repo tools", tc.prov)
		}
		if !caps.NativeJSON {
			t.Fatalf("%s should support native JSON mode", tc.prov)
		}
	}
}
