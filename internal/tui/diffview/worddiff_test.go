package diffview

import "testing"

// reassemble concatenates a side's segments; it must equal the original input.
func reassemble(segs []Seg) string {
	var s string
	for _, seg := range segs {
		s += seg.Text
	}
	return s
}

// changedText joins just the changed spans of a side.
func changedText(segs []Seg) string {
	var s string
	for _, seg := range segs {
		if seg.Changed {
			s += seg.Text
		}
	}
	return s
}

func TestWordDiffIsolatesChangedToken(t *testing.T) {
	old := "result := foo(bar)"
	new := "result := foo(baz)"
	oldSegs, newSegs := WordDiff(old, new)
	if reassemble(oldSegs) != old {
		t.Fatalf("old segments must reassemble to old: got %q", reassemble(oldSegs))
	}
	if reassemble(newSegs) != new {
		t.Fatalf("new segments must reassemble to new: got %q", reassemble(newSegs))
	}
	if got := changedText(oldSegs); got != "bar" {
		t.Errorf("old changed span = %q, want %q", got, "bar")
	}
	if got := changedText(newSegs); got != "baz" {
		t.Errorf("new changed span = %q, want %q", got, "baz")
	}
}

func TestWordDiffIdenticalLines(t *testing.T) {
	oldSegs, newSegs := WordDiff("same line", "same line")
	if len(oldSegs) != 1 || oldSegs[0].Changed {
		t.Errorf("identical old should be one unchanged seg, got %+v", oldSegs)
	}
	if len(newSegs) != 1 || newSegs[0].Changed {
		t.Errorf("identical new should be one unchanged seg, got %+v", newSegs)
	}
}

func TestWordDiffEmptySides(t *testing.T) {
	oldSegs, newSegs := WordDiff("", "added text")
	if len(oldSegs) != 0 {
		t.Errorf("empty old should yield no segments, got %+v", oldSegs)
	}
	if changedText(newSegs) != "added text" {
		t.Errorf("all of new should be changed, got %q", changedText(newSegs))
	}
	oldSegs, newSegs = WordDiff("removed text", "")
	if len(newSegs) != 0 {
		t.Errorf("empty new should yield no segments, got %+v", newSegs)
	}
	if changedText(oldSegs) != "removed text" {
		t.Errorf("all of old should be changed, got %q", changedText(oldSegs))
	}
}

func TestWordDiffInsertionAndDeletion(t *testing.T) {
	old := "call(a, c)"
	new := "call(a, b, c)"
	oldSegs, newSegs := WordDiff(old, new)
	if reassemble(oldSegs) != old || reassemble(newSegs) != new {
		t.Fatal("segments must reassemble to their inputs")
	}
	// Only the new side gains "b" (plus its separators); the old side has no
	// changed word token.
	if got := changedText(newSegs); got == "" {
		t.Errorf("expected an insertion on the new side, got none")
	}
	// The common "call(a," and ", c)" fragments must not be flagged changed on
	// the old side (nothing was removed from old).
	if got := changedText(oldSegs); got != "" {
		// A pure insertion means old has no deletions.
		t.Errorf("old side should have no changed spans for a pure insertion, got %q", got)
	}
}

func TestWordDiffMergesAdjacentSameState(t *testing.T) {
	// "abc def" -> "xyz def": the leading word changes, trailing unchanged.
	oldSegs, newSegs := WordDiff("abc def", "xyz def")
	// The unchanged " def" tail should be a single merged segment on each side.
	if n := countChanged(oldSegs, false); n != 1 {
		t.Errorf("expected one merged unchanged run on old side, got %d segs: %+v", n, oldSegs)
	}
	if n := countChanged(newSegs, false); n != 1 {
		t.Errorf("expected one merged unchanged run on new side, got %d segs: %+v", n, newSegs)
	}
}

func countChanged(segs []Seg, changed bool) int {
	n := 0
	for _, s := range segs {
		if s.Changed == changed {
			n++
		}
	}
	return n
}
