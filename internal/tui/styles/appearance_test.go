package styles

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// withRenderer saves and restores the global lipgloss renderer flags so the
// appearance-driven render assertions don't leak between tests.
func withRenderer(t *testing.T) {
	t.Helper()
	prevProfile := lipgloss.ColorProfile()
	prevDark := lipgloss.HasDarkBackground()
	prevAppearance := theme.ActiveAppearance()
	t.Cleanup(func() {
		theme.ApplyAppearance(prevAppearance)
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDark)
	})
}

func TestChromeIsMonochromeUnderNoColor(t *testing.T) {
	withRenderer(t)
	theme.ApplyAppearance(theme.Appearance{Mode: theme.ModeDark, NoColor: true})

	// A colour-only chrome style must emit no ANSI escapes at all.
	if out := StatusBar.Render("help"); strings.Contains(out, "\x1b[") {
		t.Errorf("StatusBar under NO_COLOR still contains ANSI: %q", out)
	}
	if out := DimStyle.Render("hint"); strings.Contains(out, "\x1b[") {
		t.Errorf("DimStyle under NO_COLOR still contains ANSI: %q", out)
	}
	// Even a filled chip (background + foreground) must lose its colour;
	// only non-colour attributes (bold) may remain.
	if out := ChipPrimaryStyle.Render("Run"); containsColorSGR(out) {
		t.Errorf("ChipPrimaryStyle under NO_COLOR still contains colour SGR: %q", out)
	}
	if out := HeaderBar.Render("appr-ai-sal"); containsColorSGR(out) {
		t.Errorf("HeaderBar under NO_COLOR still contains colour SGR: %q", out)
	}
}

func TestChromeRendersDarkPaletteInDarkMode(t *testing.T) {
	withRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	theme.ApplyAppearance(theme.Appearance{Mode: theme.ModeDark})

	// StatusBar foreground is RoleMuted → dark #888888 (greys survive the
	// colour round-trip exactly, unlike saturated hues which lipgloss may
	// round by ±1, so we assert grey values precisely and colour-presence
	// elsewhere).
	out := StatusBar.Render("x")
	if !containsRGB(out, 0x88, 0x88, 0x88) {
		t.Errorf("dark StatusBar should render #888888; got %q", out)
	}
	// HeaderBar has an accent background — under a colour profile it must emit
	// a background SGR (48;2;...).
	if hb := HeaderBar.Render("x"); !strings.Contains(hb, "48;2;") {
		t.Errorf("dark HeaderBar should render a background colour; got %q", hb)
	}
}

func TestChromeRendersLightPaletteInLightMode(t *testing.T) {
	withRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	theme.ApplyAppearance(theme.Appearance{Mode: theme.ModeLight})

	// StatusBar foreground is RoleMuted → light #666666.
	out := StatusBar.Render("x")
	if !containsRGB(out, 0x66, 0x66, 0x66) {
		t.Errorf("light StatusBar should render #666666; got %q", out)
	}
	// Restore dark for subsequent tests explicitly (cleanup also handles it).
	theme.ApplyAppearance(theme.Appearance{Mode: theme.ModeDark})
}

// TestNoHardcodedChromeHexColours is the grep-based audit the acceptance
// criteria call for: no lipgloss.Color("#..."), lipgloss.Color("<index>"), or
// lipgloss.AdaptiveColor{...} literal may live in internal/tui non-test code.
// All chrome colour must flow through theme.Adaptive(role). Two deliberate
// exceptions are allow-listed:
//
//   - styles/tags.go: tagForeground returns "#000000"/"#FFFFFF" as the
//     auto-contrast foreground for a user-chosen tag-pill background (derived
//     from the tag colour, not a chrome colour; stripped under NO_COLOR).
//   - diffview/syntax.go: chroma owns the syntax-highlight colour source (its
//     own style), which is intentionally separate and disabled under NO_COLOR.
func TestNoHardcodedChromeHexColours(t *testing.T) {
	tuiRoot := filepath.Join("..") // internal/tui (test cwd is internal/tui/styles)

	// Patterns that indicate a hardcoded chrome colour literal.
	literalColor := regexp.MustCompile(`lipgloss\.Color\("(#|[0-9])`)
	literalAdaptive := regexp.MustCompile(`lipgloss\.AdaptiveColor\{`)

	allow := map[string]bool{
		filepath.FromSlash("styles/tags.go"):     true,
		filepath.FromSlash("diffview/syntax.go"): true,
	}

	var offenders []string
	err := filepath.Walk(tuiRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(tuiRoot, path)
		if allow[rel] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if literalColor.MatchString(src) || literalAdaptive.MatchString(src) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/tui: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("hardcoded chrome colour literals found in %d file(s) (route them through theme.Adaptive): %v", len(offenders), offenders)
	}
}

// containsColorSGR reports whether s carries any colour SGR parameter (the
// 30–38 / 40–48 / 90–97 / 100–107 ranges, or the 38/48 extended forms). Bold
// (1), underline (4), etc. are ignored — NO_COLOR only forbids colour.
func containsColorSGR(s string) bool {
	sgr := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	for _, m := range sgr.FindAllStringSubmatch(s, -1) {
		for _, p := range strings.Split(m[1], ";") {
			switch {
			case p == "":
			case isColorParam(p):
				return true
			}
		}
	}
	return false
}

func isColorParam(p string) bool {
	var n int
	if _, err := fmt.Sscanf(p, "%d", &n); err != nil {
		return false
	}
	switch {
	case n >= 30 && n <= 38, n >= 40 && n <= 48,
		n >= 90 && n <= 97, n >= 100 && n <= 107:
		return true
	}
	return false
}

func containsRGB(s string, r, g, b int) bool {
	return strings.Contains(s, fmt.Sprintf("38;2;%d;%d;%d", r, g, b)) ||
		strings.Contains(s, fmt.Sprintf("48;2;%d;%d;%d", r, g, b))
}
