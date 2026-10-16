package evals

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestCheckSuggestionCompileGoOK(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "calc", "compute.go"), "package calc\n\nfunc Compute(items []int) int {\n\ttotal:=0\n\treturn total\n}\n")
	f := review.Finding{
		Path:       "calc/compute.go",
		Line:       4,
		Suggestion: "\ttotal := 0",
	}
	got := checkSuggestionCompile(dir, f)
	if got.Status != compileOK {
		t.Fatalf("status=%s err=%v, want ok", got.Status, got.Err)
	}
}

func TestCheckSuggestionCompileGoFail(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "calc", "compute.go"), "package calc\n\nfunc Compute(items []int) int {\n\ttotal := 0\n\treturn total\n}\n")
	f := review.Finding{
		Path:       "calc/compute.go",
		Line:       4,
		Suggestion: "\ttotal := {", // syntactically invalid — gofmt -e rejects it
	}
	got := checkSuggestionCompile(dir, f)
	if got.Status != compileFail {
		t.Fatalf("status=%s, want fail", got.Status)
	}
}

func TestCheckSuggestionCompileToolMissing(t *testing.T) {
	if _, err := exec.LookPath("tsc"); err == nil {
		t.Skip("tsc present; cannot assert soft-skip")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src", "a.ts"), " cons x = 1\n")
	f := review.Finding{Path: "src/a.ts", Line: 1, Suggestion: "const x = 1"}
	got := checkSuggestionCompile(dir, f)
	if got.Status != compileSkipTool {
		t.Fatalf("status=%s, want skip_tool", got.Status)
	}
}

func TestCheckSuggestionCompileNoFile(t *testing.T) {
	got := checkSuggestionCompile(t.TempDir(), review.Finding{
		Path: "missing.go", Line: 1, Suggestion: "x",
	})
	if got.Status != compileSkipNoFile {
		t.Fatalf("status=%s, want skip_no_file", got.Status)
	}
}

func TestApplySuggestionReplacesLine(t *testing.T) {
	in := "a\nold\nc\n"
	out, err := applySuggestion(in, 2, 2, "new")
	if err != nil {
		t.Fatal(err)
	}
	if out != "a\nnew\nc\n" {
		t.Fatalf("got %q", out)
	}
}
