// Package repoagents stores and generates per-repo, per-specialist context
// briefs ("repo agents") that get injected into specialist review prompts.
//
// One brief per (repo, specialist) is cached on disk under
// <cache>/repo-profiles/<owner>__<repo>/repo-agents.json. The TUI exposes
// CRUD; the runner loads briefs at review time and threads each into the
// matching specialist's user prompt.
package repoagents

import (
	"sort"
	"strings"
	"time"
)

// Specialists is the fixed set of specialists that have a paired repo agent.
// It must mirror review.AllSpecialists. Vibe-coach is intentionally excluded —
// it is a synthesis pass, not a code reviewer.
var Specialists = []string{
	"formatting",
	"design",
	"testing",
	"docs",
	"security",
}

// IsKnownSpecialist reports whether name matches one of Specialists.
func IsKnownSpecialist(name string) bool {
	for _, s := range Specialists {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

// Agent is one (repo, specialist) brief: a markdown context document plus
// metadata describing how it was produced.
type Agent struct {
	Specialist  string    `json:"specialist"`
	Context     string    `json:"context"`
	GeneratedAt time.Time `json:"generated_at,omitempty"`
	Manual      bool      `json:"manual,omitempty"`
	Model       string    `json:"model,omitempty"`
	SourceHash  string    `json:"source_hash,omitempty"`
	Provider    string    `json:"provider,omitempty"`
}

// RepoAgents is the on-disk shape: a repo's agents keyed by specialist name.
type RepoAgents struct {
	Owner  string           `json:"owner"`
	Repo   string           `json:"repo"`
	Agents map[string]Agent `json:"agents"`
}

// Get returns the agent for a specialist (or zero Agent + false).
func (r *RepoAgents) Get(specialist string) (Agent, bool) {
	if r == nil {
		return Agent{}, false
	}
	a, ok := r.Agents[strings.ToLower(strings.TrimSpace(specialist))]
	return a, ok
}

// Set stores an agent under specialist key.
func (r *RepoAgents) Set(specialist string, a Agent) {
	if r.Agents == nil {
		r.Agents = make(map[string]Agent)
	}
	r.Agents[strings.ToLower(strings.TrimSpace(specialist))] = a
}

// SortedSpecialists returns specialist keys present, sorted to match Specialists order.
func (r *RepoAgents) SortedSpecialists() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.Agents))
	for k := range r.Agents {
		out = append(out, k)
	}
	rank := func(s string) int {
		for i, sp := range Specialists {
			if strings.EqualFold(sp, s) {
				return i
			}
		}
		return len(Specialists)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

// ContextFor returns the stored context body for a specialist (or "" if absent).
func (r *RepoAgents) ContextFor(specialist string) string {
	a, ok := r.Get(specialist)
	if !ok {
		return ""
	}
	return strings.TrimSpace(a.Context)
}

// HasAny reports whether at least one agent context is populated.
func (r *RepoAgents) HasAny() bool {
	if r == nil {
		return false
	}
	for _, a := range r.Agents {
		if strings.TrimSpace(a.Context) != "" {
			return true
		}
	}
	return false
}

// NormalizeRepoKey lowercases owner/repo and strips any github.com prefix.
func NormalizeRepoKey(owner, repo string) string {
	o := strings.ToLower(strings.TrimSpace(owner))
	r := strings.ToLower(strings.TrimSpace(repo))
	return o + "/" + r
}
