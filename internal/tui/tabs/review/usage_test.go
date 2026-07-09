package review

import (
	"strings"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// TestOverlayUsageLineFromProgress verifies the overlay adopts the runner's
// usage snapshot and renders the R1 summary line in the overview (done-state)
// and summary bodies.
func TestOverlayUsageLineFromProgress(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)

	// Before any usage event the line is empty (graceful default).
	if got := ro.usageLine(); got != "" {
		t.Fatalf("usageLine should be empty before usage arrives, got %q", got)
	}

	u := &review.RunUsage{Calls: 14, InputTokens: 182_000, OutputTokens: 21_000, CostUSD: 0.43, CostKnown: true, WallClock: 6*time.Minute + 12*time.Second}
	ro.mergeProgress(review.Progress{Stage: "usage", Usage: u})

	line := ro.usageLine()
	if !strings.Contains(line, "14 calls") || !strings.Contains(line, "182k in / 21k out") || !strings.Contains(line, "~$0.43") || !strings.Contains(line, "6m12s") {
		t.Fatalf("usageLine missing expected content: %q", line)
	}

	// The overview (running/done) body shows the line.
	if body := ro.renderRunningBody(); !strings.Contains(body, "~$0.43") {
		t.Fatalf("running body should include the usage line; got:\n%s", body)
	}
}

// TestOverlayUsageMonotonicGuard confirms a stale (lower-call-count) snapshot
// arriving out of order does not regress the displayed total.
func TestOverlayUsageMonotonicGuard(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	newer := &review.RunUsage{Calls: 10, InputTokens: 1000, OutputTokens: 100, WallClock: time.Minute}
	older := &review.RunUsage{Calls: 3, InputTokens: 300, OutputTokens: 30, WallClock: 20 * time.Second}
	ro.adoptUsage(newer)
	ro.adoptUsage(older) // should be ignored (fewer calls)
	if ro.runUsage == nil || ro.runUsage.Calls != 10 {
		t.Fatalf("stale snapshot should not regress the total, got %+v", ro.runUsage)
	}
}

// TestOverlayUsageCostUnknownOmitted confirms the cost segment is dropped when
// the provider reported no cost.
func TestOverlayUsageCostUnknownOmitted(t *testing.T) {
	ro := New(120, 44, false, false, false, nil, false)
	ro.mergeProgress(review.Progress{Stage: "done", Usage: &review.RunUsage{Calls: 2, InputTokens: 5000, OutputTokens: 600, WallClock: 42 * time.Second}})
	line := ro.usageLine()
	if strings.Contains(line, "$") {
		t.Fatalf("cost segment should be omitted when cost is unknown, got %q", line)
	}
	if !strings.Contains(line, "2 calls") || !strings.Contains(line, "42s") {
		t.Fatalf("usageLine missing expected content: %q", line)
	}
}
