package langagents

import "strings"

// Load returns the cached brief for lang as an Agent. Returns
// (zero Agent, false) when no brief is cached (the caller can then
// surface "missing language brief" and offer to generate one).
//
// There is no bundled fallback: all per-language briefs are
// LLM-generated and live under <cache>/lang-agents/. The generator
// system prompt at prompts/lang-generator.md (embedded in the binary)
// drives Generate; that's the only language-related asset shipping in
// the codebase.
func Load(lang Language) (Agent, bool) {
	c := Canonical(lang)
	if c == "" {
		return Agent{}, false
	}
	cache, err := LoadCache()
	if err != nil || cache == nil {
		return Agent{}, false
	}
	if a, ok := cache.Get(c); ok && strings.TrimSpace(a.Context) != "" {
		return a, true
	}
	return Agent{}, false
}

// ContextFor returns the cached brief body for lang or "" when none is
// cached. Convenience for prompt assembly.
func ContextFor(lang Language) string {
	a, ok := Load(lang)
	if !ok {
		return ""
	}
	return strings.TrimSpace(a.Context)
}

// AllAvailable returns the canonical language names for which a brief
// is currently cached, alphabetically. Used by the TUI to render the
// "what's available right now" row set; languages without a cached
// brief don't appear here — they're discovered lazily via the TUI's
// "add language" affordance or by reviewing a PR that touches them.
func AllAvailable() ([]Language, error) {
	cache, err := LoadCache()
	if err != nil {
		return nil, err
	}
	if cache == nil {
		return nil, nil
	}
	return cache.SortedLanguages(), nil
}
