package repocontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanguageForName(t *testing.T) {
	cases := map[string]string{
		"main.go":         "go",
		"app.py":          "python",
		"Component.tsx":   "ts",
		"helpers.test.ts": "ts",
		"main.tf":         "hcl",
		"lib.rs":          "rust",
		"Item.kt":         "kotlin",
		"README.md":       "markdown",
		"build.sh":        "shell",
		"queries.sql":     "sql",
		"settings.yaml":   "yaml",
		"package.json":    "json",
		"unknown.bizarre": "other",
	}
	for name, want := range cases {
		if got := languageForName(name); got != want {
			t.Errorf("languageForName(%q) = %q want %q", name, got, want)
		}
	}
}

func TestIsTestName(t *testing.T) {
	cases := []struct {
		name string
		lang string
		want bool
	}{
		{"foo_test.go", "go", true},
		{"foo.go", "go", false},
		{"test_app.py", "python", true},
		{"app_test.py", "python", true},
		{"Component.test.tsx", "ts", true},
		{"Component.spec.ts", "ts", true},
		{"Component.tsx", "ts", false},
		{"app_spec.rb", "ruby", true},
	}
	for _, tc := range cases {
		if got := isTestName(tc.name, tc.lang); got != tc.want {
			t.Errorf("isTestName(%q,%q)=%v want %v", tc.name, tc.lang, got, tc.want)
		}
	}
}

func TestBuildEvidenceGoSiblingTestAndDocGo(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "internal", "stuff")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pkgDir, "stuff.go"), `package stuff

// DoSomething runs the thing.
func DoSomething() {}

func unexportedHelper() {}

// IntendedType is exported.
type IntendedType struct{}

type unexportedType struct{}
`)
	mustWrite(t, filepath.Join(pkgDir, "stuff_test.go"), `package stuff

import "testing"

func TestDoSomething(t *testing.T) {
	DoSomething()
}
`)
	mustWrite(t, filepath.Join(pkgDir, "doc.go"), "// Package stuff does stuff.\npackage stuff\n")

	ev, err := BuildEvidence(context.Background(), EvidenceOptions{
		Worktree:     dir,
		ChangedPaths: []string{"internal/stuff/stuff.go", "internal/stuff/stuff_test.go"},
		MaxBytes:     8000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ev.Aggregates.ChangedSourceFiles; got != 1 {
		t.Fatalf("changed source files = %d, want 1", got)
	}
	if got := ev.Aggregates.ChangedSourceFilesWithSiblingTest; got != 1 {
		t.Fatalf("changed source files with sibling test = %d, want 1", got)
	}
	if got := ev.Aggregates.ChangedSourceFilesInPackageWithDocGo; got != 1 {
		t.Fatalf("changed source files in pkg with doc.go = %d, want 1", got)
	}
	if got := ev.Aggregates.TotalExportedSymbolsTouched; got != 2 {
		t.Fatalf("exported symbols touched = %d, want 2", got)
	}
	if got := ev.Aggregates.TotalDocumentedExportedSymbolsTouched; got != 2 {
		t.Fatalf("documented exported symbols touched = %d, want 2", got)
	}
	md := FormatEvidenceMarkdown(ev, 4096)
	if !strings.Contains(md, "Changed source files") {
		t.Fatalf("missing aggregates header: %q", md)
	}
	if !strings.Contains(md, "stuff_test.go") {
		t.Fatalf("missing sibling test mention: %q", md)
	}
	if !strings.Contains(md, "Representative existing test") {
		t.Fatalf("missing representative test header: %q", md)
	}
}

func TestBuildEvidenceMissingSiblingAndUndocumented(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pkgDir, "untested.go"), `package pkg

func ExportedNoDoc() {}

type AnotherExported int
`)
	ev, err := BuildEvidence(context.Background(), EvidenceOptions{
		Worktree:     dir,
		ChangedPaths: []string{"pkg/untested.go"},
		MaxBytes:     2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Aggregates.ChangedSourceFilesWithSiblingTest != 0 {
		t.Fatalf("expected no sibling test, got %d", ev.Aggregates.ChangedSourceFilesWithSiblingTest)
	}
	if ev.Aggregates.TotalExportedSymbolsTouched != 2 {
		t.Fatalf("exported = %d, want 2", ev.Aggregates.TotalExportedSymbolsTouched)
	}
	if ev.Aggregates.TotalDocumentedExportedSymbolsTouched != 0 {
		t.Fatalf("documented = %d, want 0", ev.Aggregates.TotalDocumentedExportedSymbolsTouched)
	}
	md := FormatEvidenceMarkdown(ev, 4096)
	if !strings.Contains(md, "no sibling test") {
		t.Fatalf("expected sibling-test absence note: %q", md)
	}
}

func TestBuildEvidencePythonDocstring(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "lib.py"), `def documented_fn():
    """Docstring."""
    return 1


def undocumented_fn():
    return 2


class _Internal:
    pass


class Public:
    """Class docstring."""
    pass
`)
	ev, err := BuildEvidence(context.Background(), EvidenceOptions{
		Worktree:     dir,
		ChangedPaths: []string{"lib.py"},
		MaxBytes:     4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Aggregates.TotalExportedSymbolsTouched != 3 {
		t.Fatalf("exported = %d, want 3", ev.Aggregates.TotalExportedSymbolsTouched)
	}
	if ev.Aggregates.TotalDocumentedExportedSymbolsTouched != 2 {
		t.Fatalf("documented = %d, want 2", ev.Aggregates.TotalDocumentedExportedSymbolsTouched)
	}
}

func TestFormatEvidenceMarkdownEmpty(t *testing.T) {
	if got := FormatEvidenceMarkdown(nil, 1024); got != "" {
		t.Fatalf("expected empty: %q", got)
	}
	ev := &Evidence{}
	if got := FormatEvidenceMarkdown(ev, 1024); got != "" {
		t.Fatalf("expected empty for no files: %q", got)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
