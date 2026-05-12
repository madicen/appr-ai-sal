package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdown_EmptyInputPassThrough(t *testing.T) {
	if got := renderMarkdown("", 80); got != "" {
		t.Fatalf("renderMarkdown(\"\") = %q, want empty", got)
	}
	if got := renderMarkdown("   \n\n", 80); strings.TrimSpace(got) != "" {
		t.Fatalf("whitespace-only input should pass through, got %q", got)
	}
}

func TestRenderMarkdown_PreservesContent(t *testing.T) {
	src := "# Heading\n\nA short paragraph with **bold** and a [link](https://example.com).\n\n- bullet one\n- bullet two\n"
	out := renderMarkdown(src, 80)
	// Glamour wraps adjacent words in separate ANSI groups, so a literal
	// "bullet one" substring won't appear in the raw bytes; strip ANSI first
	// and compare against the plain text the user actually sees.
	plain := ansi.Strip(out)
	for _, needle := range []string{"Heading", "paragraph", "bullet one", "bullet two"} {
		if !strings.Contains(plain, needle) {
			t.Errorf("rendered output missing %q\n---\n%s\n---", needle, plain)
		}
	}
}

func TestRenderMarkdown_RendererCachedByWidth(t *testing.T) {
	// Pre-warm the cache then verify we get the same pointer back.
	r1, err := markdownRendererFor(72)
	if err != nil {
		t.Fatalf("markdownRendererFor: %v", err)
	}
	r2, err := markdownRendererFor(72)
	if err != nil {
		t.Fatalf("markdownRendererFor (second): %v", err)
	}
	if r1 != r2 {
		t.Fatal("expected same renderer pointer for same width (cache miss)")
	}
	r3, err := markdownRendererFor(96)
	if err != nil {
		t.Fatalf("markdownRendererFor 96: %v", err)
	}
	if r3 == r1 {
		t.Fatal("expected distinct renderer for distinct width")
	}
}

func TestRenderMarkdown_StripsMarkdownSigils(t *testing.T) {
	// The whole point of running the body through glamour is to preview
	// what a Markdown viewer would show — not the raw source. Glamour's
	// default dark style keeps "## ", "### ", and friends as a level cue,
	// so we apply a customised style that clears them. This test pins
	// that customisation: if a future glamour upgrade or refactor brings
	// back any of the literal sigils below, we fail loudly.
	src := "## A heading\n\n" +
		"Paragraph with **bold** and *italic* and `code` inline.\n\n" +
		"### Subheading\n\n" +
		"- Bullet\n"
	plain := ansi.Strip(renderMarkdown(src, 80))
	bad := []string{"## ", "### ", "**bold**", "*italic*", "`code`"}
	for _, s := range bad {
		if strings.Contains(plain, s) {
			t.Errorf("rendered output still contains markdown sigil %q\n---\n%s\n---", s, plain)
		}
	}
	// Sanity: the actual words should still survive.
	for _, ok := range []string{"A heading", "Subheading", "bold", "italic", "code", "Bullet"} {
		if !strings.Contains(plain, ok) {
			t.Errorf("rendered output missing content %q\n---\n%s\n---", ok, plain)
		}
	}
}

func TestRenderMarkdown_TightWidthDoesNotCrash(t *testing.T) {
	// Widths below the wrapper's floor (8) should still produce output, not
	// a panic. We don't assert on layout because tiny widths legitimately
	// shred most formatting.
	out := renderMarkdown("hello **world**", 1)
	if !strings.Contains(ansi.Strip(out), "hello") {
		t.Fatalf("expected content even at tiny width, got %q", out)
	}
}

func TestRenderMarkdownIndented_FitsWithinTotalWidth(t *testing.T) {
	body := strings.Repeat("Hello world ", 30)
	const total = 60
	out := renderMarkdownIndented(body, total, 2)
	plain := ansi.Strip(out)
	for i, line := range strings.Split(plain, "\n") {
		if len(line) > total {
			t.Fatalf("line %d width=%d exceeds total=%d: %q", i, len(line), total, line)
		}
	}
}

func TestRenderMarkdownIndented_AppliesPrefix(t *testing.T) {
	out := renderMarkdownIndented("hello", 40, 4)
	plain := ansi.Strip(out)
	// Every non-empty line should start with the 4-space caller-supplied
	// prefix (glamour's own 2-cell margin adds more padding after that).
	for _, line := range strings.Split(plain, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "    ") {
			t.Fatalf("line %q does not start with 4-space prefix", line)
		}
	}
}

func TestRenderMarkdownIndented_ZeroIndentDoesNotAddPrefix(t *testing.T) {
	out := renderMarkdownIndented("hello", 40, 0)
	plain := ansi.Strip(out)
	// With extraIndent=0 the only leading whitespace should be glamour's
	// own 2-cell document margin.
	for _, line := range strings.Split(plain, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Glamour pads short lines with trailing spaces; assert leading
		// whitespace is exactly 2 cells, not 4+.
		trimmed := strings.TrimLeft(line, " ")
		leading := len(line) - len(trimmed)
		if leading != 2 {
			t.Fatalf("expected exactly 2 leading spaces (glamour margin), got %d on %q", leading, line)
		}
		break // first content line is enough
	}
}
