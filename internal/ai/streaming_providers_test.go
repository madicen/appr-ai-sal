package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/applog"
)

// sseServer starts an httptest server that writes the given SSE frames (each
// already terminated with its blank line) with a flush between them so the
// client sees a genuine stream. It records the decoded request body.
func sseServer(t *testing.T, frames ...string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, f := range frames {
			_, _ = io.WriteString(w, f)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

func TestOpenAIStreamAccumulatesTextAndUsage(t *testing.T) {
	srv, body := sseServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\", world\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\n",
		"data: [DONE]\n\n",
	)
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen", TimeoutSec: 5}
	res, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if err != nil {
		t.Fatalf("stream Complete: %v", err)
	}
	if res.Text != "Hello, world" {
		t.Fatalf("text = %q, want %q", res.Text, "Hello, world")
	}
	if res.Usage.InputTokens != 12 || res.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v, want in=12 out=3", res.Usage)
	}
	if (*body)["stream"] != true {
		t.Fatalf("request should set stream=true, got %v", (*body)["stream"])
	}
	so, ok := (*body)["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("request should set stream_options.include_usage=true, got %v", (*body)["stream_options"])
	}
}

// TestStreamingResultIdenticalToNonStreaming proves the same logical response
// yields byte-identical Result/Usage whether streamed or not — the backward-
// compat guarantee for evals/headless callers.
func TestStreamingResultIdenticalToNonStreaming(t *testing.T) {
	// Non-streaming server.
	nonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Hello, world"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
	}))
	defer nonSrv.Close()
	nonCfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: nonSrv.URL, Model: "qwen", TimeoutSec: 5}
	nonRes, err := (&openAIProvider{cfg: nonCfg}).Complete(context.Background(), Request{System: "s", User: "u"})
	if err != nil {
		t.Fatalf("non-stream: %v", err)
	}

	// Streaming server emitting the same logical content in pieces.
	strSrv, _ := sseServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\", world\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\n",
		"data: [DONE]\n\n",
	)
	strCfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: strSrv.URL, Model: "qwen", TimeoutSec: 5}
	strRes, err := (&openAIProvider{cfg: strCfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	if strRes.Text != nonRes.Text {
		t.Fatalf("streamed text %q != non-streamed %q", strRes.Text, nonRes.Text)
	}
	if strRes.Usage != nonRes.Usage {
		t.Fatalf("streamed usage %+v != non-streamed %+v", strRes.Usage, nonRes.Usage)
	}
}

func TestAzureStreamSharesOpenAIPath(t *testing.T) {
	srv, _ := sseServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"az\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"ure\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1}}\n\n",
		"data: [DONE]\n\n",
	)
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAzure, BaseURL: srv.URL, Model: "dep", APIKey: "k", TimeoutSec: 5}
	res, err := (&azureProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if err != nil {
		t.Fatalf("azure stream: %v", err)
	}
	if res.Text != "azure" || res.Usage.InputTokens != 4 || res.Usage.OutputTokens != 1 {
		t.Fatalf("res = %+v", res)
	}
}

