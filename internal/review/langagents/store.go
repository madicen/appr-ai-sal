package langagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CacheDir returns the on-disk root for the language-agent cache. Unlike
// repoagents (which is per-repo), the lang-agents cache is user-global —
// "you generated lang-swift once, every repo benefits."
//
// Layout: $APPR_AI_SAL_CACHE_DIR / appr-ai-sal / lang-agents
// (default: ~/.cache/appr-ai-sal/lang-agents)
//
// Mirrors the env-var resolution in repoagents.CacheDir so users only
// have to set one variable to relocate the whole cache tree.
func CacheDir() string {
	if v := os.Getenv("APPR_AI_SAL_CACHE_DIR"); v != "" {
		return filepath.Clean(filepath.Join(v, "..", "lang-agents"))
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "appr-ai-sal", "lang-agents")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", "appr-ai-sal", "lang-agents")
	}
	return filepath.Join(home, ".cache", "appr-ai-sal", "lang-agents")
}

// FilePath returns the on-disk path that stores the cache JSON for ALL
// non-bundled languages. We use one file (not per-language files) so
// adding a new language doesn't fragment the cache and so the user can
// rsync / back up a single artefact.
func FilePath() string {
	return filepath.Join(CacheDir(), "lang-agents.json")
}

// LoadCache reads the user-global cache. Returns an empty LangAgents (no
// error) when the file does not exist or is malformed beyond recovery —
// a corrupt cache should never block reviews; the worst case is "fall
// back to bundled briefs and offer to regenerate."
func LoadCache() (*LangAgents, error) {
	out := &LangAgents{Agents: map[Language]Agent{}}
	p := FilePath()
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var raw LangAgents
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	out.Agents = map[Language]Agent{}
	for k, v := range raw.Agents {
		c := Canonical(k)
		if c == "" {
			// Forward-compat: keep entries for languages we don't
			// recognise yet, in case the binary was rolled back.
			c = strings.ToLower(strings.TrimSpace(k))
			if c == "" {
				continue
			}
		}
		v.Language = c
		out.Agents[c] = v
	}
	return out, nil
}

// SaveCache writes l atomically to disk under FilePath().
func SaveCache(l *LangAgents) error {
	if l == nil {
		return fmt.Errorf("langagents.SaveCache: nil LangAgents")
	}
	if l.Agents == nil {
		l.Agents = map[Language]Agent{}
	}
	dir := CacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Clean copy so we don't surprise callers by mutating their map keys.
	clean := LangAgents{Agents: map[Language]Agent{}}
	for k, v := range l.Agents {
		c := Canonical(k)
		if c == "" {
			c = strings.ToLower(strings.TrimSpace(k))
			if c == "" {
				continue
			}
		}
		v.Language = c
		clean.Agents[c] = v
	}
	b, err := json.MarshalIndent(&clean, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lang-agents: %w", err)
	}
	b = append(b, '\n')
	p := FilePath()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, p)
}

// SaveAgent merges a single Agent into the user-global cache. Other
// languages' entries are preserved.
func SaveAgent(a Agent) error {
	l, err := LoadCache()
	if err != nil {
		return err
	}
	if l == nil {
		l = &LangAgents{Agents: map[Language]Agent{}}
	}
	c := Canonical(a.Language)
	if c == "" {
		return fmt.Errorf("langagents.SaveAgent: unknown language %q", a.Language)
	}
	a.Language = c
	l.Agents[c] = a
	return SaveCache(l)
}

// DeleteAgent removes one language's cached entry.
func DeleteAgent(lang Language) error {
	l, err := LoadCache()
	if err != nil {
		return err
	}
	if l == nil || l.Agents == nil {
		return nil
	}
	c := Canonical(lang)
	if c == "" {
		return nil
	}
	if _, ok := l.Agents[c]; !ok {
		return nil
	}
	delete(l.Agents, c)
	return SaveCache(l)
}

// ListCached returns the languages currently present in the user-global
// cache, sorted to match the order BundledLanguages uses (with
// alphabetical fallback for languages outside that list).
func ListCached() ([]Language, error) {
	l, err := LoadCache()
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, nil
	}
	return l.SortedLanguages(), nil
}
