package diffview

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		path string
		want string // substring the detected name should contain, "" = no lexer
	}{
		{"main.go", "Go"},
		{"app/server.py", "Python"},
		{"index.ts", "TypeScript"},
		{"styles.css", "CSS"},
		{"README.weirdextthatdoesnotexist", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := DetectLanguage(c.path)
		if c.want == "" {
			if got != "" {
				t.Errorf("DetectLanguage(%q) = %q, want no lexer", c.path, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("DetectLanguage(%q) = %q, want it to contain %q", c.path, got, c.want)
		}
	}
}

// enableColor removes NO_COLOR for the duration of a test and restores the
// original afterwards, so highlighting is actually active.
func enableColor(t *testing.T) {
	t.Helper()
	orig, had := os.LookupEnv("NO_COLOR")
	_ = os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("NO_COLOR", orig)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})
}

func TestHighlightAddsColorAndPreservesText(t *testing.T) {
	enableColor(t)
	h := NewHighlighter()
	code := "func main() { return }"
	got := h.Line("main.go", code)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI escape codes in highlighted output, got %q", got)
	}
	if plain := ansi.Strip(got); plain != code {
		t.Errorf("stripping ANSI should recover the source: got %q want %q", plain, code)
	}
}

func TestHighlightFailsOpenOnUnknownLanguage(t *testing.T) {
	enableColor(t)
	h := NewHighlighter()
	code := "this is not a known language line"
	got := h.Line("data.unknownext", code)
	if got != code {
		t.Errorf("unknown language should pass through unchanged: got %q want %q", got, code)
	}
	if h.SupportsFile("data.unknownext") {
		t.Error("SupportsFile should be false for an unknown extension")
	}
	if !h.SupportsFile("main.go") {
		t.Error("SupportsFile should be true for a .go file")
	}
}

func TestHighlightDisabledUnderNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	h := NewHighlighter()
	code := "func main() {}"
	got := h.Line("main.go", code)
	if got != code {
		t.Errorf("NO_COLOR should disable highlighting: got %q want %q", got, code)
	}
	if h.SupportsFile("main.go") {
		t.Error("SupportsFile should be false when highlighting is disabled")
	}
}

// TestHighlightDisabledUnderMonochromeAppearance proves the chroma layer honours
// the resolved theme appearance (e.g. APPR_AI_SAL_THEME=none) even when the
// NO_COLOR env var is not set, so syntax colour vanishes in lockstep with the
// (also-monochrome) chrome.
func TestHighlightDisabledUnderMonochromeAppearance(t *testing.T) {
	enableColor(t) // ensure NO_COLOR is not the reason it's disabled
	prev := theme.ActiveAppearance()
	t.Cleanup(func() { theme.ApplyAppearance(prev) })

	theme.ApplyAppearance(theme.Appearance{Mode: theme.ModeDark, NoColor: true})
	h := NewHighlighter()
	code := "func main() {}"
	if got := h.Line("main.go", code); got != code {
		t.Errorf("monochrome appearance should disable highlighting: got %q want %q", got, code)
	}
	if h.Active() {
		t.Error("Active() should be false under a monochrome appearance")
	}

	// Restoring a colour appearance re-enables it.
	theme.ApplyAppearance(theme.Appearance{Mode: theme.ModeDark})
	if !NewHighlighter().Active() {
		t.Error("Active() should be true again once colour is restored")
	}
}

func TestHighlightNilAndEmptySafe(t *testing.T) {
	enableColor(t)
	var h *Highlighter
	if got := h.Line("main.go", "x := 1"); got != "x := 1" {
		t.Errorf("nil highlighter must pass through: got %q", got)
	}
	live := NewHighlighter()
	if got := live.Line("main.go", "   "); got != "   " {
		t.Errorf("blank line must pass through unchanged: got %q", got)
	}
}
