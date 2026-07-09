// Package diffview holds the pure, testable helpers behind the Phase 5
// item-4 diff upgrades: chroma syntax highlighting (per-file language
// detection, fail-open to plain), intra-line word-level diffing, and the
// navigation indexes for jumping between inline finding tags and in-diff
// search matches.
//
// Everything here is deliberately decoupled from the big (untested) diff
// renderers in internal/tui/model and internal/tui/tabs/review: the helpers
// take plain strings / line numbers and return plain strings / spans /
// indexes, so they can be unit-tested without a terminal, a viewport, or a
// review.Draft. The renderers wire the results into their ANSI output.
package diffview

import (
	"bytes"
	"os"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	"github.com/madicen/appr-ai-sal/internal/theme"
)

// Highlighter applies chroma syntax highlighting to individual diff lines.
//
// Lexers are resolved once per file path (extension-based) and cached, so a
// whole diff pane re-highlights at the cost of one map lookup per line rather
// than re-analysing the language every frame. Every step fails open: an
// unknown extension, a nil lexer, a tokenise error, or NO_COLOR all fall back
// to the original plain text so the diff is never worse than before.
//
// The zero value is not usable; call NewHighlighter. A Highlighter is safe for
// concurrent use (the lexer cache is mutex-guarded), though the TUI drives it
// from a single goroutine.
type Highlighter struct {
	mu        sync.Mutex
	byPath    map[string]chroma.Lexer // path → resolved lexer (nil = "no lexer, stay plain")
	formatter chroma.Formatter
	style     *chroma.Style
	disabled  bool
}

// NewHighlighter builds a Highlighter using a 256-colour terminal formatter
// and a dark syntax theme. When colour is disabled — either the NO_COLOR env
// var is set (https://no-color.org/) or the resolved theme appearance is
// monochrome (e.g. APPR_AI_SAL_THEME=none) — highlighting is turned off
// entirely and Line is a no-op: the diff renders as plain text, keeping the
// syntax layer in lockstep with the (also-monochrome) chrome so the two colour
// sources never disagree.
func NewHighlighter() *Highlighter {
	h := &Highlighter{byPath: map[string]chroma.Lexer{}}
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor || theme.NoColor() {
		h.disabled = true
		return h
	}
	h.formatter = formatters.Get("terminal256")
	if h.formatter == nil {
		h.formatter = formatters.Fallback
	}
	h.style = styles.Get("github-dark")
	if h.style == nil {
		h.style = styles.Fallback
	}
	return h
}

// Active reports whether highlighting is enabled at all (i.e. colour is not
// disabled via NO_COLOR). Callers use it to decide between a highlighted
// render path and the plain fallback so they don't pay for per-line work that
// would be a no-op.
func (h *Highlighter) Active() bool {
	return h != nil && !h.disabled
}

// SupportsFile reports whether a lexer could be resolved for path — i.e.
// whether Line will actually add syntax colour rather than pass the text
// through unchanged. Used by callers that want to decide once per file whether
// to bother pairing lines for word-diff highlighting under a syntax layer.
func (h *Highlighter) SupportsFile(path string) bool {
	if h == nil || h.disabled {
		return false
	}
	return h.lexerFor(path) != nil
}

// Line returns code with chroma syntax highlighting for the language inferred
// from path, or the unmodified code on any failure (unknown language, tokenise
// error, disabled). It highlights a single line in isolation: cross-line
// constructs (block comments, multi-line strings) are not tracked, which keeps
// it cheap and stateless and is an acceptable trade-off for a diff view.
func (h *Highlighter) Line(path, code string) string {
	if h == nil || h.disabled || strings.TrimSpace(code) == "" {
		return code
	}
	lexer := h.lexerFor(path)
	if lexer == nil {
		return code
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var buf bytes.Buffer
	if err := h.formatter.Format(&buf, h.style, it); err != nil {
		return code
	}
	// chroma's terminal formatter appends a trailing newline; strip it so the
	// caller can lay the highlighted text out inline.
	return strings.TrimRight(buf.String(), "\n")
}

// lexerFor resolves (and caches) the lexer for a file path. A nil entry is
// cached too so repeat lookups for an unknown extension stay O(1). The
// returned lexer is coalesced (adjacent same-type tokens merged) so the
// formatter emits the minimum number of SGR spans.
func (h *Highlighter) lexerFor(path string) chroma.Lexer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l, ok := h.byPath[path]; ok {
		return l
	}
	l := lexers.Match(path)
	if l != nil {
		l = chroma.Coalesce(l)
	}
	h.byPath[path] = l
	return l
}

// DetectLanguage returns the chroma lexer name for a file path ("" when no
// lexer matches). Exposed for tests and for callers that want to label the
// detected language; it does not depend on a Highlighter instance so it works
// even under NO_COLOR.
func DetectLanguage(path string) string {
	l := lexers.Match(path)
	if l == nil {
		return ""
	}
	return l.Config().Name
}
