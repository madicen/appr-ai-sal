package detail

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// withTrueColor pins lipgloss to TrueColor for assertions on raw SGR
// codes so the test doesn't depend on terminal capabilities of the
// process running `go test` (CI tty often lacks color and lipgloss
// returns plain text in that case, which would silently bypass the
// background-painting assertions below).
func withTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// untintedPrintableCells walks s and returns the count of printable
// cells that fall outside any SGR span containing a background color
// (CSI param starting with 48). Returns 0 when every printable cell is
// inside a bg-tinted span — the property we want for add/del rows.
func untintedPrintableCells(s string) int {
	parts := strings.Split(s, "\x1b[0m")
	untinted := 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		// Pull off any number of leading SGR sequences and check if
		// any of them set a background. Anything past the SGR cluster
		// counts as printable for this segment.
		rest := p
		hasBg := false
		for strings.HasPrefix(rest, "\x1b[") {
			end := strings.IndexByte(rest, 'm')
			if end < 0 {
				break
			}
			sgr := rest[:end+1]
			if strings.Contains(sgr, ";48;") || strings.HasPrefix(sgr, "\x1b[48;") || strings.HasPrefix(sgr, "\x1b[48m") {
				hasBg = true
			}
			rest = rest[end+1:]
		}
		if hasBg {
			continue
		}
		// Whatever printable content remains is untinted.
		untinted += ansi.StringWidth(rest)
	}
	return untinted
}

// Pane titles sit above viewport content; bubblezone row Y matches scanned lines.
// Wrapped titles add extra lines and shift every row zone vs where users click.
func TestDetailPaneTitleStyledTruncationStaysOneLine(t *testing.T) {
	innerW := 28
	title := "Files · " + styles.BoldStyle.Render("focused (tab to switch)")
	truncated := ansi.Truncate(title, innerW, "…")
	h := lipgloss.Height(styles.DetailPaneTitleStyle.Width(innerW).Render(truncated))
	if h != 1 {
		t.Fatalf("pane title wrapped to %d lines (want 1); mouse hit boxes drift", h)
	}
}

// framePane must render the rounded border on all four sides — historically
// using both Style.Height(h) and MaxHeight(h) clipped the bottom border (and
// the right border for the rightmost pane on screen) because Style.Height/
// Style.Width set INTERIOR dimensions while MaxHeight/MaxWidth cap TOTAL
// dimensions; setting both to outerH inflates total to outerH+2 and then
// clips two rows.
func TestDetailRenderedPRDetailBodyHasBottomBorders(t *testing.T) {
	m := detailFixtureModel(t)
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	// The body region sits between header (row 0) and status (last row).
	// Find the last non-empty body line that contains a bottom border arc;
	// every pane's bottom border has '╰' and '╯'.
	foundBottom := false
	for _, ln := range lines {
		if strings.Contains(ln, "╰") && strings.Contains(ln, "╯") {
			foundBottom = true
			break
		}
	}
	if !foundBottom {
		t.Fatalf("no bottom border (╰…╯) row found in rendered detail body — borders are clipped:\n%s", out)
	}
}

// The rightmost pane (controls) must render its right vertical border. With
// the Width/MaxWidth bug, the right border of the rightmost pane is clipped;
// adjacent panes hide their missing right border under the next pane's left
// border in the horizontal join.
func TestDetailRightmostPaneHasRightBorder(t *testing.T) {
	m := detailFixtureModel(t)
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	// Look for any line whose trailing run of non-space characters ends in
	// '│' — that's the right edge of the rightmost framed pane on a body
	// row. We accept trailing whitespace from terminal padding.
	any := false
	for _, ln := range lines {
		trimmed := strings.TrimRight(ln, " ")
		if strings.HasSuffix(trimmed, "│") {
			any = true
			break
		}
	}
	if !any {
		t.Fatalf("no body row ends in a right-border '│' — rightmost pane border is clipped:\n%s", out)
	}
}

// formatGutter for context lines must render BOTH old and new line numbers
// separated by '│', with total printable width exactly diffGutterWidth.
func TestFormatGutterContext(t *testing.T) {
	g := ansi.Strip(formatGutter(review.DiffLine{Kind: review.DiffContext, OldNo: 12, NewNo: 34}))
	if w := ansi.StringWidth(g); w != diffGutterWidth {
		t.Fatalf("gutter width=%d, want %d (got %q)", w, diffGutterWidth, g)
	}
	if !strings.Contains(g, "12") || !strings.Contains(g, "34") {
		t.Fatalf("context gutter should show both line numbers; got %q", g)
	}
	if !strings.Contains(g, "\u2502") {
		t.Fatalf("context gutter should contain '│' separator; got %q", g)
	}
}

