package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestParseModeDefaultsToDark(t *testing.T) {
	cases := map[string]Mode{
		"":         ModeDark,
		"dark":     ModeDark,
		"DARK":     ModeDark,
		"light":    ModeLight,
		" Light ":  ModeLight,
		"auto":     ModeAuto,
		"adaptive": ModeAuto,
		"system":   ModeAuto,
		"nonsense": ModeDark,
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestModeStringRoundTrips(t *testing.T) {
	for _, m := range []Mode{ModeDark, ModeLight, ModeAuto} {
		if got := ParseMode(m.String()); got != m {
			t.Errorf("ParseMode(%q) = %v, want %v", m.String(), got, m)
		}
	}
}

func TestDetectAppearanceEnvOverrides(t *testing.T) {
	// Ensure a clean environment for each sub-case.
	unset := func(t *testing.T) {
		t.Helper()
		_ = os.Unsetenv("APPR_AI_SAL_THEME")
		_ = os.Unsetenv("NO_COLOR")
	}

	t.Run("default keeps saved mode", func(t *testing.T) {
		unset(t)
		if a := DetectAppearance(ModeLight); a.Mode != ModeLight || a.NoColor {
			t.Errorf("DetectAppearance(ModeLight) = %+v, want light, no NO_COLOR", a)
		}
	})

	t.Run("APPR_AI_SAL_THEME overrides saved mode", func(t *testing.T) {
		unset(t)
		t.Setenv("APPR_AI_SAL_THEME", "light")
		if a := DetectAppearance(ModeDark); a.Mode != ModeLight {
			t.Errorf("env light should win over saved dark; got %+v", a)
		}
	})

	t.Run("APPR_AI_SAL_THEME=none forces monochrome", func(t *testing.T) {
		unset(t)
		t.Setenv("APPR_AI_SAL_THEME", "none")
		if a := DetectAppearance(ModeDark); !a.NoColor {
			t.Errorf("theme=none should set NoColor; got %+v", a)
		}
	})

	t.Run("NO_COLOR wins over everything", func(t *testing.T) {
		unset(t)
		t.Setenv("APPR_AI_SAL_THEME", "light")
		t.Setenv("NO_COLOR", "")
		a := DetectAppearance(ModeDark)
		if !a.NoColor {
			t.Errorf("NO_COLOR (even empty) should force monochrome; got %+v", a)
		}
		// Mode is still resolved (light) so a later un-setting keeps intent.
		if a.Mode != ModeLight {
			t.Errorf("mode should still resolve under NO_COLOR; got %+v", a)
		}
	})
}

// withRendererState saves and restores the global lipgloss renderer flags the
// appearance touches so these tests don't leak into others.
func withRendererState(t *testing.T) {
	t.Helper()
	prevProfile := lipgloss.ColorProfile()
	prevDark := lipgloss.HasDarkBackground()
	prevAppearance := ActiveAppearance()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDark)
		ApplyAppearance(prevAppearance)
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDark)
	})
}

func TestApplyAppearanceNoColorForcesAsciiProfile(t *testing.T) {
	withRendererState(t)
	lipgloss.SetColorProfile(termenv.TrueColor)

	ApplyAppearance(Appearance{Mode: ModeDark, NoColor: true})
	if lipgloss.ColorProfile() != termenv.Ascii {
		t.Errorf("NoColor appearance should force Ascii profile; got %v", lipgloss.ColorProfile())
	}
	if !NoColor() {
		t.Errorf("NoColor() should report true after applying a monochrome appearance")
	}

	// A colour-only style must emit no ANSI at all under the Ascii profile.
	out := lipgloss.NewStyle().Foreground(Adaptive(RoleError)).Render("boom")
	if containsANSI(out) {
		t.Errorf("NO_COLOR render still contains ANSI escapes: %q", out)
	}
}

func TestApplyAppearanceForcesDarkBackground(t *testing.T) {
	withRendererState(t)
	lipgloss.SetColorProfile(termenv.TrueColor)

	ApplyAppearance(Appearance{Mode: ModeDark})
	if !lipgloss.HasDarkBackground() {
		t.Errorf("ModeDark should pin HasDarkBackground(true)")
	}
	// AdaptiveColor should resolve to the dark hex under a dark background.
	out := lipgloss.NewStyle().Foreground(Adaptive(RoleMuted)).Render("x")
	if !containsRGB(out, 0x88, 0x88, 0x88) { // #888888
		t.Errorf("dark mode muted colour should render #888888; got %q", out)
	}
}

func TestApplyAppearanceForcesLightBackground(t *testing.T) {
	withRendererState(t)
	lipgloss.SetColorProfile(termenv.TrueColor)

	ApplyAppearance(Appearance{Mode: ModeLight})
	if lipgloss.HasDarkBackground() {
		t.Errorf("ModeLight should pin HasDarkBackground(false)")
	}
	out := lipgloss.NewStyle().Foreground(Adaptive(RoleMuted)).Render("x")
	if !containsRGB(out, 0x66, 0x66, 0x66) { // #666666
		t.Errorf("light mode muted colour should render #666666; got %q", out)
	}
}

func TestThemeModeRoundTripsThroughStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)

	saved := Default()
	saved.Mode = "light"
	if err := Save(saved, ""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The persisted file should carry the mode.
	b, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatalf("read theme.json: %v", err)
	}
	if !strings.Contains(string(b), `"mode": "light"`) {
		t.Errorf("theme.json missing persisted mode; got %s", b)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AppearanceMode() != ModeLight {
		t.Errorf("loaded mode = %v, want light", loaded.AppearanceMode())
	}
}

func TestSaveOmitsDefaultDarkMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)

	saved := Default() // Mode "" == dark default
	if err := Save(saved, ""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatalf("read theme.json: %v", err)
	}
	if strings.Contains(string(b), `"mode"`) {
		t.Errorf("default dark mode should not be persisted; got %s", b)
	}
}

func containsANSI(s string) bool {
	return strings.Contains(s, "\x1b[")
}

// containsRGB reports whether s contains a truecolor SGR sequence for the given
// r,g,b (the "38;2;R;G;B" / "48;2;R;G;B" forms lipgloss emits under TrueColor).
func containsRGB(s string, r, g, b int) bool {
	return strings.Contains(s, fmt.Sprintf("38;2;%d;%d;%d", r, g, b)) ||
		strings.Contains(s, fmt.Sprintf("48;2;%d;%d;%d", r, g, b))
}
