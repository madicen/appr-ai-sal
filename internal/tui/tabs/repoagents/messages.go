package repoagents

import (
	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	ta "github.com/madicen/appr-ai-sal/internal/review/techagents"
	"github.com/madicen/appr-ai-sal/internal/tui/util/async"
)

// Keys identify the row an async operation belongs to. specKey and techKey
// address one agent within a repo; repoKey addresses a whole repo.
type (
	specKey struct{ Owner, Repo, Specialist string }
	techKey struct{ Owner, Repo, Tech string }
	repoKey struct{ Owner, Repo string }
)

// Marker payloads for err-only completions. They give delete / save / remove
// distinct Go types even though they carry no value, so the Update type
// switch can still dispatch them (a plain async.Result[K, struct{}] would make
// delete and save the same type).
type (
	deleted struct{}
	saved   struct{}
	removed struct{}
)

// reposLoadedMsg delivers the discovered repo list to the model.
type reposLoadedMsg struct {
	Repos []string
	Err   error
}

// agentsLoadedMsg delivers the per-repo Agents map for owner/repo.
type agentsLoadedMsg struct {
	Owner string
	Repo  string
	RA    *ra.RepoAgents
	Err   error
}

// techsLoadedMsg delivers the per-repo TechAgents map for owner/repo.
type techsLoadedMsg struct {
	Owner string
	Repo  string
	TA    *ta.TechAgents
	Err   error
}

// Per-row async lifecycle messages, via the shared async.Started /
// async.Result generics (see internal/tui/util/async). The Specialist agent
// family and the Tech-expert family mirror each other; their key types keep
// the instantiations (and thus the type-switch cases) distinct.
type (
	// Specialist agents (keyed by owner/repo/specialist).
	regenStartedMsg = async.Started[specKey]
	regenDoneMsg    = async.Result[specKey, *ra.Agent]
	deletedMsg      = async.Result[specKey, deleted]
	savedMsg        = async.Result[specKey, saved]

	// Whole-repo operations (keyed by owner/repo).
	repoRemovedMsg        = async.Result[repoKey, removed]
	techSuggestStartedMsg = async.Started[repoKey]
	techSuggestDoneMsg    = async.Result[repoKey, []ta.Candidate]

	// Tech-expert agents (keyed by owner/repo/tech).
	techRegenStartedMsg = async.Started[techKey]
	techRegenDoneMsg    = async.Result[techKey, *ta.Agent]
	techDeletedMsg      = async.Result[techKey, deleted]
	techSavedMsg        = async.Result[techKey, saved]
)