// Addition rows show only the new (right) line number; left side is
// blank so the eye doesn't pair the +line with a stale old-line value.
func TestFormatGutterAddition(t *testing.T) {
	g := ansi.Strip(formatGutter(review.DiffLine{Kind: review.DiffAdded, NewNo: 42}))
	if w := ansi.StringWidth(g); w != diffGutterWidth {
		t.Fatalf("gutter width=%d, want %d", w, diffGutterWidth)
	}
	if !strings.Contains(g, "42") {
		t.Fatalf("addition gutter should show new line number; got %q", g)
	}
	// Left of '│' must be blank (4 spaces).
	sep := strings.Index(g, "\u2502")
	if sep < 0 {
		t.Fatalf("missing '│' in %q", g)
	}
	if strings.TrimSpace(g[:sep]) != "" {
		t.Fatalf("addition: left of │ should be blank; got %q", g[:sep])
	}
}

// Deletion rows show only the old (left) line number; right side blank.
func TestFormatGutterDeletion(t *testing.T) {
	g := ansi.Strip(formatGutter(review.DiffLine{Kind: review.DiffRemoved, OldNo: 7}))
	if w := ansi.StringWidth(g); w != diffGutterWidth {
		t.Fatalf("gutter width=%d, want %d", w, diffGutterWidth)
	}
	sep := strings.Index(g, "\u2502")
	if sep < 0 {
		t.Fatalf("missing '│' in %q", g)
	}
	if !strings.Contains(g[:sep], "7") {
		t.Fatalf("deletion: left should show old line; got %q", g[:sep])
	}
	if strings.TrimSpace(g[sep+len("\u2502"):]) != "" {
		t.Fatalf("deletion: right of │ should be blank; got %q", g[sep+len("\u2502"):])
	}
}

// Wrapped continuation rows must NOT re-emit line numbers — they should
// start with a blank-width gutter so the body column stays aligned but
// readers don't see ghost duplicate numbers.
func TestRenderHunkLineLongLineWrapContinuationHasBlankGutter(t *testing.T) {
	// Build a body that's clearly wider than bodyW so wrapping triggers.
	body := strings.Repeat("abcdefghij", 10) // 100 cells
	ln := review.DiffLine{Kind: review.DiffAdded, NewNo: 99, Text: body}
	out := renderHunkLine(ln, 50, true) // contentCols=50, bodyW = 50 - 12 = 38
	// Strip ANSI styling for assertions on raw column layout.
	plain := ansi.Strip(out)
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapping to produce >=2 lines; got %d:\n%q", len(lines), plain)
	}
	// First line carries the gutter and number; continuation lines must
	// start with diffGutterWidth blanks.
	if !strings.Contains(lines[0], "99") {
		t.Fatalf("first line should contain line number 99; got %q", lines[0])
	}
	for i := 1; i < len(lines); i++ {
		if len(lines[i]) < diffGutterWidth {
			t.Fatalf("continuation row %d shorter than gutter width: %q", i, lines[i])
		}
		prefix := lines[i][:diffGutterWidth]
		if strings.TrimSpace(prefix) != "" {
			t.Fatalf("continuation row %d gutter must be blank; got %q (full=%q)", i, prefix, lines[i])
		}
	}
}

// Hunk headers must consume a leading blank-width gutter so they align
// with the content rows below — otherwise the @@ line floats two cells
// to the left of every other line and the eye loses the column.
func TestRenderDiffPaneHunkHeaderColumnAligned(t *testing.T) {
	files := review.ParseDiff(threeFileDiff)
	if len(files) == 0 {
		t.Fatal("threeFileDiff parsed empty")
	}
	out := renderDiffPane(&files[0], nil, true, 80)
	plain := ansi.Strip(out)
	// Find the '@@' line.
	var hunkLine string
	for _, ln := range strings.Split(plain, "\n") {
		if strings.Contains(ln, "@@") {
			hunkLine = ln
			break
		}
	}
	if hunkLine == "" {
		t.Fatalf("no hunk header line found in:\n%s", plain)
	}
	prefix := hunkLine[:diffGutterWidth]
	if strings.TrimSpace(prefix) != "" {
		t.Fatalf("hunk header should be preceded by %d-cell blank gutter; got %q", diffGutterWidth, prefix)
	}
}

