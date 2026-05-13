package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

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

	// One-line strip above each scroll viewport in detail / staged modes.
	detailPaneTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#BBBBBB"}).
				Background(lipgloss.AdaptiveColor{Light: "#E8E8E8", Dark: "#2C2C2C"}).
				Padding(0, 0)

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

// tagStyle returns a row-tag style for the given hex background. Tag colours
// come from the runtime theme (theme.Color), so styles are rebuilt on every
// renderTag call rather than baked into package globals.
func tagStyle(hex string) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color(hex)).
		Padding(0, 1).
		Bold(true)
}

// Severity styles are rebuilt per call so they reflect any live theme
// override. The variables below preserve the historical call-site shape
// (sevWarning.Render("+ ")) without freezing the colour at startup.
func sevStyle(k theme.Key, bold bool) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Color(k)))
	if bold {
		s = s.Bold(true)
	}
	return s
}

type severityStyle struct {
	key  theme.Key
	bold bool
}

func (s severityStyle) Render(parts ...string) string {
	return sevStyle(s.key, s.bold).Render(parts...)
}

var (
	sevInfo     = severityStyle{key: theme.KeySevInfo}
	sevWarning  = severityStyle{key: theme.KeySevWarning}
	sevError    = severityStyle{key: theme.KeySevError, bold: true}
	sevCritical = severityStyle{key: theme.KeySevCritical, bold: true}
)

func renderTag(specialist string) string {
	switch specialist {
	case "formatting":
		return tagStyle(theme.Color(theme.KeyTagFormatting)).Render(specialist)
	case "design":
		return tagStyle(theme.Color(theme.KeyTagDesign)).Render(specialist)
	case "testing":
		return tagStyle(theme.Color(theme.KeyTagTesting)).Render(specialist)
	case "docs":
		return tagStyle(theme.Color(theme.KeyTagDocs)).Render(specialist)
	case "security":
		return tagStyle(theme.Color(theme.KeyTagSecurity)).Render(specialist)
	case "vibe-coach":
		return tagStyle(theme.Color(theme.KeyTagVibeCoach)).Render(specialist)
	case "language-briefs":
		return tagStyle(theme.Color(theme.KeyTagLangBriefs)).Render("language briefs")
	case "tech-experts":
		return tagStyle(theme.Color(theme.KeyTagTechExperts)).Render("tech experts")
	case "repo-experts":
		return tagStyle(theme.Color(theme.KeyTagRepoExperts)).Render("repo experts")
	case "repo-arbiter":
		return tagStyle(theme.Color(theme.KeyTagRepoArbiter)).Render("repo arbiter")
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
