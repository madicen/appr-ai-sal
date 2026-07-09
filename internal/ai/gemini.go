package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// geminiProvider talks to the Gemini generateContent HTTP API.
type geminiProvider struct {
	cfg *aiconfig.Config
}

func (p *geminiProvider) Name() string { return string(aiconfig.ProviderGemini) }

func (p *geminiProvider) Capabilities() Capabilities {
	// HTTP providers review the diff blind — no repo tools. Gemini supports
	// native JSON mode via generationConfig.responseMimeType (+ responseSchema
	// when a schema is supplied) (R5) and SSE streaming via
	// streamGenerateContent (P6).
	return Capabilities{NativeJSON: true, Streaming: true}
}

func (p *geminiProvider) Complete(ctx context.Context, req Request) (Result, error) {
	cfg := p.cfg
	key := strings.TrimSpace(cfg.EffectiveAPIKey())
	if key == "" {
		return Result{}, fmt.Errorf("Gemini requires an API key (set APPR_AI_SAL_AI_API_KEY)")
	}
	model := cfg.AIModelOrDefault()
	if model == "" {
		return Result{}, fmt.Errorf("model is required for Gemini (set APPR_AI_SAL_AI_MODEL, e.g. gemini-2.0-flash)")
	}

	base := cfg.AIBaseURLResolved()
	u, err := url.Parse(base + "/v1beta/models/" + url.PathEscape(model) + ":generateContent")
	if err != nil {
		return Result{}, err
	}

	reqBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": req.System}},
		},
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]string{{"text": req.User}},
			},
		},
	}
	if req.wantsJSON() {
		// Native JSON mode. responseMimeType constrains output to JSON;
		// responseSchema (when a schema is supplied) constrains its shape. The
		// salvage ladder (llmjson.Parse) still runs on the response as a
		// fallback.
		genCfg := map[string]any{"responseMimeType": "application/json"}
		if len(req.JSONSchema) > 0 {
			genCfg["responseSchema"] = json.RawMessage(req.JSONSchema)
		}
		reqBody["generationConfig"] = genCfg
	}
	if req.Stream {
		return geminiStream(ctx, cfg, base, model, key, reqBody)
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return Result{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Pass the API key in the header rather than the ?key= query string so it
	// never lands in URL logs, proxy access logs, or referrer headers.
	httpReq.Header.Set("x-goog-api-key", key)

	resp, err := httpClientFor(cfg).Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("gemini generateContent: %w", err)
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
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return Result{}, fmt.Errorf("parse gemini JSON: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return Result{}, fmt.Errorf("gemini API error: %s", envelope.Error.Message)
	}
	if len(envelope.Candidates) == 0 {
		return Result{}, fmt.Errorf("gemini: empty candidates")
	}
	var b strings.Builder
	for _, part := range envelope.Candidates[0].Content.Parts {
		b.WriteString(part.Text)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return Result{}, fmt.Errorf("gemini: empty text in response")
	}
	return Result{
		Text:  out,
		Model: model,
		Usage: Usage{
			InputTokens:  envelope.UsageMetadata.PromptTokenCount,
			OutputTokens: envelope.UsageMetadata.CandidatesTokenCount,
		},
	}, nil
}

// geminiStream performs the streaming variant using the SSE form of the
// generateContent endpoint (streamGenerateContent?alt=sse), accumulating
// candidate part text and reading usageMetadata from the final chunk. Result /
// Usage match the non-streaming path.
func geminiStream(ctx context.Context, cfg *aiconfig.Config, base, model, key string, reqBody map[string]any) (Result, error) {
	u, err := url.Parse(base + "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse")
	if err != nil {
		return Result{}, err
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	// Key in the header, never the query string (0.4 fix #7 preserved).
	httpReq.Header.Set("x-goog-api-key", key)
	return runSSEStream(ctx, cfg, string(cfg.Provider), httpReq, model, geminiSSEHandler)
}

// geminiSSEHandler maps one Gemini streamGenerateContent SSE chunk onto
// streamState. The stream ends at EOF (no [DONE] sentinel).
func geminiSSEHandler(_ /*event*/, data string, st *streamState) (string, bool, error) {
	if strings.TrimSpace(data) == "" {
		return "", false, nil
	}
	var chunk struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return "", false, nil
	}
	if chunk.Error != nil && chunk.Error.Message != "" {
		return "", false, fmt.Errorf("gemini API error: %s", chunk.Error.Message)
	}
	var delta string
	if len(chunk.Candidates) > 0 {
		for _, part := range chunk.Candidates[0].Content.Parts {
			delta += part.Text
		}
	}
	if delta != "" {
		st.text.WriteString(delta)
	}
	if chunk.UsageMetadata != nil {
		st.usage.InputTokens = chunk.UsageMetadata.PromptTokenCount
		st.usage.OutputTokens = chunk.UsageMetadata.CandidatesTokenCount
	}
	return delta, false, nil
}
