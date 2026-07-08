package langagents

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// Styles route through the semantic palette in internal/theme so the tab
// draws from the same source as the rest of the app (Phase 5 item 7) instead
// of the ad-hoc 256-colour indices it used to carry. The status chips map to
// their semantic roles (success / info / warning / error); the tab keeps the
// same sibling feel as the repo-agents tab because both now resolve the shared
// palette, and everything degrades to monochrome together under NO_COLOR.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Adaptive(theme.RoleAccent))

	hintStyle = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleMuted)).
			Italic(true)

	statusStyle = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleFg))

	rowStyle = lipgloss.NewStyle().
			Padding(0, 1)

	rowSelectedStyle = rowStyle.
				Background(theme.Adaptive(theme.RoleSelectionBg)).
				Foreground(theme.Adaptive(theme.RoleSelectionFg))

	chipBundled = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleSuccess)).
			Bold(true)

	chipCached = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleInfo))

	chipMissing = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleError)).
			Bold(true)

	chipStale = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleWarning))

	chipBusy = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleInfo)).
			Italic(true)

	errStyle = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleError)).
			Bold(true)

	// btnStyle / btnDangerStyle render the per-row mouse action buttons
	// (Generate/Refresh and Delete) and the footer Close button so the
	// tab can be driven without the keyboard.
	btnStyle = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleOnSurface)).
			Background(theme.Adaptive(theme.RoleSurface)).
			Padding(0, 1)

	btnDangerStyle = lipgloss.NewStyle().
			Foreground(theme.Adaptive(theme.RoleOnAccent)).
			Background(theme.Adaptive(theme.RoleDanger)).
			Padding(0, 1)
)
