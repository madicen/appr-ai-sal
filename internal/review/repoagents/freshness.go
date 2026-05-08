package repoagents

import (
	"strings"
	"time"
)

// Freshness summarises the state of a repo's stored agents from a reviewer's
// perspective: whether we have anything to inject into specialist prompts,
// and whether what we have is recent enough to still be trusted.
//
// The TUI uses Freshness to color the "build repo agents" affordances so the
// reviewer sees at a glance whether the next review will run with rich
// repo-aware context, partial context, or none at all.
type Freshness int

const (
	// FreshnessUnknown is the zero value: callers haven't computed yet, or
	// they don't have enough information (e.g. no current PR). Render as
	// the default style; do not nag.
	FreshnessUnknown Freshness = iota

	// FreshnessMissing means there are no usable agents on disk for this
	// repo. Reviews will run with the bare specialist prompts; the reviewer
	// should run "build repo agents" before reviewing meaningful changes.
	FreshnessMissing

	// FreshnessIncomplete means at least one specialist has a brief but at
	// least one is also missing. Partial coverage; the missing specialists
	// fall back to bare prompts.
	FreshnessIncomplete

	// FreshnessStale means every specialist has a brief but at least one
	// was generated more than the configured staleAfter window ago. The
	// repo's conventions or PR review history have likely drifted since
	// these briefs were produced.
	FreshnessStale

	// FreshnessFresh means every specialist has a recent brief. No nag.
	FreshnessFresh
)

// String returns a short human label for the state. Used by the TUI status
// chips and by tests.
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
// covers Missing, Incomplete, and Stale; Fresh and Unknown both render as
// the default (uncoloured) state.
func (f Freshness) NeedsAttention() bool {
	switch f {
	case FreshnessMissing, FreshnessIncomplete, FreshnessStale:
		return true
	default:
		return false
	}
}

// DefaultStaleAfter is the age at which we start nagging the reviewer to
// regenerate. Long enough that day-to-day PR review on an unchanged repo
// doesn't churn the banner; short enough that a long-untouched repo gets
// flagged before its briefs go badly out of date.
const DefaultStaleAfter = 30 * 24 * time.Hour

// Compute returns the freshness for a loaded RepoAgents value. ra==nil and
// ra with no populated agents both return FreshnessMissing. Pass now from
// the caller for testability; staleAfter <= 0 disables the stale check
// (every fully-populated repo is reported Fresh).
func Compute(ra *RepoAgents, now time.Time, staleAfter time.Duration) Freshness {
	if ra == nil {
		return FreshnessMissing
	}

	have := 0
	var oldest time.Time
	sawZeroTimestamp := false

	for _, sp := range Specialists {
		a, ok := ra.Get(sp)
		if !ok {
			continue
		}
		if strings.TrimSpace(a.Context) == "" {
			continue
		}
		have++
		ts := a.GeneratedAt
		if ts.IsZero() {
			// A populated brief without a timestamp is almost certainly
			// pre-migration data. Treat as stale rather than silently
			// claiming it's fresh.
			sawZeroTimestamp = true
			continue
		}
		if oldest.IsZero() || ts.Before(oldest) {
			oldest = ts
		}
	}

	switch {
	case have == 0:
		return FreshnessMissing
	case have < len(Specialists):
		return FreshnessIncomplete
	}

	// All specialists covered.
	if sawZeroTimestamp {
		return FreshnessStale
	}
	if staleAfter > 0 && !oldest.IsZero() && now.Sub(oldest) > staleAfter {
		return FreshnessStale
	}
	return FreshnessFresh
}

// LoadFreshness reads owner/repo from disk and computes its current
// freshness. A missing file or a load error both map to FreshnessMissing —
// from the reviewer's perspective the fix is the same in either case
// (open the tab and (re)generate). owner=="" or repo=="" returns
// FreshnessUnknown so the caller can suppress nags when it doesn't yet
// know which repo is in scope.
func LoadFreshness(owner, repo string, now time.Time, staleAfter time.Duration) Freshness {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return FreshnessUnknown
	}
	ra, err := Load(owner, repo)
	if err != nil {
		return FreshnessMissing
	}
	return Compute(ra, now, staleAfter)
}
