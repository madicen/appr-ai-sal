package repoagents

import (
	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
)

// DoneMsg signals the root TUI that this tab should close. Cancelled is
// purely informational (the tab has no save-on-close concept; mutations are
// persisted as they happen).
type DoneMsg struct {
	Cancelled bool
	Err       error
}

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
