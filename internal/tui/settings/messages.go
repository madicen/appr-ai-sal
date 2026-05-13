package settings

import "github.com/madicen/appr-ai-sal/internal/aiconfig"

// DoneMsg is returned to the root model after save, cancel, or validation error.
type DoneMsg struct {
	Cancelled bool
	Cfg       *aiconfig.Config
	Err       error
	// RepoSaved is true after repo-context.json was written from the Repo context tab.
	RepoSaved bool
	// ThemeSaved is true after theme.json was written from the Theme tab.
	ThemeSaved bool
}
