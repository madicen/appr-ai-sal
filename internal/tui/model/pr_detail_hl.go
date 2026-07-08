package model

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
)

// pr_detail_hl.go holds the Phase 5 item-4 diff-pane enhancements that layer on
// top of the plain diff renderer in pr_detail.go: chroma syntax highlighting,
// intra-line word-level emphasis, and the helpers that derive the finding-tag
// anchor rows and search-match rows from the finished, wrapped diff content.
//
// The plain renderer (renderHunkLine, renderDiffPane with a nil highlighter)
// is preserved verbatim so its existing tests and the careful add/del
// background-tint invariant are untouched; the highlighter-active path uses
// renderHunkLineHighlighted, which drops the tint to avoid clobbering it with
// the styled body's embedded SGR resets.

// diffFindingTagMarker is the leading glyph renderInlineFindingTag emits for
// every inline finding annotation. We scan for it (after stripping ANSI) to
// locate finding anchors in the rendered diff without threading row numbers
// through the renderer.
const diffFindingTagMarker = "↳"

// diffBodyStyled returns the styled body for one diff line under the
// highlighter-active path: word-level emphasis for lines that are part of an
// in-place edit pair, otherwise chroma syntax highlighting (fails open to
// plain text on unknown language / tokenise error).
func diffBodyStyled(path string, ln review.DiffLine, wordSegs []diffview.Seg, hl *diffview.Highlighter) string {
	if len(wordSegs) > 0 {
		return renderDiffWordSegs(wordSegs)
	}
	return hl.Line(path, ln.Text)
}

// renderDiffWordSegs emphasises the changed spans of a word-diffed line.
func renderDiffWordSegs(segs []diffview.Seg) string {
	var b strings.Builder
	for _, s := range segs {
		if s.Changed {
			b.WriteString(diffWordChangeStyle.Render(s.Text))
		} else {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

var diffWordChangeStyle = styles.BoldStyle.Underline(true)

// wordDiffForHunk pairs each removed line with the added line that replaced it
// (a maximal removed run immediately followed by an equal-length added run) and
// returns the per-line word-diff segmentation, nil where a line isn't part of a
// clean 1:1 replacement. Mirrors the review-overlay snippet pairing so both
// diff surfaces emphasise the same spans.
func wordDiffForHunk(lines []review.DiffLine) [][]diffview.Seg {
	out := make([][]diffview.Seg, len(lines))
	i := 0
	for i < len(lines) {
		if lines[i].Kind != review.DiffRemoved {
			i++
			continue
		}
		remStart := i
		for i < len(lines) && lines[i].Kind == review.DiffRemoved {
			i++
		}
		remCount := i - remStart
		addStart := i
		for i < len(lines) && lines[i].Kind == review.DiffAdded {
			i++
		}
		addCount := i - addStart
		if remCount == 0 || remCount != addCount {
			continue
		}
		for k := 0; k < remCount; k++ {
			oldSegs, newSegs := diffview.WordDiff(lines[remStart+k].Text, lines[addStart+k].Text)
			out[remStart+k] = oldSegs
			out[addStart+k] = newSegs
		}
	}
	return out
}

// diffRowForNewLine returns the rendered diff row whose new-side gutter number
// equals line (Phase 5 item 4 jump-from-card). It scans the ANSI-stripped rows
// for the "old│new " gutter and parses the number after the "│". Returns
// (0,false) when no row matches (e.g. the diff has no gutter, or the line is
// outside the shown hunks).
func diffRowForNewLine(lines []string, line int) (int, bool) {
	if line <= 0 {
		return 0, false
	}
	for i, ln := range lines {
		plain := ansi.Strip(ln)
		bar := strings.IndexRune(plain, '\u2502')
		if bar < 0 {
			continue
		}
		rest := strings.TrimLeft(plain[bar+len("\u2502"):], " ")
		n := 0
		got := false
		for _, r := range rest {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
			got = true
		}
		if got && n == line {
			return i, true
		}
	}
	return 0, false
}

// diffAnchorRows scans rendered (ANSI-styled) diff lines and returns the row
// indices carrying an inline finding tag — the n/p jump targets (Phase 5 item
// 4). It operates on the final content so it stays correct regardless of how
// lines wrapped.
func diffAnchorRows(lines []string) []int {
	var rows []int
	for i, ln := range lines {
		if strings.Contains(ansi.Strip(ln), diffFindingTagMarker) {
			rows = append(rows, i)
		}
	}
	return rows
}

// existingCommentsByLine groups a PR's existing inline comments (Phase 5 item
// 8) by their anchor line for the given file. Returns nil when there are none,
// so the diff renderer skips the annotation pass entirely.
func existingCommentsByLine(comments []gh.PullReviewComment, path string) map[int][]gh.PullReviewComment {
	if len(comments) == 0 {
		return nil
	}
	out := map[int][]gh.PullReviewComment{}
	for _, c := range comments {
		if c.Path != path || c.Line <= 0 {
			continue
		}
		out[c.Line] = append(out[c.Line], c)
	}
	return out
}

// diffCommentTagMarker is the leading glyph renderInlineCommentTag emits for an
// existing-comment annotation, distinct from the finding-tag ↳ so the two read
// differently in the diff.
const diffCommentTagMarker = "💬"

// renderInlineCommentTag renders one existing PR review comment as an inline
// annotation under its anchor line (Phase 5 item 8), truncated to one line.
func renderInlineCommentTag(c gh.PullReviewComment, contentCols int) string {
	if contentCols < 8 {
		contentCols = 8
	}
	author := c.AuthorLogin
	if author == "" {
		author = "reviewer"
	}
	prefix := "    " + styles.DimStyle.Render(diffCommentTagMarker+" @"+author) + " "
	avail := contentCols - ansi.StringWidth(prefix) - 1
	if avail < 8 {
		avail = 8
	}
	first := strings.SplitN(strings.TrimSpace(c.Body), "\n", 2)[0]
	return prefix + ansi.Truncate(styles.DimStyle.Render(first), avail, "…")
}
