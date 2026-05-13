package langagents

import (
	la "github.com/madicen/appr-ai-sal/internal/review/langagents"
)

// cacheLoadedMsg delivers the user-global lang-agents cache to the model.
type cacheLoadedMsg struct {
	Cache *la.LangAgents
	Err   error
}

// regenStartedMsg is emitted when a regenerate command is dispatched so
// the row can show a "running…" badge immediately.
type regenStartedMsg struct {
	Language la.Language
}

// regenDoneMsg is emitted when a regenerate command completes (success
// or failure).
type regenDoneMsg struct {
	Language la.Language
	Agent    *la.Agent
	Err      error
}

// deleteDoneMsg is emitted when a cached entry has been removed (or the
// remove failed).
type deleteDoneMsg struct {
	Language la.Language
	Err      error
}
