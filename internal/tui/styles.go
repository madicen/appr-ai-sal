package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Layout
	appPadding = lipgloss.NewStyle().Padding(0, 1)

	// Header bar at the top
	headerBar = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5D2D91")).
			Padding(0, 1)

	// Status bar at the bottom
	statusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Padding(0, 1)

	// Panel borders for the two-pane layout (high enough contrast to read as real boxes).
	panelBorder = lipgloss.AdaptiveColor{Light: "#666666", Dark: "#9A9A9A"}

	leftPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(panelBorder).
			Padding(0, 1)

	rightPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(panelBorder).
			Padding(0, 1)

	// One-line strip above each scroll viewport in detail / staged modes.
	detailPaneTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#BBBBBB"}).
				Background(lipgloss.AdaptiveColor{Light: "#E8E8E8", Dark: "#2C2C2C"}).
				Padding(0, 0)

	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#333333", Dark: "#CCCCCC"}).
				MarginTop(1).
				MarginBottom(0)

	diffFrameStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(panelBorder).
			Padding(0, 1)

	// Specialist tags shown next to comments
	tagFormatting = tagStyle("#7AA2F7") // blue
	tagDesign     = tagStyle("#BB9AF7") // purple
	tagTesting    = tagStyle("#9ECE6A") // green
	tagDocs       = tagStyle("#E0AF68") // yellow
	tagSecurity   = tagStyle("#F7768E") // red
	tagVibeCoach  = tagStyle("#7DCFFF") // cyan

	// Severity styles for the inline finding lines
	sevInfo     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7"))
	sevWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0AF68"))
	sevError    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
	sevCritical = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).Bold(true)

	// Misc text styles
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	boldStyle = lipgloss.NewStyle().Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0AF68"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A"))
	// Modal actions (jj-tui-style Copy / secondary buttons).
	modalButtonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#30363d")).
				Padding(0, 1).
				Bold(true)
)

func tagStyle(hex string) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color(hex)).
		Padding(0, 1).
		Bold(true)
}

func renderTag(specialist string) string {
	switch specialist {
	case "formatting":
		return tagFormatting.Render(specialist)
	case "design":
		return tagDesign.Render(specialist)
	case "testing":
		return tagTesting.Render(specialist)
	case "docs":
		return tagDocs.Render(specialist)
	case "security":
		return tagSecurity.Render(specialist)
	case "vibe-coach":
		return tagVibeCoach.Render(specialist)
	default:
		return lipgloss.NewStyle().Padding(0, 1).Render(specialist)
	}
}

func renderSeverity(sev string) string {
	switch sev {
	case "info":
		return sevInfo.Render("info")
	case "warning":
		return sevWarning.Render("warn")
	case "error":
		return sevError.Render("ERROR")
	case "critical":
		return sevCritical.Render("CRITICAL")
	default:
		return dimStyle.Render(sev)
	}
}
