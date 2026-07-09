package styles

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

var (
	// DimStyle renders muted ancillary text (timestamps, hints, captions).
	DimStyle = lipgloss.NewStyle().Foreground(theme.Adaptive(theme.RoleMuted))
	// BoldStyle renders bold without colour change.
	BoldStyle = lipgloss.NewStyle().Bold(true)
	// ErrStyle renders a bold red used by error overlays and inline error
	// captions.
	ErrStyle = lipgloss.NewStyle().Foreground(theme.Adaptive(theme.RoleError)).Bold(true)
	// WarnStyle renders amber, used by warning captions and the gutter
	// "+" marker on inline diffs.
	WarnStyle = lipgloss.NewStyle().Foreground(theme.Adaptive(theme.RoleWarning))
	// OkStyle renders green; used for success captions and accept buttons.
	OkStyle = lipgloss.NewStyle().Foreground(theme.Adaptive(theme.RoleSuccess))

	// OkColor / WarnColor / DimColor expose the raw palette colours behind
	// OkStyle / WarnStyle / DimStyle for the few call sites that need a
	// colour directly (e.g. a border foreground) rather than a full style.
	// They are AdaptiveColors so they honour the active appearance just like
	// the styles above.
	OkColor   = theme.Adaptive(theme.RoleSuccess)
	WarnColor = theme.Adaptive(theme.RoleWarning)
	DimColor  = theme.Adaptive(theme.RoleMuted)
)
