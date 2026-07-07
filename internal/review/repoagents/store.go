package repoagents

import (
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
)

// store is the shared per-repo JSON store. Layout:
// <cache>/repo-profiles/<owner>__<repo>/repo-agents.json. All slugging,
// atomic writes, listing, and deletion live in agentstore; this package only
// supplies the domain shape and its key normalization.
var store = agentstore.Store[RepoAgents]{
	Subdir:   "repo-profiles",
	FileName: "repo-agents.json",
	New: func(owner, repo string) *RepoAgents {
		return &RepoAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	},
	Clean: cleanRepoAgents,
}

// cleanRepoAgents returns a normalized copy safe to marshal: owner/repo are
// lower-cased (falling back to the args when blank) and every agent is keyed
// by its lower-cased specialist name.
func cleanRepoAgents(doc *RepoAgents, owner, repo string) *RepoAgents {
	out := &RepoAgents{
		Owner:  firstNonEmpty(strings.ToLower(strings.TrimSpace(doc.Owner)), owner),
		Repo:   firstNonEmpty(strings.ToLower(strings.TrimSpace(doc.Repo)), repo),
		Agents: map[string]Agent{},
	}
	for k, v := range doc.Agents {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		v.Specialist = key
		out.Agents[key] = v
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// CacheDir returns the on-disk root for repo-agent profiles.
func CacheDir() string { return store.Dir() }

// FilePath returns the absolute path to the repo-agents file for owner/repo.
func FilePath(owner, repo string) string { return store.FilePath(owner, repo) }

// Load reads stored agents for owner/repo. Returns an empty RepoAgents (no
// error) when the file does not exist; that is a normal first-run state.
func Load(owner, repo string) (*RepoAgents, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = strings.ToLower(strings.TrimSpace(repo))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("repoagents.Load: empty owner/repo")
	}
	return store.Load(owner, repo)
}

// Save writes ra atomically to disk. Caller sets Owner/Repo before calling.
func Save(ra *RepoAgents) error {
	if ra == nil {
		return fmt.Errorf("repoagents.Save: nil RepoAgents")
	}
	owner := strings.ToLower(strings.TrimSpace(ra.Owner))
	repo := strings.ToLower(strings.TrimSpace(ra.Repo))
	if owner == "" || repo == "" {
		return fmt.Errorf("repoagents.Save: empty owner/repo")
	}
	return store.Save(owner, repo, ra)
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
func DeleteRepo(owner, repo string) error { return store.DeleteRepo(owner, repo) }

// ListRepos returns owner/repo keys (lowercased) discovered under CacheDir
// that have a repo-agents.json present, sorted alphabetically.
func ListRepos() ([]string, error) { return store.ListRepos() }
