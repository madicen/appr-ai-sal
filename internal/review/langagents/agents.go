// Package langagents stores and generates per-language convention briefs
// that are injected into specialist review prompts.
//
// Unlike repoagents, the unit is a LANGUAGE not a repo: Go conventions are
// the same across every Go repo. Mirroring how repoagents works, this
// package ships only the GENERATOR system prompt (prompts/lang-generator.md);
// the per-language briefs themselves are LLM-authored and stored in a
// user-global cache under <cache>/lang-agents/. Users add, refresh, and
// delete briefs at their whim through the TUI tab.
//
// The deterministic convention gate (internal/review/convention_gate.go)
// consults langagents.Table for the small set of universally-agreed
// naming conventions (Go: MixedCaps, Python: snake_case, etc.). That
// table is a static safety net independent of the cached briefs — even
// a fresh install with no generated briefs still strips wrong-case
// suggestions for the languages Table covers.
//
// Cross-package shape mirrors internal/review/repoagents/ on purpose:
// the runner, TUI, and freshness UI all benefit from the parallel.
package langagents

import (
	"sort"
	"strings"
	"time"
)

// Language is the canonical lowercased identifier used throughout this
// package — "go", "python", "typescript", etc. Values are stable across
// versions; aliases (".tsx" -> "typescript", "js" -> "typescript") all
// resolve to the canonical name via Canonical.
type Language = string

// Agent is one language's brief plus provenance metadata. All Agents are
// LLM-generated and cached on disk; there is no bundled-brief concept.
// Provider/Model/SourceHash track the generation run that produced the
// brief so the freshness UI can decide when to nag for a refresh.
type Agent struct {
	Language    Language  `json:"language"`
	Context     string    `json:"context"`
	GeneratedAt time.Time `json:"generated_at,omitempty"`
	Manual      bool      `json:"manual,omitempty"`
	Model       string    `json:"model,omitempty"`
	SourceHash  string    `json:"source_hash,omitempty"`
	Provider    string    `json:"provider,omitempty"`
}

// LangAgents is the on-disk shape of the user-global cache. Languages
// are keyed by their canonical name.
type LangAgents struct {
	Agents map[Language]Agent `json:"agents"`
}

// Get returns the Agent for a language (or zero + false). Lookups are
// canonicalised so callers don't have to remember whether to pass "ts"
// or "typescript".
func (l *LangAgents) Get(lang Language) (Agent, bool) {
	if l == nil {
		return Agent{}, false
	}
	c := Canonical(lang)
	if c == "" {
		return Agent{}, false
	}
	a, ok := l.Agents[c]
	return a, ok
}

// Set stores an agent under the canonical language key.
func (l *LangAgents) Set(lang Language, a Agent) {
	if l.Agents == nil {
		l.Agents = make(map[Language]Agent)
	}
	c := Canonical(lang)
	if c == "" {
		return
	}
	a.Language = c
	l.Agents[c] = a
}

// SortedLanguages returns the language keys present in the cache,
// alphabetically.
func (l *LangAgents) SortedLanguages() []Language {
	if l == nil {
		return nil
	}
	out := make([]Language, 0, len(l.Agents))
	for k := range l.Agents {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ContextFor returns the trimmed context body for a language (or "" if
// absent). Convenience for code that already has a LangAgents in hand;
// most callers should use Load instead.
func (l *LangAgents) ContextFor(lang Language) string {
	a, ok := l.Get(lang)
	if !ok {
		return ""
	}
	return strings.TrimSpace(a.Context)
}

// HasAny reports whether at least one cached agent context is populated.
func (l *LangAgents) HasAny() bool {
	if l == nil {
		return false
	}
	for _, a := range l.Agents {
		if strings.TrimSpace(a.Context) != "" {
			return true
		}
	}
	return false
}
