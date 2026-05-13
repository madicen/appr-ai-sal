package styles

import (
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
