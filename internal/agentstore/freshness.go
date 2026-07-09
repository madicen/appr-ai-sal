package agentstore

import (
	"strings"
	"time"
)

// Freshness summarises the state of a set of stored briefs from a reviewer's
// perspective: whether there is anything to inject and whether it is recent
// enough to trust. It is shared by repoagents and langagents (whose states
// coincide exactly). techagents deliberately keeps its own narrower type
// because for tech experts the "missing" state is the expected opt-in default
// rather than something to nag about; see techagents/freshness.go.
type Freshness int

const (
	// FreshnessUnknown is the zero value: not computed yet, or not enough
	// information (e.g. no current repo/PR). Render neutrally; do not nag.
	FreshnessUnknown Freshness = iota
	// FreshnessMissing: nothing usable on disk. Reviews run with bare
	// prompts; the reviewer should generate briefs first.
	FreshnessMissing
	// FreshnessIncomplete: some briefs present but at least one is missing.
	// Partial coverage.
	FreshnessIncomplete
	// FreshnessStale: full coverage but at least one brief is older than the
	// configured stale-after window (or has a zero timestamp, i.e. legacy
	// pre-migration data).
	FreshnessStale
	// FreshnessFresh: full, recent coverage. No nag.
	FreshnessFresh
)

// String returns a short human label used by TUI chips and tests.
func (f Freshness) String() string {
	switch f {
	case FreshnessMissing:
		return "missing"
	case FreshnessIncomplete:
		return "partial"
	case FreshnessStale:
		return "stale"
	case FreshnessFresh:
		return "fresh"
	default:
		return "unknown"
	}
}

// NeedsAttention is true when the reviewer should be warned visually. It
// covers Missing, Incomplete, and Stale; Fresh and Unknown render neutrally.
func (f Freshness) NeedsAttention() bool {
	switch f {
	case FreshnessMissing, FreshnessIncomplete, FreshnessStale:
		return true
	default:
		return false
	}
}

// StaleScan accumulates the age signal across a set of populated brief
// entries so the three subsystems compute staleness identically. Callers feed
// each brief's context body and generated-at timestamp via Observe, then ask
// Stale for the verdict.
//
// The per-family stale-after window differs by design and is passed into
// Stale rather than baked in: repoagents and techagents use 30 days,
// langagents uses 60 days (language conventions drift far more slowly than a
// repo's own conventions or PR-review history). Keeping it a parameter lets
// each family — and tests — set its own window.
type StaleScan struct {
	// Have counts populated (non-empty context) briefs observed.
	Have int

	oldest       time.Time
	sawZeroStamp bool
}

// Observe records one brief. Briefs with a blank context are ignored (they
// don't count toward Have). A populated brief with a zero timestamp is
// treated as stale — almost always legacy data predating the metadata.
func (s *StaleScan) Observe(context string, generatedAt time.Time) {
	if strings.TrimSpace(context) == "" {
		return
	}
	s.Have++
	if generatedAt.IsZero() {
		s.sawZeroStamp = true
		return
	}
	if s.oldest.IsZero() || generatedAt.Before(s.oldest) {
		s.oldest = generatedAt
	}
}

// Stale reports whether the observed set is stale as of now: any zero
// timestamp, or an oldest brief beyond staleAfter. staleAfter <= 0 disables
// the age check (a fully-populated set is then never reported stale on age
// alone, though a zero timestamp still counts as stale).
func (s StaleScan) Stale(now time.Time, staleAfter time.Duration) bool {
	if s.sawZeroStamp {
		return true
	}
	return staleAfter > 0 && !s.oldest.IsZero() && now.Sub(s.oldest) > staleAfter
}
