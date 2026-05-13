package techagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CacheDir returns the on-disk root for repo-profile caches. Mirrors
// repoagents.CacheDir; duplicated here so this package has no dependency
// on the parent review package (avoiding an import cycle).
//
// Layout: $APPR_AI_SAL_CACHE_DIR / appr-ai-sal / repo-profiles
// (default: ~/.cache/appr-ai-sal/repo-profiles)
func CacheDir() string {
	if v := os.Getenv("APPR_AI_SAL_CACHE_DIR"); v != "" {
		return filepath.Clean(filepath.Join(v, "..", "repo-profiles"))
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "appr-ai-sal", "repo-profiles")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", "appr-ai-sal", "repo-profiles")
	}
	return filepath.Join(home, ".cache", "appr-ai-sal", "repo-profiles")
}

// FilePath returns the absolute path to the tech-agents file for owner/repo.
func FilePath(owner, repo string) string {
	return filepath.Join(repoDir(owner, repo), "tech-agents.json")
}

func repoDir(owner, repo string) string {
	slug := slugify(owner) + "__" + slugify(repo)
	return filepath.Join(CacheDir(), slug)
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// Load reads stored tech agents for owner/repo. Returns an empty TechAgents
// (no error) when the file does not exist; first-run is a normal state.
func Load(owner, repo string) (*TechAgents, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = strings.ToLower(strings.TrimSpace(repo))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("techagents.Load: empty owner/repo")
	}
	out := &TechAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	p := FilePath(owner, repo)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var raw TechAgents
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	out.Owner = strings.ToLower(strings.TrimSpace(raw.Owner))
	out.Repo = strings.ToLower(strings.TrimSpace(raw.Repo))
	if out.Owner == "" {
		out.Owner = owner
	}
	if out.Repo == "" {
		out.Repo = repo
	}
	out.Agents = map[string]Agent{}
	for k, v := range raw.Agents {
		key := CanonicalTech(k)
		if key == "" {
			continue
		}
		v.Tech = key
		if strings.TrimSpace(v.Label) == "" {
			v.Label = key
		}
		out.Agents[key] = v
	}
	return out, nil
}

// Save writes ta atomically to disk under FilePath(ta.Owner, ta.Repo).
// Caller is expected to set Owner/Repo before calling.
func Save(ta *TechAgents) error {
	if ta == nil {
		return fmt.Errorf("techagents.Save: nil TechAgents")
	}
	owner := strings.ToLower(strings.TrimSpace(ta.Owner))
	repo := strings.ToLower(strings.TrimSpace(ta.Repo))
	if owner == "" || repo == "" {
		return fmt.Errorf("techagents.Save: empty owner/repo")
	}
	if ta.Agents == nil {
		ta.Agents = map[string]Agent{}
	}
	dir := repoDir(owner, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	clean := TechAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	for k, v := range ta.Agents {
		key := CanonicalTech(k)
		if key == "" {
			continue
		}
		v.Tech = key
		if strings.TrimSpace(v.Label) == "" {
			v.Label = key
		}
		clean.Agents[key] = v
	}
	b, err := json.MarshalIndent(&clean, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tech-agents: %w", err)
	}
	b = append(b, '\n')
	p := FilePath(owner, repo)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, p)
}

// SaveAgent merges a single tech agent into the on-disk file for
// owner/repo, creating the file if needed. Other techs are preserved.
func SaveAgent(owner, repo string, a Agent) error {
	ta, err := Load(owner, repo)
	if err != nil {
		return err
	}
	if ta == nil {
		ta = &TechAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	}
	ta.Owner = strings.ToLower(strings.TrimSpace(owner))
	ta.Repo = strings.ToLower(strings.TrimSpace(repo))
	ta.Set(a.Tech, a)
	return Save(ta)
}

// Delete removes one tech entry from owner/repo. Removing the last entry
// leaves an empty map (the file remains so users can see the repo is
// tracked); use DeleteRepo to remove the whole file.
func Delete(owner, repo, tech string) error {
	ta, err := Load(owner, repo)
	if err != nil {
		return err
	}
	if ta == nil || ta.Agents == nil {
		return nil
	}
	key := CanonicalTech(tech)
	if _, ok := ta.Agents[key]; !ok {
		return nil
	}
	delete(ta.Agents, key)
	return Save(ta)
}

// DeleteRepo removes the entire tech-agents file (and the empty parent dir
// when it is otherwise empty). Sibling repo-agents.json is untouched.
func DeleteRepo(owner, repo string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = strings.ToLower(strings.TrimSpace(repo))
	p := FilePath(owner, repo)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := repoDir(owner, repo)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	return nil
}

// ListRepos returns owner/repo keys (lowercased) discovered under CacheDir
// that have a tech-agents.json file present. Sorted alphabetically.
func ListRepos() ([]string, error) {
	root := CacheDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		owner, repo, ok := splitSlug(e.Name())
		if !ok {
			continue
		}
		if _, statErr := os.Stat(FilePath(owner, repo)); statErr == nil {
			out = append(out, owner+"/"+repo)
		}
	}
	sort.Strings(out)
	return out, nil
}

func splitSlug(name string) (owner, repo string, ok bool) {
	idx := strings.Index(name, "__")
	if idx <= 0 || idx >= len(name)-2 {
		return "", "", false
	}
	owner = name[:idx]
	repo = name[idx+2:]
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}
