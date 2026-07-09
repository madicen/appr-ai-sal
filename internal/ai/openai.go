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
	// HTTP providers review the diff blind — no repo tools. They support
	// native JSON mode via response_format (R5) and SSE streaming (P6).
	return Capabilities{NativeJSON: true, Streaming: true}
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
	if req.Stream {
		return openAIChatStream(ctx, cfg, endpoint, model, setAuth, body)
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

// openAIChatStream performs the streaming (SSE) variant of an OpenAI-style
// /chat/completions call, accumulating choices[].delta.content into the final
// text and reading usage from the final chunk (requested via
// stream_options.include_usage so the streamed Result.Usage matches the
// whole-response path). Shared by openAIProvider and azureProvider exactly like
// openAIChatComplete.
func openAIChatStream(ctx context.Context, cfg *aiconfig.Config, endpoint, model string, setAuth func(http.Header), body map[string]any) (Result, error) {
	body["stream"] = true
	// Ask the endpoint to emit a final usage-only chunk; without this OpenAI
	// omits usage from a streamed response. Ollama/most OpenAI-compat proxies
	// honour it, and those that ignore it simply report zero usage (as before
	// streaming existed for them).
	body["stream_options"] = map[string]any{"include_usage": true}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if setAuth != nil {
		setAuth(httpReq.Header)
	}
	return runSSEStream(ctx, cfg, string(cfg.Provider), httpReq, model, openAISSEHandler)
}

// openAISSEHandler maps one OpenAI-style streaming chunk onto streamState.
func openAISSEHandler(_ /*event*/, data string, st *streamState) (string, bool, error) {
	if data == "[DONE]" {
		return "", true, nil
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		// A non-JSON keep-alive/comment line is not fatal; skip it.
		return "", false, nil
	}
	if chunk.Error != nil && chunk.Error.Message != "" {
		return "", false, fmt.Errorf("chat API error: %s", chunk.Error.Message)
	}
	var delta string
	if len(chunk.Choices) > 0 {
		delta = chunk.Choices[0].Delta.Content
	}
	if delta != "" {
		st.text.WriteString(delta)
	}
	if chunk.Usage != nil {
		st.usage.InputTokens = chunk.Usage.PromptTokens
		st.usage.OutputTokens = chunk.Usage.CompletionTokens
	}
	return delta, false, nil
}
