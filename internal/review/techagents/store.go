package techagents

import (
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
)

// store is the shared per-repo JSON store. Layout:
// <cache>/repo-profiles/<owner>__<repo>/tech-agents.json — sibling of the
// repo-agents.json file. Slugging, atomic writes, listing, and deletion live
// in agentstore; this package only supplies the domain shape and its
// canonical-tech key normalization.
var store = agentstore.Store[TechAgents]{
	Subdir:   "repo-profiles",
	FileName: "tech-agents.json",
	New: func(owner, repo string) *TechAgents {
		return &TechAgents{Owner: owner, Repo: repo, Agents: map[string]Agent{}}
	},
	Clean: cleanTechAgents,
}

// cleanTechAgents returns a normalized copy safe to marshal: owner/repo are
// lower-cased (falling back to the args when blank) and every agent is keyed
// by its canonical tech name with Label defaulted from the key.
func cleanTechAgents(doc *TechAgents, owner, repo string) *TechAgents {
	out := &TechAgents{
		Owner:  firstNonEmpty(strings.ToLower(strings.TrimSpace(doc.Owner)), owner),
		Repo:   firstNonEmpty(strings.ToLower(strings.TrimSpace(doc.Repo)), repo),
		Agents: map[string]Agent{},
	}
	for k, v := range doc.Agents {
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
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// CacheDir returns the on-disk root for repo-profile caches.
func CacheDir() string { return store.Dir() }

// FilePath returns the absolute path to the tech-agents file for owner/repo.
func FilePath(owner, repo string) string { return store.FilePath(owner, repo) }

// Load reads stored tech agents for owner/repo. Returns an empty TechAgents
// (no error) when the file does not exist; first-run is a normal state.
func Load(owner, repo string) (*TechAgents, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = strings.ToLower(strings.TrimSpace(repo))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("techagents.Load: empty owner/repo")
	}
	return store.Load(owner, repo)
}

// Save writes ta atomically to disk. Caller sets Owner/Repo before calling.
func Save(ta *TechAgents) error {
	if ta == nil {
		return fmt.Errorf("techagents.Save: nil TechAgents")
	}
	owner := strings.ToLower(strings.TrimSpace(ta.Owner))
	repo := strings.ToLower(strings.TrimSpace(ta.Repo))
	if owner == "" || repo == "" {
		return fmt.Errorf("techagents.Save: empty owner/repo")
	}
	return store.Save(owner, repo, ta)
}

// SaveAgent merges a single tech agent into the on-disk file for owner/repo,
// creating the file if needed. Other techs are preserved.
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
func DeleteRepo(owner, repo string) error { return store.DeleteRepo(owner, repo) }

// ListRepos returns owner/repo keys (lowercased) discovered under CacheDir
// that have a tech-agents.json present, sorted alphabetically.
func ListRepos() ([]string, error) { return store.ListRepos() }
