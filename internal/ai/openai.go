package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// openAIProvider talks to any OpenAI-compatible /chat/completions endpoint
// (Ollama and openai_compatible profiles).
type openAIProvider struct {
	cfg *aiconfig.Config
}

func (p *openAIProvider) Name() string { return string(p.cfg.Provider) }

func (p *openAIProvider) Capabilities() Capabilities {
	// HTTP providers review the diff blind — no repo tools. They do support
	// native JSON mode via response_format (R5).
	return Capabilities{NativeJSON: true}
}

func httpClientFor(cfg *aiconfig.Config) *http.Client {
	sec := cfg.EffectiveTimeoutSec()
	return &http.Client{Timeout: time.Duration(sec) * time.Second}
}

func (p *openAIProvider) Complete(ctx context.Context, req Request) (Result, error) {
	cfg := p.cfg
	base := cfg.AIBaseURLResolved()
	if base == "" {
		return Result{}, fmt.Errorf("openai_compatible requires a base URL (set APPR_AI_SAL_AI_BASE_URL or ai.json base_url)")
	}
	model := cfg.AIModelOrDefault()
	if model == "" {
		return Result{}, fmt.Errorf("model is required for HTTP providers (set APPR_AI_SAL_AI_MODEL)")
	}

	endpoint := strings.TrimRight(base, "/") + "/chat/completions"
	// OpenAI-compatible endpoints authenticate with a bearer token.
	setAuth := func(h http.Header) { h.Set("Authorization", "Bearer "+cfg.BearerForHTTP()) }
	return openAIChatComplete(ctx, cfg, endpoint, model, setAuth, req)
}

// openAIChatComplete performs one OpenAI-style /chat/completions call and
// parses the response. It is shared by openAIProvider (Ollama +
// openai_compatible, bearer auth) and azureProvider (Azure OpenAI, api-key
// header + deployment URL), which differ only in the endpoint URL and the
// auth header — the request body and response shape are identical.
func openAIChatComplete(ctx context.Context, cfg *aiconfig.Config, endpoint, model string, setAuth func(http.Header), req Request) (Result, error) {
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.User},
		},
	}
	if req.wantsJSON() {
		// Native JSON mode. response_format:{"type":"json_object"} is the
		// portable OpenAI-compatible choice and is honoured by Ollama's
		// OpenAI-compat /chat/completions endpoint. The salvage ladder
		// (llmjson.Parse) still runs on the response, since models can ignore
		// this and some proxies drop it.
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if setAuth != nil {
		setAuth(httpReq.Header)
	}

	resp, err := httpClientFor(cfg).Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("chat/completions: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, &APIHTTPError{
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
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return Result{}, fmt.Errorf("parse chat/completions JSON: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return Result{}, fmt.Errorf("chat API error: %s", envelope.Error.Message)
	}
	if len(envelope.Choices) == 0 {
		return Result{}, fmt.Errorf("chat/completions: empty choices")
	}
	out := strings.TrimSpace(envelope.Choices[0].Message.Content)
	if out == "" {
		return Result{}, fmt.Errorf("chat/completions: empty message content")
	}
	return Result{
		Text:  out,
		Model: model,
		Usage: Usage{
			InputTokens:  envelope.Usage.PromptTokens,
			OutputTokens: envelope.Usage.CompletionTokens,
		},
	}, nil
}
