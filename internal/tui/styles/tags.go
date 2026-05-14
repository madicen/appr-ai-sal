package styles

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// TagStyle returns a row-tag style (filled background pill, white text)
// for the given hex background. Tag colours come from the runtime theme,
// so styles are rebuilt on every RenderTag call rather than baked into
// package globals at import time.
func TagStyle(hex string) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color(hex)).
		Padding(0, 1).
		Bold(true)
}

// SevStyle returns the foreground style for a severity slot, optionally
// bolded. Rebuilt per-call so live theme changes show up immediately.
func SevStyle(k theme.Key, bold bool) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Color(k)))
	if bold {
		s = s.Bold(true)
	}
	return s
}

// SeverityStyle preserves the historical call-site shape
// (sevWarning.Render("+ ")) without freezing the colour at startup —
// the underlying theme key is resolved on every Render.
type SeverityStyle struct {
	key  theme.Key
	bold bool
}

// Render proxies to a fresh lipgloss style built from the current theme
// value for the slot.
func (s SeverityStyle) Render(parts ...string) string {
	return SevStyle(s.key, s.bold).Render(parts...)
}

var (
	// SevInfo is the dim blue used for the lowest-severity findings.
	SevInfo = SeverityStyle{key: theme.KeySevInfo}
	// SevWarning is the amber used for medium findings and the diff "+"
	// gutter.
	SevWarning = SeverityStyle{key: theme.KeySevWarning}
	// SevError is the bold red for high-severity findings and the diff
	// "-" gutter.
	SevError = SeverityStyle{key: theme.KeySevError, bold: true}
	// SevCritical is the bold red used for merge-blocking findings.
	SevCritical = SeverityStyle{key: theme.KeySevCritical, bold: true}
)

// RenderTag returns a coloured pill for a row label (specialist tag,
// vibe-coach, or one of the context-injection rows). Unknown labels are
// padded but not coloured so misspellings stay visible.
func RenderTag(specialist string) string {
	switch specialist {
	case "formatting":
		return TagStyle(theme.Color(theme.KeyTagFormatting)).Render(specialist)
	case "design":
		return TagStyle(theme.Color(theme.KeyTagDesign)).Render(specialist)
	case "testing":
		return TagStyle(theme.Color(theme.KeyTagTesting)).Render(specialist)
	case "docs":
		return TagStyle(theme.Color(theme.KeyTagDocs)).Render(specialist)
	case "security":
		return TagStyle(theme.Color(theme.KeyTagSecurity)).Render(specialist)
	case "vibe-coach":
		return TagStyle(theme.Color(theme.KeyTagVibeCoach)).Render(specialist)
	case "language-briefs":
		return TagStyle(theme.Color(theme.KeyTagLangBriefs)).Render("language briefs")
	case "tech-experts":
		return TagStyle(theme.Color(theme.KeyTagTechExperts)).Render("tech experts")
	case "repo-experts":
		return TagStyle(theme.Color(theme.KeyTagRepoExperts)).Render("repo experts")
	case "repo-arbiter":
		return TagStyle(theme.Color(theme.KeyTagRepoArbiter)).Render("repo arbiter")
	default:
		return lipgloss.NewStyle().Padding(0, 1).Render(specialist)
	}
}

// RenderSeverity formats a finding's severity label with the matching
// foreground colour from the active theme.
func RenderSeverity(sev string) string {
	switch sev {
	case "info":
		return SevInfo.Render("info")
	case "warning":
		return SevWarning.Render("warn")
	case "error":
		return SevError.Render("ERROR")
	case "critical":
		return SevCritical.Render("CRITICAL")
	default:
		return DimStyle.Render(sev)
	}
}
