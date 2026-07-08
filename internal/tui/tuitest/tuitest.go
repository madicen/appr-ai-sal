// Package tuitest holds shared helpers for the TUI's hermetic render tests:
// the golden-file machinery (with the standard `-update` flag) and the
// deterministic-rendering setup (monochrome + Ascii colour profile) every
// golden / teatest flow relies on.
//
// It is deliberately a leaf: it imports only the theme package and low-level
// rendering libraries, never internal/tui/model or the tab packages, so any
// TUI package's tests can depend on it without an import cycle.
//
// Determinism recipe (Phase 5 item 11):
//   - ForceMonochrome pins lipgloss to the Ascii colour profile and sets
//     NO_COLOR, so no ANSI escapes churn the goldens run-to-run.
//   - Callers pick a fixed terminal size (see model / review flow tests).
//   - Golden inputs come from the demo package's stable offline fixtures.
//   - Normalize strips any residual ANSI + bubblezone markers and trailing
//     whitespace; callers redact time-dependent spans before comparing.
package tuitest

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// The standard Go golden-file flag is `-update`. We register it lazily so we
// coexist with other packages that define the same flag in the same test
// binary — notably charmbracelet/x/exp/golden (pulled in transitively by
// teatest), which also registers `-update`. Registering unconditionally would
// panic with "flag redefined: update" in any binary that links both.
func init() {
	if flag.Lookup("update") == nil {
		flag.Bool("update", false, "rewrite golden files instead of comparing against them")
	}
}

// ShouldUpdate reports whether the -update flag was passed. It reads the flag
// dynamically (rather than caching a *bool at registration time) so it returns
// the correct value regardless of which package registered the shared flag.
func ShouldUpdate() bool {
	f := flag.Lookup("update")
	if f == nil {
		return false
	}
	g, ok := f.Value.(flag.Getter)
	if !ok {
		return false
	}
	b, _ := g.Get().(bool)
	return b
}

// ForceMonochrome makes rendering deterministic for the duration of the test:
// it forces lipgloss to the Ascii colour profile (no ANSI colour) and sets
// NO_COLOR so the diff-syntax highlighter and every theme-routed style also
// degrade to plain text. The prior colour profile is restored on cleanup.
//
// Call this at the top of any golden / teatest that renders a View so its
// output is plain UTF-8 the goldens can capture verbatim.
func ForceMonochrome(t testing.TB) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	t.Setenv("NO_COLOR", "1")
	// Reset the global zone manager so zone.Scan (used by Normalize and by the
	// root model's View) has a live manager to strip markers against.
	zone.NewGlobal()
	theme.ApplyAppearance(theme.Appearance{Mode: theme.ModeDark, NoColor: true})
}

// Normalize canonicalizes rendered TUI output for stable golden comparison:
// it removes bubblezone click markers, strips any residual ANSI escapes, trims
// trailing whitespace on every line, and collapses the trailing blank lines to
// a single terminating newline.
func Normalize(s string) string {
	s = zone.Scan(s) // drop bubblezone markers (no-op if none present)
	s = ansi.Strip(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// AssertGolden compares got against testdata/<name>.golden (relative to the
// caller's package), after Normalize. With -update it writes the golden
// instead. name should be a bare identifier (no extension / directory).
func AssertGolden(t testing.TB, name, got string) {
	t.Helper()
	got = Normalize(got)
	path := filepath.Join("testdata", name+".golden")
	if ShouldUpdate() {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("tuitest: mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("tuitest: write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("tuitest: read golden %s: %v (run tests with -update to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch for %s.\n--- want ---\n%s\n--- got ---\n%s\n(run with -update if this change is intended)", name, string(want), got)
	}
}