// On a narrow pane (contentCols < diffGutterMinPaneWidth) the gutter is
// dropped so the body stays readable. Verify by rendering at a width
// just below the threshold and asserting no '│' appears in the output.
func TestRenderDiffPaneNarrowFallsBackToNoGutter(t *testing.T) {
	files := review.ParseDiff(threeFileDiff)
	if len(files) == 0 {
		t.Fatal("threeFileDiff parsed empty")
	}
	out := renderDiffPane(&files[0], nil, true, diffGutterMinPaneWidth-1)
	plain := ansi.Strip(out)
	if strings.Contains(plain, "\u2502") {
		t.Fatalf("narrow pane should drop gutter; '│' still present:\n%s", plain)
	}
}

// Add/del rows must paint their background tint edge-to-edge. The bug
// we're guarding against here was that the dim-styled gutter emitted an
// embedded SGR reset (\x1b[0m) which silently cleared the outer bg for
// every cell after the gutter — so the user saw a 10-cell band of
// color and then nothing, even though the row was logically a "+"/"-"
// row across the full width.
func TestRenderHunkLineAddedRowBgPaintsFullWidth(t *testing.T) {
	withTrueColor(t)
	ln := review.DiffLine{Kind: review.DiffAdded, NewNo: 5, Text: "func foo() {"}
	out := renderHunkLine(ln, 60, true)
	// One physical line for this short body.
	if strings.Contains(out, "\n") {
		t.Fatalf("expected single physical line; got\n%q", out)
	}
	if got := untintedPrintableCells(out); got != 0 {
		t.Fatalf("added row has %d untinted printable cell(s); bg must paint full width\nraw: %q", got, out)
	}
}

func TestRenderHunkLineRemovedRowBgPaintsFullWidth(t *testing.T) {
	withTrueColor(t)
	ln := review.DiffLine{Kind: review.DiffRemoved, OldNo: 9, Text: "old line"}
	out := renderHunkLine(ln, 60, true)
	if got := untintedPrintableCells(out); got != 0 {
		t.Fatalf("removed row has %d untinted printable cell(s); bg must paint full width\nraw: %q", got, out)
	}
}

// Wrapped continuation rows must keep the bg band running edge-to-edge
// too — otherwise a long add/del line shows a tinted first physical row
// and untinted continuation rows, which reads as a visual seam.
func TestRenderHunkLineAddedWrapsKeepBgEdgeToEdge(t *testing.T) {
	withTrueColor(t)
	body := strings.Repeat("abcdefghij", 8) // 80 cells; will wrap at width 50
	ln := review.DiffLine{Kind: review.DiffAdded, NewNo: 1, Text: body}
	out := renderHunkLine(ln, 50, true)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapping; got 1 line: %q", out)
	}
	for i, line := range lines {
		if got := untintedPrintableCells(line); got != 0 {
			t.Fatalf("wrapped row %d has %d untinted cell(s); raw=%q", i, got, line)
		}
	}
}

// Context (no add/del) rows are deliberately untinted: the test asserts
// the previous "no bg" behavior for plain context lines so we don't
// accidentally regress to coloring the entire diff.
func TestRenderHunkLineContextRowHasNoBg(t *testing.T) {
	withTrueColor(t)
	ln := review.DiffLine{Kind: review.DiffContext, OldNo: 1, NewNo: 1, Text: "  unchanged"}
	out := renderHunkLine(ln, 60, true)
	if strings.Contains(out, "\x1b[48;") {
		t.Fatalf("context row should not carry any bg SGR; got %q", out)
	}
}

// In the no-gutter narrow-pane fallback, add/del rows should still
// paint as tinted bands so the user can scan the diff visually even
// without line numbers.
func TestRenderHunkLineNoGutterAddedRowBgPaintsFullWidth(t *testing.T) {
	withTrueColor(t)
	ln := review.DiffLine{Kind: review.DiffAdded, NewNo: 1, Text: "x"}
	out := renderHunkLine(ln, 18, false) // contentCols below diffGutterMinPaneWidth
	if got := untintedPrintableCells(out); got != 0 {
		t.Fatalf("no-gutter added row has %d untinted cell(s); raw=%q", got, out)
	}
}
