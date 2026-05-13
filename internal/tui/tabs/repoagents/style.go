package repoagents

import "github.com/charmbracelet/lipgloss"

// Local styles mirror internal/tui/styles.go so this package does not import tui.
var (
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	boldStyle = lipgloss.NewStyle().Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0AF68"))
	chipStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#30363d")).
			Padding(0, 1).
			Bold(true)
	chipBusy = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7AA2F7")).
			Padding(0, 1).
			Bold(true)
	chipPrimary = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5D2D91")).
			Padding(0, 1).
			Bold(true)
	chipDanger = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7A1F1F")).
			Padding(0, 1).
			Bold(true)
	sectionRule = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#666666", Dark: "#9A9A9A"})
)
