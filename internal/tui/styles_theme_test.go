package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// forceTrueColor pins lipgloss to a true-colour profile so tests can assert
// against the RGB triplets emitted in ANSI escape sequences. Without this,
// `go test` runs without a terminal and lipgloss strips colour entirely.
func forceTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// TestRenderTagHonoursThemeOverride proves the styles refactor actually
// reads from theme.Current() per call: applying a custom theme should
// change the ANSI background colour embedded in renderTag's output.
func TestRenderTagHonoursThemeOverride(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)

	custom := theme.Default()
	custom.Set(theme.KeyTagFormatting, "#abcdef")
	custom.Set(theme.KeyTagLangBriefs, "#123456")
	theme.Apply(custom)

	formatting := renderTag("formatting")
	langBriefs := renderTag("language-briefs")

	// lipgloss emits true-colour escapes as ESC [ 48 ; 2 ; R ; G ; B m.
	// We don't need to parse the escape - just check the literal RGB
	// values appear in the rendered string for each tag.
	wantFmt := "171;205;239" // #abcdef
	wantLang := "18;52;86"   // #123456
	if !strings.Contains(formatting, wantFmt) {
		t.Errorf("formatting tag should encode override RGB %s; got %q", wantFmt, formatting)
	}
	if !strings.Contains(langBriefs, wantLang) {
		t.Errorf("language-briefs tag should encode override RGB %s; got %q", wantLang, langBriefs)
	}
}

// TestRenderSeverityHonoursThemeOverride covers the sevX helpers used by
// pr_detail.go and review_overlay.go (these were inlined as constants
// before the theme refactor).
func TestRenderSeverityHonoursThemeOverride(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)

	custom := theme.Default()
	custom.Set(theme.KeySevWarning, "#fedcba")
	theme.Apply(custom)

	got := renderSeverity("warning")
	want := "254;220;186" // #fedcba
	if !strings.Contains(got, want) {
		t.Errorf("warning severity should encode override RGB %s; got %q", want, got)
	}
}

// TestRenderTagFallsBackToDefaultsOnReset documents the Apply(nil) path
// and ensures the default palette is still reachable after editing.
//
// We assert behavioural equivalence (default render after reset matches
// the unmodified default render) rather than a specific RGB triplet,
// because lipgloss/termenv round true-colour values slightly differently
// across versions.
func TestRenderTagFallsBackToDefaultsOnReset(t *testing.T) {
	forceTrueColor(t)
	original := theme.Current()
	defer theme.Apply(original)

	theme.Apply(nil)
	defaultRendered := renderTag("formatting")

	custom := theme.Default()
	custom.Set(theme.KeyTagFormatting, "#000000")
	theme.Apply(custom)
	overriddenRendered := renderTag("formatting")
	if overriddenRendered == defaultRendered {
		t.Fatalf("override should produce different output than default; both rendered as %q", overriddenRendered)
	}

	theme.Apply(nil)
	resetRendered := renderTag("formatting")
	if resetRendered != defaultRendered {
		t.Errorf("after Apply(nil) renderTag should match the original default render\n got: %q\nwant: %q",
			resetRendered, defaultRendered)
	}
	// Defensive: the rendered string should still contain SOME RGB triplet
	// (i.e. lipgloss is colouring it at all). Strip everything but digits
	// and semicolons; if the result is empty, true-colour wasn't applied.
	if !strings.ContainsAny(resetRendered, ";") {
		t.Errorf("expected ANSI true-colour escape; got %q", resetRendered)
	}
}
