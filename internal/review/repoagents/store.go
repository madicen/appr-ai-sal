package repoagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CacheDir returns the on-disk root for repo-agent profiles. Mirrors
// review.RepoProfilesDir; duplicated here so this package has no dependency
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

// FilePath returns the absolute path to the repo-agents file for owner/repo.
func FilePath(owner, repo string) string {
	dir := repoDir(owner, repo)
	return filepath.Join(dir, "repo-agents.json")
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

// Load reads stored agents for owner/repo. Returns an empty RepoAgents (no
// error) when the file does not exist; that is a normal first-run state.
func Load(owner, repo string) (*RepoAgents, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = strings.ToLower(strings.TrimSpace(repo))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("repoagents.Load: empty owner/repo")
	}
	out := &RepoAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	p := FilePath(owner, repo)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var raw RepoAgents
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
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		v.Specialist = key
		out.Agents[key] = v
	}
	return out, nil
}

// Save writes ra atomically to disk under FilePath(ra.Owner, ra.Repo).
// Caller is expected to set Owner/Repo before calling.
func Save(ra *RepoAgents) error {
	if ra == nil {
		return fmt.Errorf("repoagents.Save: nil RepoAgents")
	}
	owner := strings.ToLower(strings.TrimSpace(ra.Owner))
	repo := strings.ToLower(strings.TrimSpace(ra.Repo))
	if owner == "" || repo == "" {
		return fmt.Errorf("repoagents.Save: empty owner/repo")
	}
	if ra.Agents == nil {
		ra.Agents = map[string]Agent{}
	}
	dir := repoDir(owner, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	clean := RepoAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	for k, v := range ra.Agents {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		v.Specialist = key
		clean.Agents[key] = v
	}
	b, err := json.MarshalIndent(&clean, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal repo-agents: %w", err)
	}
	b = append(b, '\n')
	p := FilePath(owner, repo)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, p)
}

// SaveAgent merges a single agent into the on-disk file for owner/repo,
// creating the file if needed. Other specialists' entries are preserved.
func SaveAgent(owner, repo string, a Agent) error {
	ra, err := Load(owner, repo)
	if err != nil {
		return err
	}
	if ra == nil {
		ra = &RepoAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	}
	ra.Owner = strings.ToLower(strings.TrimSpace(owner))
	ra.Repo = strings.ToLower(strings.TrimSpace(repo))
	ra.Set(a.Specialist, a)
	return Save(ra)
}

// Delete removes one specialist's entry from owner/repo. Removing the last
// entry leaves an empty map (the file remains so users can see the repo is
// tracked); use DeleteRepo to remove the whole file.
func Delete(owner, repo, specialist string) error {
	ra, err := Load(owner, repo)
	if err != nil {
		return err
	}
	if ra == nil || ra.Agents == nil {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(specialist))
	if _, ok := ra.Agents[key]; !ok {
		return nil
	}
	delete(ra.Agents, key)
	return Save(ra)
}

// DeleteRepo removes the entire repo-agents file (and the empty parent dir).
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

// ListRepos returns owner/repo keys (lowercased) discovered under CacheDir.
// Skips directories that don't contain a repo-agents.json.
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
		name := e.Name()
		owner, repo, ok := splitSlug(name)
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
