package util

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdown_EmptyInputPassThrough(t *testing.T) {
	if got := RenderMarkdown("", 80); got != "" {
		t.Fatalf("RenderMarkdown(\"\") = %q, want empty", got)
	}
	if got := RenderMarkdown("   \n\n", 80); strings.TrimSpace(got) != "" {
		t.Fatalf("whitespace-only input should pass through, got %q", got)
	}
}

func TestRenderMarkdown_PreservesContent(t *testing.T) {
	src := "# Heading\n\nA short paragraph with **bold** and a [link](https://example.com).\n\n- bullet one\n- bullet two\n"
	out := RenderMarkdown(src, 80)
	plain := ansi.Strip(out)
	for _, needle := range []string{"Heading", "paragraph", "bullet one", "bullet two"} {
		if !strings.Contains(plain, needle) {
			t.Errorf("rendered output missing %q\n---\n%s\n---", needle, plain)
		}
	}
}

func TestRenderMarkdown_RendererCachedByWidth(t *testing.T) {
	r1, err := MarkdownRendererFor(72)
	if err != nil {
		t.Fatalf("MarkdownRendererFor: %v", err)
	}
	r2, err := MarkdownRendererFor(72)
	if err != nil {
		t.Fatalf("MarkdownRendererFor (second): %v", err)
	}
	if r1 != r2 {
		t.Fatal("expected same renderer pointer for same width (cache miss)")
	}
	r3, err := MarkdownRendererFor(96)
	if err != nil {
		t.Fatalf("MarkdownRendererFor 96: %v", err)
	}
	if r3 == r1 {
		t.Fatal("expected distinct renderer for distinct width")
	}
}

func TestRenderMarkdown_StripsMarkdownSigils(t *testing.T) {
	src := "## A heading\n\n" +
		"Paragraph with **bold** and *italic* and `code` inline.\n\n" +
		"### Subheading\n\n" +
		"- Bullet\n"
	plain := ansi.Strip(RenderMarkdown(src, 80))
	bad := []string{"## ", "### ", "**bold**", "*italic*", "`code`"}
	for _, s := range bad {
		if strings.Contains(plain, s) {
			t.Errorf("rendered output still contains markdown sigil %q\n---\n%s\n---", s, plain)
		}
	}
	for _, ok := range []string{"A heading", "Subheading", "bold", "italic", "code", "Bullet"} {
		if !strings.Contains(plain, ok) {
			t.Errorf("rendered output missing content %q\n---\n%s\n---", ok, plain)
		}
	}
}

func TestRenderMarkdown_TightWidthDoesNotCrash(t *testing.T) {
	out := RenderMarkdown("hello **world**", 1)
	if !strings.Contains(ansi.Strip(out), "hello") {
		t.Fatalf("expected content even at tiny width, got %q", out)
	}
}

func TestRenderMarkdownIndented_FitsWithinTotalWidth(t *testing.T) {
	body := strings.Repeat("Hello world ", 30)
	const total = 60
	out := RenderMarkdownIndented(body, total, 2)
	plain := ansi.Strip(out)
	for i, line := range strings.Split(plain, "\n") {
		if len(line) > total {
			t.Fatalf("line %d width=%d exceeds total=%d: %q", i, len(line), total, line)
		}
	}
}

func TestRenderMarkdownIndented_AppliesPrefix(t *testing.T) {
	out := RenderMarkdownIndented("hello", 40, 4)
	plain := ansi.Strip(out)
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
	out := RenderMarkdownIndented("hello", 40, 0)
	plain := ansi.Strip(out)
	for _, line := range strings.Split(plain, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		leading := len(line) - len(trimmed)
		if leading != 2 {
			t.Fatalf("expected exactly 2 leading spaces (glamour margin), got %d on %q", leading, line)
		}
		break
	}
}
