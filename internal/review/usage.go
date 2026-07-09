package review

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madicen/appr-ai-sal/internal/ai"
)

// StageUsage is the aggregated inference usage for one pipeline stage
// (e.g. "specialist security", "repo-arbiter"). Duration is the summed
// per-call wall-clock for that stage — note that under parallel dispatch it
// can exceed the run's WallClock, since calls overlap.
type StageUsage struct {
	Stage        string
	Calls        int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	Duration     time.Duration
}

// RunUsage is the aggregated inference usage for a whole review run: run-wide
// totals, a per-stage breakdown, and the run's wall-clock. It is threaded to
// the TUI through Progress so the overlay can show running totals and a final
// "14 calls · 182k in / 21k out · ~$0.43 · 6m12s" summary line.
type RunUsage struct {
	Calls        int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	// CostKnown is true once any call reported a non-zero cost. Providers that
	// don't surface cost (most HTTP backends today) leave it false so the
	// formatter omits the "~$…" segment instead of showing a misleading
	// "~$0.00".
	CostKnown bool
	WallClock time.Duration
	Stages    []StageUsage
}

// HasData reports whether the run made any metered inference calls. The TUI
// uses it to decide whether to render the usage line at all.
func (u RunUsage) HasData() bool { return u.Calls > 0 }

// Summary renders the compact one-line usage/cost/time summary shown in the
// review overlay, e.g. "14 calls · 182k in / 21k out · ~$0.43 · 6m12s". The
// cost segment is omitted gracefully when no provider reported cost.
func (u RunUsage) Summary() string {
	parts := []string{
		fmt.Sprintf("%d %s", u.Calls, pluralCalls(u.Calls)),
		fmt.Sprintf("%s in / %s out", FormatTokenCount(u.InputTokens), FormatTokenCount(u.OutputTokens)),
	}
	if u.CostKnown && u.CostUSD > 0 {
		parts = append(parts, FormatCostUSD(u.CostUSD))
	}
	parts = append(parts, FormatRunDuration(u.WallClock))
	return strings.Join(parts, " · ")
}

func pluralCalls(n int) string {
	if n == 1 {
		return "call"
	}
	return "calls"
}

// FormatTokenCount renders a token count compactly: 182345 → "182k",
// 1_500_000 → "1.5M", values under 1000 stay exact. Negative inputs clamp to 0.
func FormatTokenCount(n int) string {
	if n < 0 {
		n = 0
	}
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return strconv.Itoa(n/1000) + "k"
	default:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	}
}

// FormatCostUSD renders a cost with a leading "~$" (it is an estimate) and two
// decimals, e.g. 0.43 → "~$0.43".
func FormatCostUSD(cost float64) string {
	if cost < 0 {
		cost = 0
	}
	return fmt.Sprintf("~$%.2f", cost)
}

// FormatRunDuration renders a wall-clock duration human-readably in the form
// used by the usage line: "42s", "6m12s", "1h05m". Non-positive durations
// render as "0s".
func FormatRunDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	return fmt.Sprintf("%dh%02dm", h, m)
}

// usageAccumulator aggregates ai.CallReports across a run. It is safe for
// concurrent use because parallel specialists / PR agents report from their
// own goroutines through the single provider observer.
type usageAccumulator struct {
	mu        sync.Mutex
	stages    map[string]*StageUsage
	order     []string
	calls     int
	inTokens  int
	outTokens int
	cost      float64
	costKnown bool
}

func newUsageAccumulator() *usageAccumulator {
	return &usageAccumulator{stages: map[string]*StageUsage{}}
}

// record folds one completed call into the totals and its stage bucket.
func (a *usageAccumulator) record(r ai.CallReport) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	a.inTokens += r.Usage.InputTokens
	a.outTokens += r.Usage.OutputTokens
	a.cost += r.Usage.CostUSD
	if r.Usage.CostUSD > 0 {
		a.costKnown = true
	}
	stage := strings.TrimSpace(r.Stage)
	if stage == "" {
		stage = "other"
	}
	su := a.stages[stage]
	if su == nil {
		su = &StageUsage{Stage: stage}
		a.stages[stage] = su
		a.order = append(a.order, stage)
	}
	su.Calls++
	su.InputTokens += r.Usage.InputTokens
	su.OutputTokens += r.Usage.OutputTokens
	su.CostUSD += r.Usage.CostUSD
	su.Duration += r.Duration
}

// snapshot returns an immutable copy of the current totals plus the per-stage
// breakdown, stamped with the supplied run wall-clock. Stages are ordered by
// first-seen so the breakdown is deterministic for a given call order; callers
// wanting a stable display order can sort by Stage.
func (a *usageAccumulator) snapshot(wall time.Duration) RunUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	ru := RunUsage{
		Calls:        a.calls,
		InputTokens:  a.inTokens,
		OutputTokens: a.outTokens,
		CostUSD:      a.cost,
		CostKnown:    a.costKnown,
		WallClock:    wall,
	}
	for _, name := range a.order {
		ru.Stages = append(ru.Stages, *a.stages[name])
	}
	return ru
}
