package settings

import "github.com/charmbracelet/lipgloss"

// Local styles mirror internal/tui/styles.go so this package does not import tui.
var (
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	boldStyle = lipgloss.NewStyle().Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A"))
)
