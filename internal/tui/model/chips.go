package model

import (
	"strconv"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
)

func (m *Model) repoAgentsFreshness(owner, repo string) repoagentsstore.Freshness {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = strings.ToLower(strings.TrimSpace(repo))
	if owner == "" || repo == "" {
		return repoagentsstore.FreshnessUnknown
	}
	key := owner + "/" + repo
	now := time.Now()
	if e, ok := m.repoAgentsFreshnessCache[key]; ok {
		if now.Sub(e.computed) < repoAgentsFreshnessCacheTTL {
			return e.state
		}
	}
	state := repoagentsstore.LoadFreshness(owner, repo, now, repoagentsstore.DefaultStaleAfter)
	if m.repoAgentsFreshnessCache == nil {
		m.repoAgentsFreshnessCache = map[string]repoAgentsFreshnessEntry{}
	}
	m.repoAgentsFreshnessCache[key] = repoAgentsFreshnessEntry{state: state, computed: now}
	return state
}

// invalidateRepoAgentsFreshness drops cached freshness entries so the next
// render re-reads from disk. Called when the repo-agents tab returns (any
// repo could have been regenerated) and proactively when the user opens the
// tab (so the post-edit reading is fresh on return).
func (m *Model) invalidateRepoAgentsFreshness() {
	m.repoAgentsFreshnessCache = nil
}

// prKey is the cache key for prLanguages / langAgentsFreshnessCache.
// We use owner+repo+number rather than just number so two PRs with the
// same number in different repos can't collide.
func prKey(owner, repo string, number int) string {
	return strings.ToLower(strings.TrimSpace(owner)) + "/" + strings.ToLower(strings.TrimSpace(repo)) + "#" + strconv.Itoa(number)
}

// recordPRLanguages stores the canonical touched-language set for a
// PR. Called from the detail-mode loaders right after ParseDiff so
// list-mode rendering of the same PR knows what's touched without
// re-fetching anything. Called with a nil/empty parsedDiff is a no-op.
func (m *Model) recordPRLanguages(pr *gh.PR, parsed []review.FileDiff) {
	if pr == nil {
		return
	}
	if m.prLanguages == nil {
		m.prLanguages = map[string][]langagentsstore.Language{}
	}
	// Use an empty slice (not nil) to mark "we parsed; nothing
	// recognised" so the freshness computer returns FreshnessFresh
	// rather than FreshnessUnknown.
	m.prLanguages[prKey(pr.Owner, pr.Repo, pr.Number)] = languagesForFileDiffs(parsed)
	// Any change to a PR's touched set invalidates the cached
	// freshness reading for that PR (and is cheap to recompute on
	// next render).
	delete(m.langAgentsFreshnessCache, prKey(pr.Owner, pr.Repo, pr.Number))
}

// langAgentsFreshness returns the PR-aggregated freshness reading
// for a (owner, repo, number) triple. Returns FreshnessUnknown when
// we have no record of the PR's touched languages — typically a list
// row the user hasn't drilled into this session. Callers should
// render neutrally on Unknown rather than warn (no signal == no nag).
//
// TTL'd via langAgentsFreshnessCache so the renderer doesn't re-read
// disk on every frame; invalidated wholesale by
// invalidateLangAgentsFreshness when the lang-agents tab returns.
func (m *Model) langAgentsFreshness(owner, repo string, number int) langagentsstore.Freshness {
	if number == 0 || strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return langagentsstore.FreshnessUnknown
	}
	key := prKey(owner, repo, number)
	touched, known := m.prLanguages[key]
	if !known {
		return langagentsstore.FreshnessUnknown
	}
	now := time.Now()
	if e, ok := m.langAgentsFreshnessCache[key]; ok {
		if now.Sub(e.computed) < langAgentsFreshnessCacheTTL {
			return e.state
		}
	}
	cache, _ := langagentsstore.LoadCache()
	state := langagentsstore.ComputePR(touched, cache, now, langagentsstore.DefaultStaleAfter)
	if m.langAgentsFreshnessCache == nil {
		m.langAgentsFreshnessCache = map[string]langAgentsFreshnessEntry{}
	}
	m.langAgentsFreshnessCache[key] = langAgentsFreshnessEntry{state: state, computed: now}
	return state
}

