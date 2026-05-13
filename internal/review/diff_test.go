package review

import "testing"

// TestFindUniqueExcerptInFile_HappyPath locks in the canonical re-anchor
// scenario: a model-provided excerpt that matches exactly one line in the
// file's hunks returns that line and ok=true. This is the case the TUI
// uses to silently move a finding whose original Line fell outside any
// hunk after a force-push.
func TestFindUniqueExcerptInFile_HappyPath(t *testing.T) {
	diff := `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,5 +1,5 @@
 package x
-old comment line that was removed
+a uniquely identifiable line that we anchor at
 import "fmt"
 
 func F() {}
`
	files := ParseDiff(diff)
	if len(files) != 1 {
		t.Fatalf("parse failed: %d files", len(files))
	}
	line, ok := FindUniqueExcerptInFile(&files[0], "a uniquely identifiable line that we anchor at")
	if !ok {
		t.Fatalf("expected unique match, got ok=false")
	}
	if line != 2 {
		t.Errorf("matched line %d, want 2 (the post-image line number for the +line)", line)
	}
}

// TestFindUniqueExcerptInFile_RejectsAmbiguous covers the safety case: an
// excerpt that appears on two lines in the same file is treated as a
// non-match. Re-anchoring on an ambiguous excerpt would silently move the
// finding to whichever line the search visited first, which is worse than
// leaving the card with no anchor (the file-level fallback is the right
// move there).
func TestFindUniqueExcerptInFile_RejectsAmbiguous(t *testing.T) {
	diff := `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,6 +1,6 @@
 package x
-old1
+the same long enough string of text to pass the length floor
 import "fmt"
-old2
+the same long enough string of text to pass the length floor
 func F() {}
`
	files := ParseDiff(diff)
	if _, ok := FindUniqueExcerptInFile(&files[0], "the same long enough string of text to pass the length floor"); ok {
		t.Error("expected ambiguous excerpt to be rejected (ok=false), got ok=true")
	}
}

// TestFindUniqueExcerptInFile_RejectsShortExcerpts encodes the deliberate
// floor: lines under 20 chars are too generic to safely relocate. Lines
// like "}" or "return nil" recur all over real files and a confident
// re-anchor against them would be a foot-gun. The threshold mirrors the
// posture documented in anchor_excerpt.go.
func TestFindUniqueExcerptInFile_RejectsShortExcerpts(t *testing.T) {
	diff := `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,3 +1,3 @@
 package x
-old
+}
 func F() {}
`
	files := ParseDiff(diff)
	if _, ok := FindUniqueExcerptInFile(&files[0], "}"); ok {
		t.Error("expected short excerpt to be rejected, got ok=true")
	}
}

// TestFindUniqueExcerptInFile_NilFile guards the trivial nil-safety case;
// callers can pass a *FileDiff straight from FindFile (which returns nil
// when the path isn't in the diff) without a separate guard.
func TestFindUniqueExcerptInFile_NilFile(t *testing.T) {
	if _, ok := FindUniqueExcerptInFile(nil, "any reasonably long string here"); ok {
		t.Error("nil file should yield ok=false")
	}
}

// TestFindUniqueExcerptInFile_WhitespaceTolerant confirms the search uses
// normaliseExcerpt under the hood — so the model can quote a line with
// different whitespace runs (tabs vs spaces, trailing spaces) than the
// diff and still match. Reuses the same shape anchor_excerpt.go uses
// for its existing mismatch check, so the two gates agree on what counts
// as the "same" line.
func TestFindUniqueExcerptInFile_WhitespaceTolerant(t *testing.T) {
	// The diff line has two spaces of indent; the model's excerpt has a tab.
	diff := "diff --git a/x.go b/x.go\n" +
		"--- a/x.go\n" +
		"+++ b/x.go\n" +
		"@@ -1,3 +1,3 @@\n" +
		" package x\n" +
		"-old\n" +
		"+  if foo := bar(); foo != nil { return foo }\n" +
		" func F() {}\n"
	files := ParseDiff(diff)
	line, ok := FindUniqueExcerptInFile(&files[0], "\tif foo := bar(); foo != nil { return foo }")
	if !ok {
		t.Fatalf("expected whitespace-normalised match, got ok=false")
	}
	if line != 2 {
		t.Errorf("matched line %d, want 2", line)
	}
}