func TestGeminiStreamSSE(t *testing.T) {
	var gotPath, gotHeaderKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaderKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, f := range []string{
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi \"}]}}]}\n\n",
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"there\"}]}}],\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":2}}\n\n",
		} {
			_, _ = io.WriteString(w, f)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderGemini, BaseURL: srv.URL, Model: "gemini-2.0-flash", APIKey: "secret", TimeoutSec: 5}
	res, err := (&geminiProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if err != nil {
		t.Fatalf("gemini stream: %v", err)
	}
	if res.Text != "hi there" {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Usage.InputTokens != 9 || res.Usage.OutputTokens != 2 {
		t.Fatalf("usage = %+v, want in=9 out=2", res.Usage)
	}
	if !strings.Contains(gotPath, ":streamGenerateContent") {
		t.Fatalf("path = %q, want streamGenerateContent", gotPath)
	}
	if gotHeaderKey != "secret" {
		t.Fatalf("key must be in x-goog-api-key header, got %q", gotHeaderKey)
	}
}

func TestAnthropicStreamTextAndUsage(t *testing.T) {
	srv, body := sseServer(t,
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-x\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"looks \"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"good\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	)
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-sonnet-4", APIKey: "k", TimeoutSec: 5}
	res, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if err != nil {
		t.Fatalf("anthropic stream: %v", err)
	}
	if res.Text != "looks good" {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Usage.InputTokens != 100 || res.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want in=100 out=7", res.Usage)
	}
	if res.Model != "claude-x" {
		t.Fatalf("model = %q, want claude-x", res.Model)
	}
	if (*body)["stream"] != true {
		t.Fatalf("request should set stream=true, got %v", (*body)["stream"])
	}
}

// TestAnthropicStreamToolJSON proves the forced-tool JSON path accumulates
// input_json_delta fragments into the final JSON object (matching the
// non-streaming tool_use `input`).
func TestAnthropicStreamToolJSON(t *testing.T) {
	srv, _ := sseServer(t,
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-x\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\":\"emit_json\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"verdict\\\":\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"approve\\\"}\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	)
	schema := json.RawMessage(`{"type":"object"}`)
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k", TimeoutSec: 5}
	res, err := (&anthropicProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true, JSONSchema: schema})
	if err != nil {
		t.Fatalf("anthropic tool stream: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(res.Text), &parsed); err != nil {
		t.Fatalf("accumulated text is not JSON: %q (%v)", res.Text, err)
	}
	if parsed["verdict"] != "approve" {
		t.Fatalf("verdict = %v, want approve", parsed["verdict"])
	}
}

// TestStreamLivenessObserver proves streamed deltas surface a throttled
// activity heartbeat carrying the stage label and a running token count that
// ends at the number of content deltas.
func TestStreamLivenessObserver(t *testing.T) {
	srv, _ := sseServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"c\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":3}}\n\n",
		"data: [DONE]\n\n",
	)
	var reports []ActivityReport
	var mu atomicReports
	ctx := applog.WithStage(context.Background(), "specialist security")
	ctx = WithActivityObserver(ctx, func(r ActivityReport) { mu.append(r) })

	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen", TimeoutSec: 5}
	if _, err := (&openAIProvider{cfg: cfg}).Complete(ctx, Request{System: "s", User: "u", Stream: true}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	reports = mu.snapshot()
	if len(reports) == 0 {
		t.Fatal("expected at least one activity heartbeat")
	}
	last := reports[len(reports)-1]
	if last.Stage != "specialist security" {
		t.Fatalf("heartbeat stage = %q, want %q", last.Stage, "specialist security")
	}
	if last.Tokens != 3 {
		t.Fatalf("final token count = %d, want 3 (one per content delta)", last.Tokens)
	}
}

// atomicReports is a tiny concurrency-safe slice for the observer (which the
// docs say may be called from multiple goroutines).
type atomicReports struct {
	v atomic.Value
	// guard writes; reads use the loaded snapshot.
}

func (a *atomicReports) append(r ActivityReport) {
	cur, _ := a.v.Load().([]ActivityReport)
	next := make([]ActivityReport, len(cur)+1)
	copy(next, cur)
	next[len(cur)] = r
	a.v.Store(next)
}

func (a *atomicReports) snapshot() []ActivityReport {
	cur, _ := a.v.Load().([]ActivityReport)
	return cur
}

// TestOpenAIStreamIdleTimeout proves a stream that starts then goes silent is
// aborted with the retryable idle-timeout error (TimeoutSec reinterpreted as
// the idle budget).
func TestOpenAIStreamIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if fl != nil {
			fl.Flush()
		}
		// Go silent well past the idle budget, but return promptly once the
		// client cancels so srv.Close() does not block.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen", TimeoutSec: 1}
	_, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("err = %v, want ErrStreamIdleTimeout", err)
	}
	if !IsRetryableCompleteError(err) {
		t.Fatal("idle timeout should be retryable")
	}
}

// TestOpenAIStreamFirstByteTimeout proves a connection that produces nothing is
// aborted with the retryable first-byte-timeout error.
func TestOpenAIStreamFirstByteTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush() // flush headers only; no body bytes
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen", TimeoutSec: 1}
	_, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if !errors.Is(err, ErrStreamFirstByteTimeout) {
		t.Fatalf("err = %v, want ErrStreamFirstByteTimeout", err)
	}
	if !IsRetryableCompleteError(err) {
		t.Fatal("first-byte timeout should be retryable")
	}
}

// TestOpenAIStreamTrickleSurvivesTimeout proves a slow-but-alive SSE stream
// (a delta every 300ms) with a 1s idle budget completes normally rather than
// dying — the whole point of the idle timeout.
func TestOpenAIStreamTrickleSurvivesTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()
	cfg := &aiconfig.Config{Provider: aiconfig.ProviderOpenAICompatible, BaseURL: srv.URL, Model: "qwen", TimeoutSec: 1}
	res, err := (&openAIProvider{cfg: cfg}).Complete(context.Background(), Request{System: "s", User: "u", Stream: true})
	if err != nil {
		t.Fatalf("trickle stream should survive a 1s idle budget, got %v", err)
	}
	if res.Text != "xxxxx" {
		t.Fatalf("text = %q, want xxxxx", res.Text)
	}
}
