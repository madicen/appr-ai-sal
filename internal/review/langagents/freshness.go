package langagents

import (
	"strings"
	"time"
)

// Freshness summarises the per-language brief cache state from a
// reviewer's perspective. Mirrors repoagents.Freshness so the TUI can
// reuse its semantics: missing / partial / stale / fresh, plus an
// "unknown" zero value for "we don't know yet."
//
// All language briefs are LLM-generated into the user cache; there is
// no bundled "always fresh" tier. A missing language brief is therefore
// reported as FreshnessMissing — the right state to nudge the user
// toward the Generate action in the TUI.
type Freshness int

const (
	FreshnessUnknown Freshness = iota
	FreshnessMissing
	FreshnessIncomplete
	FreshnessStale
	FreshnessFresh
)

// String returns a short human label for the state.
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

// NeedsAttention is true when the reviewer should be warned visually.
func (f Freshness) NeedsAttention() bool {
	switch f {
	case FreshnessMissing, FreshnessIncomplete, FreshnessStale:
		return true
	default:
		return false
	}
}

// DefaultStaleAfter is the age at which a cached language brief gets
// flagged as stale. Long-ish since language conventions don't actually
// shift week-to-week; short enough that a brief generated against an
// old model gets re-run after a couple of months.
const DefaultStaleAfter = 60 * 24 * time.Hour

// ComputeLanguage returns freshness for a single canonical language.
//   - Populated cache entry younger than staleAfter: FreshnessFresh.
//   - Populated entry older than staleAfter or with a zero
//     GeneratedAt: FreshnessStale (the zero-timestamp case usually
//     means a manually-edited brief that pre-dates the metadata, so we
//     prefer to nag rather than silently claim freshness).
//   - No entry at all: FreshnessMissing.
func ComputeLanguage(lang Language, cache *LangAgents, now time.Time, staleAfter time.Duration) Freshness {
	c := Canonical(lang)
	if c == "" {
		return FreshnessUnknown
	}
	if cache == nil {
		return FreshnessMissing
	}
	a, ok := cache.Get(c)
	if !ok || strings.TrimSpace(a.Context) == "" {
		return FreshnessMissing
	}
	if a.GeneratedAt.IsZero() {
		return FreshnessStale
	}
	if staleAfter > 0 && now.Sub(a.GeneratedAt) > staleAfter {
		return FreshnessStale
	}
	return FreshnessFresh
}

// ComputePR aggregates per-language freshness over the language set a
// PR touches and returns a single state for the whole PR. Used by the
// TUI to color the "build lang experts" affordance on each PR row /
// header — same UX shape as repoagents.LoadFreshness, but at PR
// granularity since languages are intrinsically per-diff.
//
// Rules:
//
//   - len(touched) == 0  → FreshnessFresh (nothing to warn about; the
//     PR touches no languages we recognise).
//   - Any touched language has no cached brief → FreshnessMissing
//     (loudest state; this is the screenshot-failure preventer).
//   - All touched languages have briefs but at least one is stale →
//     FreshnessStale.
//   - All touched languages have fresh cached briefs → FreshnessFresh.
//
// Returns FreshnessUnknown only when callers explicitly want the "we
// don't know yet" state — pass a nil `touched` for that.
func ComputePR(touched []Language, cache *LangAgents, now time.Time, staleAfter time.Duration) Freshness {
	if touched == nil {
		return FreshnessUnknown
	}
	if len(touched) == 0 {
		// PR touches no recognised languages — nothing the user
		// could generate a brief for. Render neutrally.
		return FreshnessFresh
	}
	seenStale := false
	for _, l := range touched {
		switch ComputeLanguage(l, cache, now, staleAfter) {
		case FreshnessMissing:
			return FreshnessMissing
		case FreshnessStale:
			seenStale = true
		}
	}
	if seenStale {
		return FreshnessStale
	}
	return FreshnessFresh
}

// ReviewSummary captures a PR-scoped view of which languages the PR
// touches and which of those have an injectable brief, for the TUI to
// render a pre-review banner.
type ReviewSummary struct {
	// Touched is the set of canonical languages the diff actually
	// touches, in dominant-first order (same order BriefsForDiff
	// returns).
	Touched []Language
	// Briefs are the languages we WILL inject — top-N from Touched
	// that have a brief available. Same shape BriefsForDiff returns;
	// recorded separately so the TUI can show "injecting X & Y" vs.
	// "skipping Z (no brief)".
	Briefs []Brief
	// Missing lists languages from Touched that the PR exercises but
	// have no brief available (bundled or cached). The TUI's "generate
	// missing" action lists these.
	Missing []Language
}

// HasMissing reports whether any touched language lacks a brief.
func (s ReviewSummary) HasMissing() bool {
	return len(s.Missing) > 0
}

// SummariseForDiff builds a ReviewSummary from a path -> changed-line
// count map. Convenience wrapper around BriefsForDiff that also
// reports the full touched-language set.
func SummariseForDiff(touchesByPath map[string]int) ReviewSummary {
	if len(touchesByPath) == 0 {
		return ReviewSummary{}
	}
	byLang := map[Language]int{}
	for p, n := range touchesByPath {
		c := LanguageForPath(p)
		if c == "" {
			continue
		}
		byLang[c] += n
	}
	touched := make([]Language, 0, len(byLang))
	for l := range byLang {
		touched = append(touched, l)
	}
	// Stable dominant-first sort matching BriefsForDiff.
	for i := 0; i < len(touched); i++ {
		for j := i + 1; j < len(touched); j++ {
			if byLang[touched[j]] > byLang[touched[i]] {
				touched[i], touched[j] = touched[j], touched[i]
			}
		}
	}
	briefs, missing := BriefsForDiff(touchesByPath)
	return ReviewSummary{
		Touched: touched,
		Briefs:  briefs,
		Missing: missing,
	}
}
