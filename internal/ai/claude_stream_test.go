package ai

import (
	"strings"
	"testing"
)

// TestParseClaudeStreamJSON proves the stream-json NDJSON parser skips the
// intermediate init/assistant events, counts each as a liveness tick, and
// returns the final `result` event's envelope (result text + usage + cost) —
// identical fields to the whole-response --output-format json envelope.
func TestParseClaudeStreamJSON(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"thinking"}]}}`,
		`not json — should be skipped`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"more"}]}}`,
		`{"type":"result","subtype":"success","result":"final answer","total_cost_usd":0.0123,"usage":{"input_tokens":321,"output_tokens":45}}`,
	}, "\n") + "\n"

	ticks := 0
	env, err := parseClaudeStreamJSON(strings.NewReader(stream), func(string) { ticks++ })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if env.Result != "final answer" {
		t.Fatalf("result = %q, want %q", env.Result, "final answer")
	}
	if env.Usage.InputTokens != 321 || env.Usage.OutputTokens != 45 {
		t.Fatalf("usage = %+v, want in=321 out=45", env.Usage)
	}
	if env.TotalCostUSD != 0.0123 {
		t.Fatalf("cost = %v, want 0.0123", env.TotalCostUSD)
	}
	// One tick per JSON event (the non-JSON line is skipped): system, 2×
	// assistant, result = 4.
	if ticks != 4 {
		t.Fatalf("liveness ticks = %d, want 4", ticks)
	}
}

// TestParseClaudeStreamJSONNoResult proves a stream that never emits a result
// event is an error (so the caller can classify it as a transient CLI glitch
// and retry).
func TestParseClaudeStreamJSONNoResult(t *testing.T) {
	stream := `{"type":"system","subtype":"init"}` + "\n"
	if _, err := parseClaudeStreamJSON(strings.NewReader(stream), nil); err == nil {
		t.Fatal("expected an error when no result event is present")
	}
}

// TestParseClaudeStreamJSONError proves an envelope-level error in the result
// event round-trips (IsError set) so resultFromClaudeEnvelope can classify it.
func TestParseClaudeStreamJSONError(t *testing.T) {
	stream := `{"type":"result","subtype":"error_max_turns","is_error":true,"error":"rate limit exceeded"}` + "\n"
	env, err := parseClaudeStreamJSON(strings.NewReader(stream), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !env.IsError {
		t.Fatal("expected IsError=true")
	}
	if _, rerr := resultFromClaudeEnvelope(env, "sonnet"); rerr == nil {
		t.Fatal("expected resultFromClaudeEnvelope to return an error for an error envelope")
	}
}
