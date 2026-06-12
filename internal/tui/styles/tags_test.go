package styles

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// forceTrueColor pins lipgloss to a true-colour profile so tests can
// assert against the RGB triplets emitted in ANSI escape sequences.
// Without this, `go test` runs without a terminal and lipgloss strips
// colour entirely.
func forceTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestRenderTagHonoursThemeOverride(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)

	custom := theme.Default()
	custom.Set(theme.KeyTagFormatting, "#abcdef")
	custom.Set(theme.KeyTagLangBriefs, "#123456")
	theme.Apply(custom)

	formatting := RenderTag("formatting")
	langBriefs := RenderTag("language-briefs")

	wantFmt := "171;205;239" // #abcdef
	wantLang := "18;52;86"   // #123456
	if !strings.Contains(formatting, wantFmt) {
		t.Errorf("formatting tag should encode override RGB %s; got %q", wantFmt, formatting)
	}
	if !strings.Contains(langBriefs, wantLang) {
		t.Errorf("language-briefs tag should encode override RGB %s; got %q", wantLang, langBriefs)
	}
}

// The PR-agent rows (description / checks / discussion / scope) get their
// own coloured pills rather than falling through to the uncoloured default.
func TestRenderTagColoursPRAgents(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)
	theme.Apply(nil)

	cases := []struct {
		name string
		key  theme.Key
	}{
		{"description", theme.KeyTagDescription},
		{"checks", theme.KeyTagChecks},
		{"discussion", theme.KeyTagDiscussion},
		{"scope", theme.KeyTagScope},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderTag(tc.name)
			r, g, b, ok := parseHex(theme.DefaultColor(tc.key))
			if !ok {
				t.Fatalf("default colour for %s is not parseable", tc.key)
			}
			wantBG := fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
			if !strings.Contains(got, wantBG) {
				t.Errorf("RenderTag(%q) should encode background %s; got %q", tc.name, wantBG, got)
			}
		})
	}
}

// The tech specialist gets its own coloured pill like the other
// specialists rather than falling through to the uncoloured default.
func TestRenderTagColoursTechSpecialist(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)
	theme.Apply(nil)

	got := RenderTag("tech")
	r, g, b, ok := parseHex(theme.DefaultColor(theme.KeyTagTech))
	if !ok {
		t.Fatalf("default colour for %s is not parseable", theme.KeyTagTech)
	}
	wantBG := fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
	if !strings.Contains(got, wantBG) {
		t.Errorf("RenderTag(%q) should encode background %s; got %q", "tech", wantBG, got)
	}
}

func TestRenderSeverityHonoursThemeOverride(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)

	custom := theme.Default()
	custom.Set(theme.KeySevWarning, "#fedcba")
	theme.Apply(custom)

	got := RenderSeverity("warning")
	want := "254;220;186" // #fedcba
	if !strings.Contains(got, want) {
		t.Errorf("warning severity should encode override RGB %s; got %q", want, got)
	}
}

func TestTagForegroundPicksContrastingText(t *testing.T) {
	cases := []struct {
		name string
		bg   string
		want string
	}{
		{"dark navy gets white text", "#1a1b26", "#FFFFFF"},
		{"pure black gets white text", "#000000", "#FFFFFF"},
		{"pastel teal gets black text", "#7BC5CC", "#000000"},
		{"pastel peach gets black text", "#ECB088", "#000000"},
		{"pastel lavender gets black text", "#A8B5DC", "#000000"},
		{"pure white gets black text", "#FFFFFF", "#000000"},
		{"short form #fff gets black text", "#fff", "#000000"},
		{"short form #000 gets white text", "#000", "#FFFFFF"},
		{"unparseable hex falls back to white", "not-a-hex", "#FFFFFF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tagForeground(tc.bg); got != tc.want {
				t.Errorf("tagForeground(%q) = %q, want %q", tc.bg, got, tc.want)
			}
		})
	}
}

func TestRenderTagEmitsContrastingForeground(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)

	// Force the language-briefs pill to a very light background; the
	// rendered escape sequence should contain the foreground RGB for
	// black (0;0;0) and not the historical 255;255;255.
	custom := theme.Default()
	custom.Set(theme.KeyTagLangBriefs, "#eeeeee")
	theme.Apply(custom)

	got := RenderTag("language-briefs")
	if !strings.Contains(got, "38;2;0;0;0") {
		t.Errorf("expected black foreground escape (38;2;0;0;0) for light pill; got %q", got)
	}
	if strings.Contains(got, "38;2;255;255;255") {
		t.Errorf("light pill should not render white foreground; got %q", got)
	}
}

func TestRenderTagFallsBackToDefaultsOnReset(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)

	theme.Apply(nil)
	defaultRendered := RenderTag("formatting")

	custom := theme.Default()
	custom.Set(theme.KeyTagFormatting, "#000000")
	theme.Apply(custom)
	overriddenRendered := RenderTag("formatting")
	if overriddenRendered == defaultRendered {
		t.Fatalf("override should produce different output than default; both rendered as %q", overriddenRendered)
	}

	theme.Apply(nil)
	resetRendered := RenderTag("formatting")
	if resetRendered != defaultRendered {
		t.Errorf("after Apply(nil) RenderTag should match the original default render\n got: %q\nwant: %q",
			resetRendered, defaultRendered)
	}
	if !strings.ContainsAny(resetRendered, ";") {
		t.Errorf("expected ANSI true-colour escape; got %q", resetRendered)
	}
}
