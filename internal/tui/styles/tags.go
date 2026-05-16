package styles

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// TagStyle returns a row-tag style (filled background pill) for the given
// hex background. The foreground is picked automatically — black on light
// pills and white on dark ones — so that themable colours like the pastel
// context-injection rows stay readable without each slot having to declare
// its own foreground. Styles are rebuilt on every RenderTag call rather
// than baked into package globals so live theme changes show up
// immediately.
func TagStyle(hex string) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(tagForeground(hex))).
		Background(lipgloss.Color(hex)).
		Padding(0, 1).
		Bold(true)
}

// tagForeground returns "#000000" or "#FFFFFF" depending on which has
// better contrast against the supplied background hex. We use the
// well-known YIQ luma threshold (≈ 128/255) rather than full WCAG
// relative luminance — terminals render the difference between the two
// methods imperceptibly, and the YIQ form keeps the helper allocation
// free. Hex strings that fail to parse fall back to white so the
// behaviour matches the historical hard-coded value.
func tagForeground(hex string) string {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return "#FFFFFF"
	}
	// YIQ luma in the 0..255 range; ≥128 ⇒ light background ⇒ dark text.
	if (int(r)*299+int(g)*587+int(b)*114)/1000 >= 128 {
		return "#000000"
	}
	return "#FFFFFF"
}

// parseHex accepts "#rgb" and "#rrggbb" forms. Theme values are
// normalised to the 7-char form on write, but TagStyle is exported so
// we tolerate both shapes here.
func parseHex(s string) (r, g, b byte, ok bool) {
	if len(s) == 4 && s[0] == '#' {
		r, ok1 := hexNibble(s[1])
		g, ok2 := hexNibble(s[2])
		b, ok3 := hexNibble(s[3])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, false
		}
		return r*17, g*17, b*17, true
	}
	if len(s) == 7 && s[0] == '#' {
		rh, ok1 := hexByte(s[1], s[2])
		gh, ok2 := hexByte(s[3], s[4])
		bh, ok3 := hexByte(s[5], s[6])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, false
		}
		return rh, gh, bh, true
	}
	return 0, 0, 0, false
}

func hexByte(hi, lo byte) (byte, bool) {
	h, ok1 := hexNibble(hi)
	l, ok2 := hexNibble(lo)
	if !(ok1 && ok2) {
		return 0, false
	}
	return h<<4 | l, true
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
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
