package styles

import "github.com/charmbracelet/lipgloss"

var (
	// DimStyle renders muted ancillary text (timestamps, hints, captions).
	DimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	// BoldStyle renders bold without colour change.
	BoldStyle = lipgloss.NewStyle().Bold(true)
	// ErrStyle renders a bold red used by error overlays and inline error
	// captions.
	ErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
	// WarnStyle renders amber, used by warning captions and the gutter
	// "+" marker on inline diffs.
	WarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0AF68"))
	// OkStyle renders green; used for success captions and accept buttons.
	OkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A"))
)
