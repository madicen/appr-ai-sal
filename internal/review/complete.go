package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// Complete runs inference for the given prompts using cfg.Provider transport.
// worktree is only used for the Claude subprocess (cwd and --add-dir).
// Transient failures (timeouts, rate limits, HTTP 429/503, etc.) use exponential
// backoff with jitter, optional Retry-After, and higher floors for quota-style
// errors — see aiconfig.Config retry fields and package review retry helpers.
func Complete(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt, worktree string) (string, error) {
	if cfg == nil {
		cfg = aiconfig.DefaultConfig()
	}
	return completeWithRetry(ctx, cfg, func(ctx context.Context) (string, error) {
		return completeOnce(ctx, cfg, systemPrompt, userPrompt, worktree)
	})
}

func completeOnce(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt, worktree string) (string, error) {
	switch cfg.Provider {
	case aiconfig.ProviderClaude:
		return runClaude(ctx, cfg, systemPrompt, userPrompt, worktree)
	case aiconfig.ProviderOllama, aiconfig.ProviderOpenAICompatible:
		return completeOpenAIChat(ctx, cfg, systemPrompt, userPrompt)
	case aiconfig.ProviderGemini:
		return completeGemini(ctx, cfg, systemPrompt, userPrompt)
	default:
		return "", fmt.Errorf("unsupported AI provider %q", cfg.Provider)
	}
}

func httpClientFor(cfg *aiconfig.Config) *http.Client {
	sec := cfg.EffectiveTimeoutSec()
	return &http.Client{Timeout: time.Duration(sec) * time.Second}
}

func completeOpenAIChat(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt string) (string, error) {
	base := cfg.AIBaseURLResolved()
	if base == "" {
		return "", fmt.Errorf("openai_compatible requires a base URL (set APPR_AI_SAL_AI_BASE_URL or ai.json base_url)")
	}
	model := cfg.AIModelOrDefault()
	if model == "" {
		return "", fmt.Errorf("model is required for HTTP providers (set APPR_AI_SAL_AI_MODEL)")
	}

	endpoint := strings.TrimRight(base, "/") + "/chat/completions"
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.BearerForHTTP())

	resp, err := httpClientFor(cfg).Do(req)
	if err != nil {
		return "", fmt.Errorf("chat/completions: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIHTTPError{
			Provider:   string(cfg.Provider),
			Status:     resp.StatusCode,
			Body:       string(respBody),
			RetryAfter: httpRetryAfter(resp, respBody),
		}
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return "", fmt.Errorf("parse chat/completions JSON: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return "", fmt.Errorf("chat API error: %s", envelope.Error.Message)
	}
	if len(envelope.Choices) == 0 {
		return "", fmt.Errorf("chat/completions: empty choices")
	}
	out := strings.TrimSpace(envelope.Choices[0].Message.Content)
	if out == "" {
		return "", fmt.Errorf("chat/completions: empty message content")
	}
	return out, nil
}

func completeGemini(ctx context.Context, cfg *aiconfig.Config, systemPrompt, userPrompt string) (string, error) {
	key := strings.TrimSpace(cfg.EffectiveAPIKey())
	if key == "" {
		return "", fmt.Errorf("Gemini requires an API key (set APPR_AI_SAL_AI_API_KEY)")
	}
	model := cfg.AIModelOrDefault()
	if model == "" {
		return "", fmt.Errorf("model is required for Gemini (set APPR_AI_SAL_AI_MODEL, e.g. gemini-2.0-flash)")
	}

	base := cfg.AIBaseURLResolved()
	u, err := url.Parse(base + "/v1beta/models/" + url.PathEscape(model) + ":generateContent")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("key", key)
	u.RawQuery = q.Encode()

	reqBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": systemPrompt}},
		},
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]string{{"text": userPrompt}},
			},
		},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClientFor(cfg).Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini generateContent: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIHTTPError{
			Provider:   string(cfg.Provider),
			Status:     resp.StatusCode,
			Body:       string(respBody),
			RetryAfter: httpRetryAfter(resp, respBody),
		}
	}

	var envelope struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return "", fmt.Errorf("parse gemini JSON: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return "", fmt.Errorf("gemini API error: %s", envelope.Error.Message)
	}
	if len(envelope.Candidates) == 0 {
		return "", fmt.Errorf("gemini: empty candidates")
	}
	var b strings.Builder
	for _, p := range envelope.Candidates[0].Content.Parts {
		b.WriteString(p.Text)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("gemini: empty text in response")
	}
	return out, nil
}
