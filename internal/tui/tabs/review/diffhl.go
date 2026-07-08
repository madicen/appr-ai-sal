package review

import (
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
)

// diffhl.go wires the diffview leaf helpers (Phase 5 item 4) into the focused
// card's diff snippet: chroma syntax highlighting for context/whole-line
// changes, and intra-line word-level emphasis for lines that were edited in
// place (a removed line paired with the added line that replaced it).
//
// The snippet renderer here is deliberately background-tint-free (unlike the
// PR-detail diff pane), so layering chroma / word-diff spans on top of the
// text is safe: there's no row background whose SGR span a mid-line reset
// could clobber.

// highlighter lazily builds (and caches) the model's chroma highlighter. It's
// created on first use so NO_COLOR is honoured at render time and the existing
// constructors don't all need updating.
func (m *Model) highlighter() *diffview.Highlighter {
	if m.hl == nil {
		m.hl = diffview.NewHighlighter()
	}
	return m.hl
}

// snippetStyledBody produces the styled body text for one snippet line.
//
//   - Context lines and whole-line insertions/deletions are syntax-highlighted
//     via chroma (fails open to plain text on unknown language / NO_COLOR).
//   - Changed lines that are part of an in-place edit pair get word-level
//     emphasis instead: the differing spans are underlined+bold so the eye
//     lands on exactly what changed, and the common spans stay plain.
//
// wordSegs is nil unless this line is one side of a matched changed pair.
func snippetStyledBody(path, text string, wordSegs []diffview.Seg, hl *diffview.Highlighter) string {
	if len(wordSegs) > 0 {
		return renderWordSegs(wordSegs)
	}
	return hl.Line(path, text)
}

// renderWordSegs renders word-diff segments: changed spans emphasised
// (underline + bold), unchanged spans left as plain text.
func renderWordSegs(segs []diffview.Seg) string {
	var b strings.Builder
	for _, s := range segs {
		if s.Changed {
			b.WriteString(wordChangeStyle.Render(s.Text))
		} else {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// wordChangeStyle emphasises the differing spans on a changed line.
var wordChangeStyle = styles.BoldStyle.Underline(true)

// wordDiffForSnippet pairs removed lines with the added lines that replaced
// them within a snippet window and returns, per snippet-line index, the
// word-diff segmentation for that line (nil when the line isn't part of a
// 1:1 changed pair). A "pair" is a maximal run of removed lines immediately
// followed by an equal-length run of added lines; the i-th removed line is
// diffed against the i-th added line. Runs of unequal length are treated as
// whole-line changes (no intra-line emphasis) because there's no unambiguous
// pairing.
func wordDiffForSnippet(lines []review.DiffLine) [][]diffview.Seg {
	out := make([][]diffview.Seg, len(lines))
	i := 0
	for i < len(lines) {
		if lines[i].Kind != review.DiffRemoved {
			i++
			continue
		}
		// Collect the run of removed lines.
		remStart := i
		for i < len(lines) && lines[i].Kind == review.DiffRemoved {
			i++
		}
		remCount := i - remStart
		// Collect the immediately-following run of added lines.
		addStart := i
		for i < len(lines) && lines[i].Kind == review.DiffAdded {
			i++
		}
		addCount := i - addStart
		if remCount == 0 || remCount != addCount {
			continue // not a clean 1:1 replacement; leave as whole-line change
		}
		for k := 0; k < remCount; k++ {
			oldSegs, newSegs := diffview.WordDiff(lines[remStart+k].Text, lines[addStart+k].Text)
			out[remStart+k] = oldSegs
			out[addStart+k] = newSegs
		}
	}
	return out
}
