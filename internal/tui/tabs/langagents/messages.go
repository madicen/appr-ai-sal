package langagents

import (
	la "github.com/madicen/appr-ai-sal/internal/review/langagents"
	"github.com/madicen/appr-ai-sal/internal/tui/util/async"
)

// cacheLoadedMsg delivers the user-global lang-agents cache to the model.
// It's a bulk data load rather than a per-row lifecycle event, so it stays a
// named struct instead of an async.Result instantiation.
type cacheLoadedMsg struct {
	Cache *la.LangAgents
	Err   error
}

// deleted is the marker payload for a delete completion, so deleteDoneMsg is a
// distinct Go type from any other err-only result keyed by language.
type deleted struct{}

// Per-row async lifecycle messages, keyed by language, via the shared
// async.Started / async.Result generics (see internal/tui/util/async).
type (
	regenStartedMsg = async.Started[la.Language]
	regenDoneMsg    = async.Result[la.Language, *la.Agent]
	deleteDoneMsg   = async.Result[la.Language, deleted]
)
