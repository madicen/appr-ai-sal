package util

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// RenderMarkdown turns body into ANSI-styled markdown suitable for our
// viewports. width is the wrap-width passed to glamour; the rendered
// output still picks up glamour's built-in 2-cell document margin on top,
// so the visible block is width+2 cells wide. For tighter control over
// the final indent, use RenderMarkdownIndented.
//
// On any failure (empty input, render error, width too small to be useful)
// we fall back to body verbatim so the UI never goes blank because glamour
// hiccuped on weird input.
func RenderMarkdown(body string, width int) string {
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		return body
	}
	w := width
	if w < 8 {
		w = 8
	}
	r, err := MarkdownRendererFor(w)
	if err != nil {
		return body
	}
	out, err := r.Render(body)
	if err != nil {
		return body
	}
	// Glamour wraps the rendered block in its own leading/trailing blank
	// lines; trim them so callers can compose the result tightly into
	// surrounding chrome (headers, button rows, etc.) without leaving a
	// double-gap.
	return strings.Trim(out, "\n")
}

// RenderMarkdownIndented renders body and guarantees the visible block
// fits in totalWidth cells with an additional extraIndent spaces of
// left padding on top of glamour's built-in 2-cell margin.
//
// Useful when the body is being nested under a labeled section (e.g.
// "Thoughts" or "Comment GitHub will post") that already indents itself —
// callers just say "give me a markdown block at indent N within total
// width W" and don't have to do the width math.
//
// The returned string is newline-separated lines, all prefixed with
// `extraIndent` spaces, ready to write into a strings.Builder. It does
// not have a trailing newline; the caller decides whether to add one.
func RenderMarkdownIndented(body string, totalWidth, extraIndent int) string {
	if extraIndent < 0 {
		extraIndent = 0
	}
	// Account for both extraIndent (caller-supplied) and glamour's own
	// 2-cell document margin so the rendered block never exceeds the
	// caller's total width budget.
	wrapW := totalWidth - extraIndent - 2
	if wrapW < 6 {
		wrapW = 6
	}
	rendered := RenderMarkdown(body, wrapW)
	if extraIndent == 0 {
		return rendered
	}
	pad := strings.Repeat(" ", extraIndent)
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}

// MarkdownRendererFor returns a cached *glamour.TermRenderer for the given
// width using the dark style. Glamour renderers are width-bound (they wrap
// internally to the configured column count), so the cache is keyed by
// width. The reviewOverlay viewport and the description block change width
// when the window resizes; caching avoids re-allocating the renderer chain
// for every paint while still picking up new widths on resize.
func MarkdownRendererFor(width int) (*glamour.TermRenderer, error) {
	markdownRendererMu.Lock()
	defer markdownRendererMu.Unlock()
	if r, ok := markdownRendererCache[width]; ok {
		return r, nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(stripMarkdownGlyphsStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	if markdownRendererCache == nil {
		markdownRendererCache = map[int]*glamour.TermRenderer{}
	}
	markdownRendererCache[width] = r
	return r, nil
}

var (
	markdownRendererMu    sync.Mutex
	markdownRendererCache map[int]*glamour.TermRenderer
)

// stripMarkdownGlyphsStyle returns a copy of glamour's dark style with all
// the leftover markdown sigils removed so the on-screen preview looks like
// what a Markdown viewer would show, not the raw source.
//
// What we change versus DarkStyleConfig:
//
//   - H2–H6 Prefix is cleared. The upstream style keeps "## ", "### " etc.
//     as a visual level cue, but that defeats the point of a preview —
//     users want to see the rendered heading, not the literal hash marks.
//     Bold + the existing color palette still distinguish heading levels.
//   - Emph / Strong block prefixes/suffixes are cleared. In the dark style
//     they're empty (dark uses Bold/Italic SGRs only), but some glamour
//     defaults sneak "*"/"**" into output; clearing here is defensive so
//     a future glamour update can't reintroduce literal asterisks.
//   - Code inline keeps its background color but loses the padding spaces
//     so a `git rebase` reference doesn't render as " git rebase " with
//     visible gaps around the colored chunk.
//
// We start from a value copy of styles.DarkStyleConfig; the pointer fields
// (Margin, Indent, IndentToken on each StyleBlock) are intentionally shared
// — we never write through them, so the global remains untouched.
func stripMarkdownGlyphsStyle() ansi.StyleConfig {
	s := styles.DarkStyleConfig
	s.H2.Prefix = ""
	s.H3.Prefix = ""
	s.H4.Prefix = ""
	s.H5.Prefix = ""
	s.H6.Prefix = ""
	s.Emph.BlockPrefix = ""
	s.Emph.BlockSuffix = ""
	s.Strong.BlockPrefix = ""
	s.Strong.BlockSuffix = ""
	s.Code.Prefix = ""
	s.Code.Suffix = ""
	return s
}
