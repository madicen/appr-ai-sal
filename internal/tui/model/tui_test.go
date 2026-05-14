package model

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/tui/util"

	"github.com/charmbracelet/x/ansi"
)

func TestWrapForViewportLongUnbrokenToken(t *testing.T) {
	const cols = 40
	long := strings.Repeat("a", 120)
	out := util.WrapForViewport(long, cols)
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if w := ansi.StringWidth(ln); w > cols {
			t.Fatalf("line width %d > %d: %q", w, cols, ansi.Truncate(ln, 60, "…"))
		}
	}
}

func TestWrapForViewportPreservesSoftWrapThenHardWrapsRemainder(t *testing.T) {
	const cols = 30
	// Long token embedded — ansi.Wrap may leave it as one segment > cols.
	s := "intro words " + strings.Repeat("x", 80)
	out := util.WrapForViewport(s, cols)
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if w := ansi.StringWidth(ln); w > cols {
			t.Fatalf("line width %d > %d", w, cols)
		}
	}
}
