package techagents

import (
	"strings"
	"time"
)

// Freshness summarises the state of a repo's stored tech-experts from a
// reviewer's perspective. Unlike repoagents.Freshness there is no
// canonical "expected set" of techs: the tech list is a per-repo
// opt-in feature. The default state for any repo is "no tech experts
// configured", which is normal — not an error — so we deliberately use
// kinder framing than repoagents.Freshness:
//
//   - FreshnessUnknown — caller has no owner/repo to consult yet.
//   - FreshnessMissing — file absent or zero populated tech briefs;
//     i.e. the user has not configured any tech experts for this repo.
//     This is the default and is NOT treated as needing attention.
//   - FreshnessStale   — the user did configure tech experts but at
//     least one populated brief is older than staleAfter (or has a
//     zero timestamp, indicating pre-migration data). This DOES need
//     attention because the user opted in and the data is decaying.
//   - FreshnessFresh   — every populated brief is recent.
type Freshness int

const (
	FreshnessUnknown Freshness = iota
	FreshnessMissing
	FreshnessStale
	FreshnessFresh
)

// String returns a short human label for the state. We render "missing"
// as "not configured" because for tech experts the absence of data is
// the unconfigured default, not a broken state.
func (f Freshness) String() string {
	switch f {
	case FreshnessMissing:
		return "not configured"
	case FreshnessStale:
		return "stale"
	case FreshnessFresh:
		return "fresh"
	default:
		return "unknown"
	}
}

// NeedsAttention is true when the reviewer should be warned visually.
// FreshnessMissing is intentionally excluded: a repo with no tech
// experts configured is the expected default and we don't want to nag
// reviewers about an opt-in feature they haven't opted into.
func (f Freshness) NeedsAttention() bool {
	return f == FreshnessStale
}

// DefaultStaleAfter is the age at which we start nagging the reviewer to
// regenerate. Mirrors repoagents.DefaultStaleAfter for consistency in
// the controls panel.
const DefaultStaleAfter = 30 * 24 * time.Hour

// Compute returns the freshness for a loaded TechAgents value. ta==nil
// and ta with no populated agents both return FreshnessMissing.
// staleAfter <= 0 disables the stale check.
func Compute(ta *TechAgents, now time.Time, staleAfter time.Duration) Freshness {
	if ta == nil {
		return FreshnessMissing
	}
	have := 0
	var oldest time.Time
	sawZeroTimestamp := false
	for _, a := range ta.Agents {
		if strings.TrimSpace(a.Context) == "" {
			continue
		}
		have++
		if a.GeneratedAt.IsZero() {
			sawZeroTimestamp = true
			continue
		}
		if oldest.IsZero() || a.GeneratedAt.Before(oldest) {
			oldest = a.GeneratedAt
		}
	}
	if have == 0 {
		return FreshnessMissing
	}
	if sawZeroTimestamp {
		return FreshnessStale
	}
	if staleAfter > 0 && !oldest.IsZero() && now.Sub(oldest) > staleAfter {
		return FreshnessStale
	}
	return FreshnessFresh
}

// LoadFreshness reads owner/repo from disk and computes its current
// freshness. A missing file or a load error both map to FreshnessMissing.
// Empty owner/repo returns FreshnessUnknown so the caller can suppress
// nags when no repo is in scope.
func LoadFreshness(owner, repo string, now time.Time, staleAfter time.Duration) Freshness {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return FreshnessUnknown
	}
	ta, err := Load(owner, repo)
	if err != nil {
		return FreshnessMissing
	}
	return Compute(ta, now, staleAfter)
}
