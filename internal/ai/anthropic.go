package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// anthropicProvider talks directly to the Anthropic Messages API
// (POST /v1/messages) over HTTP, authenticating with the x-api-key +
// anthropic-version headers. Unlike claudeProvider (which shells out to the
// `claude` CLI and can read the repo worktree), this backend needs no local
// CLI, so it works headless / in CI — at the cost of reviewing the diff blind
// (no repo tools, so B5 context expansion applies, which is correct).
type anthropicProvider struct {
	cfg *aiconfig.Config
}

// anthropicVersion is the required anthropic-version header value.
const anthropicVersion = "2023-06-01"

// anthropicDefaultMaxTokens bounds the completion length. The Messages API
// requires max_tokens; this is a generous, broadly-supported default for
// structured review output.
const anthropicDefaultMaxTokens = 8192

// anthropicJSONToolName is the synthetic tool the provider forces the model to
// call to guarantee well-formed JSON output (Anthropic's structured-output
// approach): the tool's input_schema constrains the shape and tool_choice
// forces it, so the JSON arrives as the tool call's `input` object.
const anthropicJSONToolName = "emit_json"

func (p *anthropicProvider) Name() string { return string(aiconfig.ProviderAnthropic) }

func (p *anthropicProvider) Capabilities() Capabilities {
	// The HTTP API reviews the diff blind — no repo tools (unlike the Claude
	// subprocess). It supports native JSON mode via tool-use forcing (R5).
	return Capabilities{NativeJSON: true}
}

func (p *anthropicProvider) Complete(ctx context.Context, req Request) (Result, error) {
	cfg := p.cfg
	key := strings.TrimSpace(cfg.EffectiveAPIKey())
	if key == "" {
		return Result{}, fmt.Errorf("anthropic requires an API key (set APPR_AI_SAL_AI_API_KEY or ai.json api_key/api_key_env/api_key_cmd)")
	}
	model := cfg.AIModelOrDefault()
	if model == "" {
		return Result{}, fmt.Errorf("model is required for anthropic (set APPR_AI_SAL_AI_MODEL, e.g. claude-sonnet-4-20250514)")
	}

	base := cfg.AIBaseURLResolved()
	endpoint := strings.TrimRight(base, "/") + "/v1/messages"

	body := map[string]any{
		"model":      model,
		"max_tokens": anthropicDefaultMaxTokens,
		"messages": []map[string]any{
			{"role": "user", "content": req.User},
		},
	}
	if s := strings.TrimSpace(req.System); s != "" {
		body["system"] = req.System
	}
	if req.wantsJSON() {
		// Tool-use JSON forcing: define a single tool whose input_schema is
		// the caller's schema (or a permissive object when none is supplied),
		// and force the model to call it via tool_choice. The response then
		// carries the JSON as the tool call's `input`. The llmjson salvage
		// ladder still parses the returned text as a fallback.
		schema := json.RawMessage(req.JSONSchema)
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		body["tools"] = []map[string]any{
			{
				"name":         anthropicJSONToolName,
				"description":  "Return the result as a single structured JSON object matching the schema.",
				"input_schema": schema,
			},
		}
		body["tool_choice"] = map[string]any{"type": "tool", "name": anthropicJSONToolName}
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
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := httpClientFor(cfg).Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("anthropic messages: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Reuse the shared APIHTTPError taxonomy (429/5xx/529 + Retry-After),
		// so the existing retry classification handles Anthropic overload
		// (HTTP 529) exactly like the other HTTP providers.
		return Result{}, &APIHTTPError{
			Provider:   string(aiconfig.ProviderAnthropic),
			Status:     resp.StatusCode,
			Body:       string(respBody),
			RetryAfter: httpRetryAfter(resp, respBody),
		}
	}

	var envelope struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return Result{}, fmt.Errorf("parse anthropic JSON: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return Result{}, fmt.Errorf("anthropic API error: %s", envelope.Error.Message)
	}

	out := extractAnthropicText(envelope.Content)
	if out == "" {
		return Result{}, fmt.Errorf("anthropic: empty content in response")
	}
	respModel := envelope.Model
	if respModel == "" {
		respModel = model
	}
	return Result{
		Text:  out,
		Model: respModel,
		Usage: Usage{
			InputTokens:  envelope.Usage.InputTokens,
			OutputTokens: envelope.Usage.OutputTokens,
		},
	}, nil
}

// extractAnthropicText returns the assistant output from a Messages response.
// A forced tool call (tool_choice) yields a tool_use block whose `input` is
// the JSON object we want; when present it wins. Otherwise the text blocks are
// concatenated (the non-JSON / plain path).
func extractAnthropicText(content []struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}) string {
	for _, block := range content {
		if block.Type == "tool_use" && len(block.Input) > 0 {
			return strings.TrimSpace(string(block.Input))
		}
	}
	var b strings.Builder
	for _, block := range content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(b.String())
}
