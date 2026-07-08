package model

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestDiffAnchorRowsFindsFindingTags(t *testing.T) {
	lines := []string{
		"  1│  1 context",
		"    ↳ security warning here",
		"  2│  2 more",
		"    ↳ style note",
		"  3│  3 tail",
	}
	rows := diffAnchorRows(lines)
	want := []int{1, 3}
	if len(rows) != len(want) {
		t.Fatalf("anchor rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %d, want %d", i, rows[i], want[i])
		}
	}
}

func TestDiffRowForNewLineMatchesGutter(t *testing.T) {
	lines := []string{
		"@@ hunk",
		"   1│   1 first",
		"    │   2 added",
		"   3│   3 third",
	}
	row, ok := diffRowForNewLine(lines, 3)
	if !ok {
		t.Fatal("expected to find line 3")
	}
	if row != 3 {
		t.Errorf("row for line 3 = %d, want 3", row)
	}
	if _, ok := diffRowForNewLine(lines, 99); ok {
		t.Error("line 99 should not be found")
	}
	if _, ok := diffRowForNewLine(lines, 0); ok {
		t.Error("line 0 is invalid and should not match")
	}
}

func TestExistingCommentsByLineFiltersPath(t *testing.T) {
	comments := []gh.PullReviewComment{
		{Path: "a.go", Line: 5, Body: "one"},
		{Path: "a.go", Line: 5, Body: "two"},
		{Path: "b.go", Line: 5, Body: "other file"},
		{Path: "a.go", Line: 0, Body: "no anchor"},
	}
	got := existingCommentsByLine(comments, "a.go")
	if len(got[5]) != 2 {
		t.Fatalf("a.go:5 should have 2 comments, got %d", len(got[5]))
	}
	if _, ok := got[0]; ok {
		t.Error("line 0 (no anchor) must be excluded")
	}
	if existingCommentsByLine(nil, "a.go") != nil {
		t.Error("nil input should yield nil map")
	}
}

func TestWordDiffForHunkPairsReplacement(t *testing.T) {
	lines := []review.DiffLine{
		{Kind: review.DiffContext, Text: "ctx"},
		{Kind: review.DiffRemoved, Text: "x := 1"},
		{Kind: review.DiffAdded, Text: "x := 2"},
	}
	segs := wordDiffForHunk(lines)
	if segs[0] != nil {
		t.Error("context line should have no segments")
	}
	if segs[1] == nil || segs[2] == nil {
		t.Fatal("the replacement pair should be word-diffed")
	}
}

func TestRenderInlineCommentTagShowsAuthorAndBody(t *testing.T) {
	c := gh.PullReviewComment{AuthorLogin: "octocat", Body: "please rename this\nsecond line", Line: 4, Path: "a.go"}
	out := ansi.Strip(renderInlineCommentTag(c, 80))
	if !strings.Contains(out, "octocat") {
		t.Errorf("tag missing author: %q", out)
	}
	if !strings.Contains(out, "please rename this") {
		t.Errorf("tag missing first body line: %q", out)
	}
	if strings.Contains(out, "second line") {
		t.Errorf("tag should only show the first line: %q", out)
	}
}

func TestRenderDiffPaneHLNilIsUnchanged(t *testing.T) {
	files := review.ParseDiff(threeFileDiff)
	if len(files) == 0 {
		t.Fatal("threeFileDiff parsed empty")
	}
	// nil highlighter + nil comments must equal the plain renderDiffPane output.
	a := renderDiffPane(&files[0], nil, true, 80)
	b := renderDiffPaneHL(&files[0], nil, true, 80, nil, nil)
	if a != b {
		t.Error("renderDiffPaneHL with nil hl/comments must match renderDiffPane exactly")
	}
}
