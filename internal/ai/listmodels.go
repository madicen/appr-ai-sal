package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// ModelInfo describes one model a provider can serve, as returned by
// ListModels. Only ID is guaranteed populated; the rest are best-effort
// (providers surface different metadata).
type ModelInfo struct {
	// ID is the model identifier to put in a profile's Model field.
	ID string
	// Name is a human-facing label when the provider gives one distinct from
	// the ID; otherwise it mirrors ID.
	Name string
}

// ListModels returns the models the configured provider can serve so model
// ids do not have to be hand-typed. It is a best-effort, fail-open capability:
// callers should fall back to manual entry when it returns an error (offline,
// unsupported provider, auth required, etc.).
//
//   - Ollama:            GET {base}/api/tags
//   - OpenAI-compatible: GET {base}/models
//   - Gemini:            GET {base}/v1beta/models
//   - Anthropic:         GET {base}/v1/models  (x-api-key + anthropic-version)
//   - Azure / Claude CLI: unsupported (returns an error)
func ListModels(ctx context.Context, cfg *aiconfig.Config) ([]ModelInfo, error) {
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}
	switch cfg.Provider {
	case aiconfig.ProviderOllama:
		return listOllamaModels(ctx, cfg)
	case aiconfig.ProviderOpenAICompatible:
		return listOpenAIModels(ctx, cfg)
	case aiconfig.ProviderGemini:
		return listGeminiModels(ctx, cfg)
	case aiconfig.ProviderAnthropic:
		return listAnthropicModels(ctx, cfg)
	default:
		return nil, fmt.Errorf("model listing is not supported for provider %q", cfg.Provider)
	}
}

// getJSON performs a GET to url with the given headers and unmarshals a 2xx
// body into out, returning the shared APIHTTPError taxonomy on non-2xx.
func getJSON(ctx context.Context, cfg *aiconfig.Config, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClientFor(cfg).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIHTTPError{
			Provider:   string(cfg.Provider),
			Status:     resp.StatusCode,
			Body:       string(body),
			RetryAfter: httpRetryAfter(resp, body),
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse model list JSON: %w", err)
	}
	return nil
}

func listOllamaModels(ctx context.Context, cfg *aiconfig.Config) ([]ModelInfo, error) {
	// Ollama's tag listing lives at the API root (/api/tags), not under the
	// OpenAI-compat /v1 prefix AIBaseURLResolved returns.
	base := strings.TrimRight(cfg.AIBaseURLResolved(), "/")
	base = strings.TrimSuffix(base, "/v1")
	var env struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := getJSON(ctx, cfg, base+"/api/tags", nil, &env); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(env.Models))
	for _, m := range env.Models {
		id := strings.TrimSpace(m.Model)
		if id == "" {
			id = strings.TrimSpace(m.Name)
		}
		if id == "" {
			continue
		}
		out = append(out, ModelInfo{ID: id, Name: strings.TrimSpace(m.Name)})
	}
	return sortModels(out), nil
}

func listOpenAIModels(ctx context.Context, cfg *aiconfig.Config) ([]ModelInfo, error) {
	base := strings.TrimRight(cfg.AIBaseURLResolved(), "/")
	if base == "" {
		return nil, fmt.Errorf("openai_compatible requires a base URL to list models")
	}
	headers := map[string]string{"Authorization": "Bearer " + cfg.BearerForHTTP()}
	var env struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg, base+"/models", headers, &env); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(env.Data))
	for _, m := range env.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		out = append(out, ModelInfo{ID: id, Name: id})
	}
	return sortModels(out), nil
}

func listGeminiModels(ctx context.Context, cfg *aiconfig.Config) ([]ModelInfo, error) {
	key := strings.TrimSpace(cfg.EffectiveAPIKey())
	if key == "" {
		return nil, fmt.Errorf("gemini requires an API key to list models")
	}
	base := strings.TrimRight(cfg.AIBaseURLResolved(), "/")
	headers := map[string]string{"x-goog-api-key": key}
	var env struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := getJSON(ctx, cfg, base+"/v1beta/models", headers, &env); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(env.Models))
	for _, m := range env.Models {
		// Gemini returns names like "models/gemini-2.0-flash"; strip the
		// prefix so the id is directly usable as a Model value.
		id := strings.TrimSpace(strings.TrimPrefix(m.Name, "models/"))
		if id == "" {
			continue
		}
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			name = id
		}
		out = append(out, ModelInfo{ID: id, Name: name})
	}
	return sortModels(out), nil
}

func listAnthropicModels(ctx context.Context, cfg *aiconfig.Config) ([]ModelInfo, error) {
	key := strings.TrimSpace(cfg.EffectiveAPIKey())
	if key == "" {
		return nil, fmt.Errorf("anthropic requires an API key to list models")
	}
	base := strings.TrimRight(cfg.AIBaseURLResolved(), "/")
	headers := map[string]string{
		"x-api-key":         key,
		"anthropic-version": anthropicVersion,
	}
	var env struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg, base+"/v1/models", headers, &env); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(env.Data))
	for _, m := range env.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			name = id
		}
		out = append(out, ModelInfo{ID: id, Name: name})
	}
	return sortModels(out), nil
}

// sortModels returns models sorted by ID for stable, predictable pickers.
func sortModels(in []ModelInfo) []ModelInfo {
	sort.Slice(in, func(i, j int) bool { return in[i].ID < in[j].ID })
	return in
}
