package review

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
)

func TestWordDiffForSnippetPairsReplacements(t *testing.T) {
	lines := []review.DiffLine{
		{Kind: review.DiffContext, Text: "unchanged"},
		{Kind: review.DiffRemoved, Text: "foo(bar)"},
		{Kind: review.DiffAdded, Text: "foo(baz)"},
		{Kind: review.DiffContext, Text: "tail"},
	}
	segs := wordDiffForSnippet(lines)
	if segs[0] != nil || segs[3] != nil {
		t.Fatal("context lines should have no word-diff segments")
	}
	if segs[1] == nil || segs[2] == nil {
		t.Fatal("the removed/added pair should be word-diffed")
	}
	// The removed side must mark "bar" changed and keep "foo(" / ")" common.
	var changed []string
	for _, s := range segs[1] {
		if s.Changed {
			changed = append(changed, s.Text)
		}
	}
	if len(changed) != 1 || changed[0] != "bar" {
		t.Errorf("old side changed spans = %v, want [bar]", changed)
	}
	changed = nil
	for _, s := range segs[2] {
		if s.Changed {
			changed = append(changed, s.Text)
		}
	}
	if len(changed) != 1 || changed[0] != "baz" {
		t.Errorf("new side changed spans = %v, want [baz]", changed)
	}
}

func TestWordDiffForSnippetSkipsUnevenRuns(t *testing.T) {
	// Two removed, one added: no clean 1:1 pairing → whole-line change.
	lines := []review.DiffLine{
		{Kind: review.DiffRemoved, Text: "a"},
		{Kind: review.DiffRemoved, Text: "b"},
		{Kind: review.DiffAdded, Text: "c"},
	}
	segs := wordDiffForSnippet(lines)
	for i, s := range segs {
		if s != nil {
			t.Errorf("line %d should have no word-diff segments for an uneven run", i)
		}
	}
}

func TestWordDiffForSnippetPureInsertion(t *testing.T) {
	// Added with no preceding removed run: not a replacement, no emphasis.
	lines := []review.DiffLine{
		{Kind: review.DiffContext, Text: "x"},
		{Kind: review.DiffAdded, Text: "new line"},
	}
	segs := wordDiffForSnippet(lines)
	for i, s := range segs {
		if s != nil {
			t.Errorf("line %d should have no word-diff segments for a pure insertion", i)
		}
	}
}

func TestRenderWordSegsEmphasisesChangedSpans(t *testing.T) {
	out := renderWordSegs([]diffview.Seg{
		{Text: "keep "},
		{Text: "CHANGED", Changed: true},
		{Text: " keep"},
	})
	// Stripped text must reproduce the concatenation exactly regardless of
	// whether the test terminal supports colour (lipgloss emits no SGR under
	// an ascii colour profile, so we assert on the plain content only).
	if got := ansi.Strip(out); got != "keep CHANGED keep" {
		t.Errorf("stripped word-seg render = %q", got)
	}
}

func TestRenderHunkSnippetNilHighlighterIsPlain(t *testing.T) {
	h := &review.Hunk{
		Header: "@@ -1,2 +1,2 @@",
		Lines: []review.DiffLine{
			{Kind: review.DiffRemoved, Text: "old", OldNo: 1},
			{Kind: review.DiffAdded, Text: "new", NewNo: 1},
		},
	}
	// A nil highlighter must render exactly the plain text (no ANSI beyond the
	// gutter styling), preserving pre-item-4 behaviour for callers that opt out.
	out := renderHunkSnippet(h, "x.go", 1, 4, 80, nil)
	if !strings.Contains(ansi.Strip(out), "old") || !strings.Contains(ansi.Strip(out), "new") {
		t.Errorf("snippet missing diff text: %q", ansi.Strip(out))
	}
}
