package ai

import (
	"strings"
	"testing"
)

// TestScanSSEBasic covers data lines, multi-line data joining, event fields,
// comment/heartbeat lines, and the [DONE] stop sentinel.
func TestScanSSEBasic(t *testing.T) {
	body := strings.Join([]string{
		": heartbeat comment",
		"data: one",
		"",
		"event: delta",
		"data: two-a",
		"data: two-b",
		"",
		"data: [DONE]",
		"",
		"data: after-done should not be seen",
		"",
	}, "\n")

	type ev struct {
		event, data string
	}
	var got []ev
	err := scanSSE(strings.NewReader(body), func(event, data string) (bool, error) {
		if data == "[DONE]" {
			return true, nil
		}
		got = append(got, ev{event, data})
		return false, nil
	})
	if err != nil {
		t.Fatalf("scanSSE: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].event != "" || got[0].data != "one" {
		t.Fatalf("event 0 = %+v, want {\"\",\"one\"}", got[0])
	}
	// Multi-line data joins with newlines.
	if got[1].event != "delta" || got[1].data != "two-a\ntwo-b" {
		t.Fatalf("event 1 = %+v, want {delta, \"two-a\\ntwo-b\"}", got[1])
	}
}

// TestScanSSETrailingNoBlankLine proves an event without a closing blank line
// (stream ended abruptly) is still flushed.
func TestScanSSETrailingNoBlankLine(t *testing.T) {
	var got []string
	err := scanSSE(strings.NewReader("data: only\n"), func(_, data string) (bool, error) {
		got = append(got, data)
		return false, nil
	})
	if err != nil {
		t.Fatalf("scanSSE: %v", err)
	}
	if len(got) != 1 || got[0] != "only" {
		t.Fatalf("got %v, want [only]", got)
	}
}