// invalidateLangAgentsFreshness drops the cached PR-freshness
// readings so the next render recomputes from disk. Called when the
// user closes the lang-agents tab so any brief they generated or
// deleted flips the chip colour on the surrounding views immediately.
func (m *Model) invalidateLangAgentsFreshness() {
	m.langAgentsFreshnessCache = nil
}

// buildRepoAgentsChip is the right-side chip in the PR detail mini-header.
// It always has the same key affordance ("build repo agents (ctrl+b)") but
// gains a warning state suffix and a louder colour when this PR's repo has
// no agents, partial agents, or aging agents — so the reviewer sees at a
// glance whether the next review will get rich repo-aware context.
func (m *Model) buildRepoAgentsChip() string {
	if m.currentPR == nil {
		return styles.DimStyle.Render(" build repo agents (ctrl+b) ")
	}
	state := m.repoAgentsFreshness(m.currentPR.Owner, m.currentPR.Repo)
	switch state {
	case repoagentsstore.FreshnessMissing:
		return styles.ErrStyle.Render(" build repo agents (ctrl+b) — missing ")
	case repoagentsstore.FreshnessIncomplete:
		return styles.WarnStyle.Render(" build repo agents (ctrl+b) — partial ")
	case repoagentsstore.FreshnessStale:
		return styles.WarnStyle.Render(" build repo agents (ctrl+b) — stale ")
	default:
		return styles.DimStyle.Render(" build repo agents (ctrl+b) ")
	}
}

// renderBuildAgentsHint returns the styled "ctrl+b build agents" segment
// for the bottom status bar. When the freshness for the supplied repo
// needs attention, the segment is coloured (red for missing, yellow for
// partial / stale) and gets a short state suffix; otherwise it returns
// the plain label that the surrounding styles.StatusBar style renders
// dim with the rest of the hints.
func (m *Model) renderBuildAgentsHint(owner, repo string) string {
	const label = "ctrl+b build agents"
	state := m.repoAgentsFreshness(owner, repo)
	switch state {
	case repoagentsstore.FreshnessMissing:
		return styles.ErrStyle.Render(label + " (missing!)")
	case repoagentsstore.FreshnessIncomplete:
		return styles.WarnStyle.Render(label + " (partial)")
	case repoagentsstore.FreshnessStale:
		return styles.WarnStyle.Render(label + " (stale)")
	default:
		return label
	}
}

// buildLangAgentsChip is the lang-agents twin of buildRepoAgentsChip,
// pinned to the right side of the PR detail mini-header so a "this PR
// has a language with no expert" warning is visible the moment the
// reviewer opens the PR rather than only when they glance at the
// status bar.
func (m *Model) buildLangAgentsChip() string {
	if m.currentPR == nil {
		return styles.DimStyle.Render(" build lang experts (ctrl+l) ")
	}
	state := m.langAgentsFreshness(m.currentPR.Owner, m.currentPR.Repo, m.currentPR.Number)
	switch state {
	case langagentsstore.FreshnessMissing:
		return styles.ErrStyle.Render(" build lang experts (ctrl+l) — missing ")
	case langagentsstore.FreshnessStale:
		return styles.WarnStyle.Render(" build lang experts (ctrl+l) — stale ")
	default:
		return styles.DimStyle.Render(" build lang experts (ctrl+l) ")
	}
}

func (m *Model) repoSeedsFromList(_ *repoconfig.Config) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, it := range m.list.Items() {
		pi, ok := it.(prItem)
		if !ok {
			continue
		}
		k := strings.ToLower(pi.pr.Owner + "/" + pi.pr.Repo)
		if k == "/" {
			continue
		}
		if _, exists := seen[k]; exists {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// chromeBodyHeight is the vertical space between header and status bars.
