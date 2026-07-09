package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// azureProvider talks to Azure OpenAI. The request/response bodies are
// OpenAI-compatible (so it shares openAIChatComplete), but the transport is a
// real variant: it authenticates with an `api-key` header (not
// Authorization: Bearer) and addresses a deployment-scoped URL of the form
//
//	{endpoint}/openai/deployments/{deployment}/chat/completions?api-version=…
//
// where {deployment} is the profile's Model (Azure addresses deployments, not
// model ids) and the api-version comes from the profile (or a stable default).
type azureProvider struct {
	cfg *aiconfig.Config
}

func (p *azureProvider) Name() string { return string(aiconfig.ProviderAzure) }

func (p *azureProvider) Capabilities() Capabilities {
	// Same as the other HTTP providers: no repo tools, native JSON mode, SSE
	// streaming (P6).
	return Capabilities{NativeJSON: true, Streaming: true}
}

func (p *azureProvider) Complete(ctx context.Context, req Request) (Result, error) {
	cfg := p.cfg
	base := cfg.AIBaseURLResolved()
	if base == "" {
		return Result{}, fmt.Errorf("azure requires the resource endpoint as the base URL (e.g. https://<resource>.openai.azure.com)")
	}
	deployment := cfg.AIModelOrDefault()
	if deployment == "" {
		return Result{}, fmt.Errorf("azure requires a deployment name (set the model field to your Azure deployment)")
	}
	key := strings.TrimSpace(cfg.EffectiveAPIKey())
	if key == "" {
		return Result{}, fmt.Errorf("azure requires an API key (set APPR_AI_SAL_AI_API_KEY or ai.json api_key/api_key_env/api_key_cmd)")
	}

	endpoint := strings.TrimRight(base, "/") +
		"/openai/deployments/" + url.PathEscape(deployment) +
		"/chat/completions?api-version=" + url.QueryEscape(cfg.AzureAPIVersionOrDefault())

	// Azure authenticates with the api-key header, not Authorization: Bearer.
	setAuth := func(h http.Header) { h.Set("api-key", key) }
	return openAIChatComplete(ctx, cfg, endpoint, deployment, setAuth, req)
}
