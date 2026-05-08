package gh

import (
	"testing"
	"time"
)

func TestClassifyPRTouches(t *testing.T) {
	row := MergedPRDigestRow{Number: 12, Title: "fix", URL: "u", UpdatedAt: time.Now()}
	files := []PRFile{
		{Path: "internal/review/foo.go", Additions: 10},
		{Path: "internal/review/foo_test.go", Additions: 6},
		{Path: "README.md", Additions: 2},
		{Path: "vendor/x/y.go", Additions: 1},
	}
	dirs := pathParentDirs([]string{"internal/review/bar.go"})
	got, ok := classifyPRTouches(row, files, dirs)
	if !ok {
		t.Fatal("expected match")
	}
	if got.SourceFiles != 1 {
		t.Fatalf("source = %d, want 1", got.SourceFiles)
	}
	if got.TestFiles != 1 {
		t.Fatalf("test = %d, want 1", got.TestFiles)
	}
	if got.DocFiles != 1 {
		t.Fatalf("doc = %d, want 1", got.DocFiles)
	}
	if !got.HasTests() || !got.HasDocs() {
		t.Fatal("expected HasTests and HasDocs")
	}
}

func TestClassifyPRTouchesNoMatch(t *testing.T) {
	row := MergedPRDigestRow{Number: 7}
	files := []PRFile{
		{Path: "unrelated/x.go", Additions: 3},
	}
	dirs := pathParentDirs([]string{"internal/review/foo.go"})
	if _, ok := classifyPRTouches(row, files, dirs); ok {
		t.Fatal("expected no match")
	}
}

func TestPathDirMatches(t *testing.T) {
	dirs := pathParentDirs([]string{"internal/review/foo.go", "cmd/main.go"})
	cases := map[string]bool{
		"internal/review":     true,
		"internal/review/sub": true,
		"internal":            true,  // parent of one of the wanted dirs
		"cmd":                 true,
		"docs":                false,
		"":                    false,
	}
	for d, want := range cases {
		if got := pathDirMatches(d, dirs); got != want {
			t.Errorf("pathDirMatches(%q) = %v want %v", d, got, want)
		}
	}
}

func TestRootDocFile(t *testing.T) {
	cases := map[string]bool{
		"README.md":     true,
		"CHANGELOG":     true,
		"docs/x.md":     true,
		"docs/sub/y.md": true,
		"src/foo.md":    false,
	}
	for p, want := range cases {
		if got := isRootDocFile(p); got != want {
			t.Errorf("isRootDocFile(%q) = %v want %v", p, got, want)
		}
	}
}

func TestIsPRTestFileName(t *testing.T) {
	cases := map[string]bool{
		"foo_test.go":   true,
		"foo.go":        false,
		"test_app.py":   true,
		"app_test.py":   true,
		"x.test.tsx":    true,
		"x.spec.js":     true,
		"x.ts":          false,
		"foo_spec.rb":   true,
		"FooTest.java":  true,
		"FooTests.kt":   true,
		"random.txt":    false,
	}
	for p, want := range cases {
		if got := isPRTestFileName(p); got != want {
			t.Errorf("isPRTestFileName(%q) = %v want %v", p, got, want)
		}
	}
}

func TestIsPRDocFileName(t *testing.T) {
	cases := map[string]bool{
		"README.md":   true,
		"docs/foo.md": true,
		"foo.rst":     true,
		"main.go":     false,
		"CHANGELOG":   true,
	}
	for p, want := range cases {
		if got := isPRDocFileName(p); got != want {
			t.Errorf("isPRDocFileName(%q) = %v want %v", p, got, want)
		}
	}
}

func TestIsPRSourceFileName(t *testing.T) {
	cases := map[string]bool{
		"foo.go":   true,
		"foo.py":   true,
		"foo.tsx":  true,
		"foo.tf":   true,
		"foo.txt":  false,
		"foo.md":   false,
	}
	for p, want := range cases {
		if got := isPRSourceFileName(p); got != want {
			t.Errorf("isPRSourceFileName(%q) = %v want %v", p, got, want)
		}
	}
}
