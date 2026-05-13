// Package techagents stores and generates per-repo "technology expert"
// briefs that get injected into specialist review prompts.
//
// Unlike repoagents (one brief per (repo, specialist)) and like langagents
// (one brief per language) the unit here is a TECHNOLOGY: e.g. "kestra",
// "terraform", "kafka". A repo can have any number of tech briefs and each
// brief is a single markdown context document shared across all five
// specialists for that repo.
//
// Per-repo storage on disk under
// <cache>/repo-profiles/<owner>__<repo>/tech-agents.json — sibling of the
// repo-agents.json file. The TUI exposes CRUD; the runner loads briefs at
// review time and threads them all into every specialist's user prompt.
//
// Cross-package shape mirrors internal/review/repoagents on purpose so the
// TUI, runner, and freshness UI can share idioms.
package techagents

import (
	"sort"
	"strings"
	"time"
)

// Agent is one technology's brief for a repo: a markdown context document
// plus metadata describing how it was produced. The Tech field is the
// canonical (lowercased, slugified) key; Label is the human-friendly form
// the user originally typed (e.g. Kestra). Seed is the short user-supplied
// description that fed the generator.
type Agent struct {
	Tech        string    `json:"tech"`
	Label       string    `json:"label,omitempty"`
	Seed        string    `json:"seed,omitempty"`
	Context     string    `json:"context"`
	GeneratedAt time.Time `json:"generated_at,omitempty"`
	Manual      bool      `json:"manual,omitempty"`
	Model       string    `json:"model,omitempty"`
	SourceHash  string    `json:"source_hash,omitempty"`
	Provider    string    `json:"provider,omitempty"`
}

// TechAgents is the on-disk shape: a repo's tech experts keyed by
// canonical tech name.
type TechAgents struct {
	Owner  string           `json:"owner"`
	Repo   string           `json:"repo"`
	Agents map[string]Agent `json:"agents"`
}

// CanonicalTech lowercases and slugifies a tech identifier so on-disk
// keys are stable regardless of how the user typed the name. "Kestra",
// "kestra", and " Kestra " all canonicalise to "kestra".
func CanonicalTech(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

// Get returns the agent for a tech (or zero Agent + false). Lookups are
// canonicalised so callers don't have to remember exact casing.
func (t *TechAgents) Get(tech string) (Agent, bool) {
	if t == nil {
		return Agent{}, false
	}
	c := CanonicalTech(tech)
	if c == "" {
		return Agent{}, false
	}
	a, ok := t.Agents[c]
	return a, ok
}

// Set stores an agent under its canonical tech key. The Agent's Tech
// field is normalised to the canonical form.
func (t *TechAgents) Set(tech string, a Agent) {
	if t.Agents == nil {
		t.Agents = make(map[string]Agent)
	}
	c := CanonicalTech(tech)
	if c == "" {
		return
	}
	a.Tech = c
	if strings.TrimSpace(a.Label) == "" {
		a.Label = strings.TrimSpace(tech)
	}
	t.Agents[c] = a
}

// Delete removes a tech entry. No-op when absent.
func (t *TechAgents) Delete(tech string) {
	if t == nil || t.Agents == nil {
		return
	}
	delete(t.Agents, CanonicalTech(tech))
}

// SortedTechs returns the tech keys present, sorted alphabetically. Stable
// ordering keeps the TUI rows and the injected prompt sections deterministic.
func (t *TechAgents) SortedTechs() []string {
	if t == nil {
		return nil
	}
	out := make([]string, 0, len(t.Agents))
	for k := range t.Agents {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ContextFor returns the trimmed context body for a tech (or "" if absent).
func (t *TechAgents) ContextFor(tech string) string {
	a, ok := t.Get(tech)
	if !ok {
		return ""
	}
	return strings.TrimSpace(a.Context)
}

// LabelFor returns the human-friendly label for a tech, falling back to
// the canonical key when no label was stored.
func (t *TechAgents) LabelFor(tech string) string {
	a, ok := t.Get(tech)
	if !ok {
		return CanonicalTech(tech)
	}
	if l := strings.TrimSpace(a.Label); l != "" {
		return l
	}
	return CanonicalTech(tech)
}

// HasAny reports whether at least one agent context is populated.
func (t *TechAgents) HasAny() bool {
	if t == nil {
		return false
	}
	for _, a := range t.Agents {
		if strings.TrimSpace(a.Context) != "" {
			return true
		}
	}
	return false
}
