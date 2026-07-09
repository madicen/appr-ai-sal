package repoagents

import (
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
)

// Freshness summarises the state of a repo's stored agents from a reviewer's
// perspective: whether we have anything to inject into specialist prompts,
// and whether what we have is recent enough to still be trusted.
//
// The TUI uses Freshness to color the "build repo agents" affordances so the
// reviewer sees at a glance whether the next review will run with rich
// repo-aware context, partial context, or none at all. The type and its
// states/labels are shared with langagents via internal/agentstore (their
// semantics coincide exactly); the constants below re-export the shared ones
// so existing callers keep using repoagents.FreshnessX unchanged.
type Freshness = agentstore.Freshness

const (
	FreshnessUnknown    = agentstore.FreshnessUnknown
	FreshnessMissing    = agentstore.FreshnessMissing
	FreshnessIncomplete = agentstore.FreshnessIncomplete
	FreshnessStale      = agentstore.FreshnessStale
	FreshnessFresh      = agentstore.FreshnessFresh
)

// DefaultStaleAfter is the age at which we start nagging the reviewer to
// regenerate. Long enough that day-to-day PR review on an unchanged repo
// doesn't churn the banner; short enough that a long-untouched repo gets
// flagged before its briefs go badly out of date. repoagents and techagents
// use 30d; langagents uses 60d (see agentstore.StaleScan).
const DefaultStaleAfter = 30 * 24 * time.Hour

// Compute returns the freshness for a loaded RepoAgents value. ra==nil and
// ra with no populated agents both return FreshnessMissing. Pass now from
// the caller for testability; staleAfter <= 0 disables the stale check
// (every fully-populated repo is reported Fresh).
func Compute(ra *RepoAgents, now time.Time, staleAfter time.Duration) Freshness {
	if ra == nil {
		return FreshnessMissing
	}

	var scan agentstore.StaleScan
	for _, sp := range Specialists {
		a, ok := ra.Get(sp)
		if !ok {
			continue
		}
		scan.Observe(a.Context, a.GeneratedAt)
	}

	switch {
	case scan.Have == 0:
		return FreshnessMissing
	case scan.Have < len(Specialists):
		return FreshnessIncomplete
	}
	if scan.Stale(now, staleAfter) {
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
