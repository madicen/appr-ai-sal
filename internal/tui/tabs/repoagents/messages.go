package repoagents

import (
	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	ta "github.com/madicen/appr-ai-sal/internal/review/techagents"
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

// regenStartedMsg is emitted when a regenerate command is dispatched (for
// surfacing per-row "regenerating…" state immediately).
type regenStartedMsg struct {
	Owner      string
	Repo       string
	Specialist string
}

// regenDoneMsg is emitted when a regenerate command completes.
type regenDoneMsg struct {
	Owner      string
	Repo       string
	Specialist string
	Agent      *ra.Agent
	Err        error
}

// deletedMsg is emitted when a delete-agent command completes.
type deletedMsg struct {
	Owner      string
	Repo       string
	Specialist string
	Err        error
}

// savedMsg is emitted when a manual edit of an agent body is persisted.
type savedMsg struct {
	Owner      string
	Repo       string
	Specialist string
	Err        error
}

// repoRemovedMsg is emitted when the user deletes the entire repo file.
type repoRemovedMsg struct {
	Owner string
	Repo  string
	Err   error
}

// techsLoadedMsg delivers the per-repo TechAgents map for owner/repo.
type techsLoadedMsg struct {
	Owner string
	Repo  string
	TA    *ta.TechAgents
	Err   error
}

// techRegenStartedMsg is emitted when a tech regenerate command is dispatched.
type techRegenStartedMsg struct {
	Owner string
	Repo  string
	Tech  string
}

// techRegenDoneMsg is emitted when a tech regenerate command completes.
type techRegenDoneMsg struct {
	Owner string
	Repo  string
	Tech  string
	Agent *ta.Agent
	Err   error
}

// techDeletedMsg is emitted when a delete-tech command completes.
type techDeletedMsg struct {
	Owner string
	Repo  string
	Tech  string
	Err   error
}

// techSavedMsg is emitted when a manual tech-brief edit is persisted.
type techSavedMsg struct {
	Owner string
	Repo  string
	Tech  string
	Err   error
}

// techSuggestStartedMsg is emitted when a suggest-technologies command is
// dispatched so the panel can show its "analyzing…" state immediately.
type techSuggestStartedMsg struct {
	Owner string
	Repo  string
}

// techSuggestDoneMsg delivers the suggested technology candidates for
// owner/repo (or an error, e.g. ta.ErrNoRepoAccess).
type techSuggestDoneMsg struct {
	Owner      string
	Repo       string
	Candidates []ta.Candidate
	Err        error
}
