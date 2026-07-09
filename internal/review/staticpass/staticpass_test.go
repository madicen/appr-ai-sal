package staticpass

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTool is a test adapter with fully controllable behaviour so the pass
// orchestration can be exercised without any real binary on PATH.
type fakeTool struct {
	name      string
	avail     bool
	formatter bool
	anns      []Annotation
	checked   []string
	err       error
	block     time.Duration // simulate a slow tool for timeout tests
}

func (t fakeTool) Name() string    { return t.name }
func (t fakeTool) Available() bool { return t.avail }
func (t fakeTool) Formatter() bool { return t.formatter }
func (t fakeTool) Run(ctx context.Context, worktree string, changed []string) ([]Annotation, []string, error) {
	if t.block > 0 {
		select {
		case <-time.After(t.block):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	return t.anns, t.checked, t.err
}

func runWith(tools []Tool, changed []string) Result {
	return Run(context.Background(), "/tmp/worktree", changed, Options{tools: tools})
}

// TestRunFailOpenNoWorktreeOrFiles: an empty worktree or no changed files
// yields an empty result and never touches a tool.
func TestRunFailOpenNoWorktreeOrFiles(t *testing.T) {
	tool := fakeTool{name: "x", avail: true, checked: []string{"a.go"}}
	if r := Run(context.Background(), "", []string{"a.go"}, Options{tools: []Tool{tool}}); len(r.Tools) != 0 {
		t.Fatalf("expected empty result for empty worktree, got %+v", r)
	}
	if r := Run(context.Background(), "/wt", nil, Options{tools: []Tool{tool}}); len(r.Tools) != 0 {
		t.Fatalf("expected empty result for no changed files, got %+v", r)
	}
}

// TestRunUnavailableToolIsSkippedCleanly: an unavailable tool is recorded as
// unavailable (not run) and contributes nothing — the fail-open path.
func TestRunUnavailableToolIsSkippedCleanly(t *testing.T) {
	r := runWith([]Tool{fakeTool{name: "golangci-lint", avail: false}}, []string{"a.go"})
	if len(r.Tools) != 1 {
		t.Fatalf("want 1 report, got %d", len(r.Tools))
	}
	rep := r.Tools[0]
	if rep.Available || rep.Ran {
		t.Fatalf("unavailable tool must not be marked available/ran: %+v", rep)
	}
	if got := r.unavailable(); len(got) != 1 || got[0] != "golangci-lint" {
		t.Fatalf("unavailable() = %v", got)
	}
	if r.HasAnnotations() {
		t.Fatalf("unavailable tool must contribute no annotations")
	}
}

// TestRunToolErrorDoesNotBreakPass: a tool that returns an error still yields
// its (partial) annotations and never propagates — the review continues.
func TestRunToolErrorDoesNotBreakPass(t *testing.T) {
	r := runWith([]Tool{fakeTool{
		name:  "go vet",
		avail: true,
		anns:  []Annotation{{Tool: "go vet", Path: "a.go", Line: 3, Level: LevelError, Message: "printf: bad arg"}},
		err:   errors.New("exit status 1"),
	}}, []string{"a.go"})
	if !r.HasAnnotations() {
		t.Fatalf("expected annotations preserved despite tool error")
	}
	if r.Tools[0].Err == nil {
		t.Fatalf("expected tool error recorded for telemetry")
	}
}

// TestRunTimeoutDiscardsOutput: a tool that exceeds the per-tool timeout is
// abandoned and its output discarded (fail-open), and the pass keeps going.
func TestRunTimeoutDiscardsOutput(t *testing.T) {
	slow := fakeTool{name: "slow", avail: true, block: 200 * time.Millisecond,
		anns: []Annotation{{Tool: "slow", Path: "a.go"}}}
	fast := fakeTool{name: "fast", avail: true,
		anns: []Annotation{{Tool: "fast", Path: "b.go", Line: 1, Level: LevelWarning, Message: "m"}}}
	r := Run(context.Background(), "/wt", []string{"a.go"}, Options{
		PerToolTimeout: 20 * time.Millisecond,
		tools:          []Tool{slow, fast},
	})
	// slow timed out → no annotations; fast still ran.
	var slowRep, fastRep ToolReport
	for _, tr := range r.Tools {
		switch tr.Tool {
		case "slow":
			slowRep = tr
		case "fast":
			fastRep = tr
		}
	}
	if !slowRep.TimedOut || len(slowRep.Annotations) != 0 {
		t.Fatalf("slow tool should have timed out with no annotations: %+v", slowRep)
	}
	if len(fastRep.Annotations) != 1 {
		t.Fatalf("fast tool should still contribute: %+v", fastRep)
	}
}

// TestFormatterCleanFiles: a formatter that checked files A and B and flagged
// only A leaves B clean; a non-formatter's silence contributes nothing.
func TestFormatterCleanFiles(t *testing.T) {
	r := runWith([]Tool{
		fakeTool{
			name: "gofmt", avail: true, formatter: true,
			checked: []string{"a.go", "b.go"},
			anns:    []Annotation{{Tool: "gofmt", Path: "a.go", Level: LevelWarning, Message: "unformatted"}},
		},
		fakeTool{ // non-formatter: its clean pass must NOT mark files clean
			name: "go vet", avail: true, formatter: false,
			checked: []string{"c.go"},
		},
	}, []string{"a.go", "b.go", "c.go"})
	clean := r.FormatterCleanFiles()
	if clean["a.go"] {
		t.Fatalf("a.go was flagged by gofmt; must not be clean")
	}
	if !clean["b.go"] {
		t.Fatalf("b.go passed gofmt clean; expected clean")
	}
	if clean["c.go"] {
		t.Fatalf("c.go only seen by a non-formatter; must not count as clean")
	}
}

// TestFormatSpecialistSectionWording: the injected section must carry both the
// "don't re-report what the tool flags" instruction and the "linter clean =
// don't hand-flag formatting" false-positive signal, plus the actual flags.
func TestFormatSpecialistSectionWording(t *testing.T) {
	r := runWith([]Tool{fakeTool{
		name: "gofmt", avail: true, formatter: true,
		checked: []string{"x.go"},
		anns:    []Annotation{{Tool: "gofmt", Path: "x.go", Level: LevelWarning, Message: "file is not gofmt-formatted"}},
	}}, []string{"x.go"})
	sec := FormatSpecialistSection(r)
	for _, want := range []string{"Do not re-report", "gofmt", "false positive", "linter-clean"} {
		if !strings.Contains(sec, want) {
			t.Fatalf("section missing %q:\n%s", want, sec)
		}
	}
	if strings.TrimSpace(FormatSpecialistSection(Result{})) != "" {
		t.Fatalf("empty result should render no section")
	}
	if WrapSpecialistSection("") != "" {
		t.Fatalf("empty body should wrap to empty string")
	}
	if !strings.Contains(WrapSpecialistSection("body"), SpecialistSectionHeading) {
		t.Fatalf("wrapped section should carry heading")
	}
}

// TestFormatChecksAnnotations: annotations render for the checks agent; no
// annotations → empty string.
func TestFormatChecksAnnotations(t *testing.T) {
	if FormatChecksAnnotations(Result{}) != "" {
		t.Fatalf("no annotations should render empty checks section")
	}
	r := runWith([]Tool{fakeTool{
		name: "go vet", avail: true,
		anns: []Annotation{{Tool: "go vet", Path: "a.go", Line: 9, Level: LevelError, Message: "printf: bad"}},
	}}, []string{"a.go"})
	got := FormatChecksAnnotations(r)
	if !strings.Contains(got, "go vet") || !strings.Contains(got, "a.go:9") {
		t.Fatalf("checks annotations render missing detail:\n%s", got)
	}
}

// TestGofmtIntegration exercises the REAL gofmt adapter (gofmt ships with Go,
// so this is hermetic in any Go environment): a badly-formatted file is flagged
// and a well-formatted file is left clean.
func TestGofmtIntegration(t *testing.T) {
	tool := gofmtTool{}
	if !tool.Available() {
		t.Skip("gofmt not on PATH (unexpected in a Go toolchain)")
	}
	dir := t.TempDir()
	// Deliberately mis-indented / gofmt-dirty file.
	bad := "package x\nfunc F()  int {\nreturn 1\n}\n"
	good := "package x\n\nfunc G() int { return 2 }\n"
	writeFile(t, filepath.Join(dir, "bad.go"), bad)
	writeFile(t, filepath.Join(dir, "good.go"), good)

	anns, checked, err := tool.Run(context.Background(), dir, []string{"bad.go", "good.go"})
	if err != nil {
		// gofmt -l exits 0 even when it lists files; a non-nil err would be a
		// real problem, but keep fail-open semantics in the assertion.
		t.Logf("gofmt returned err (tolerated): %v", err)
	}
	if len(checked) != 2 {
		t.Fatalf("expected both go files checked, got %v", checked)
	}
	foundBad := false
	for _, a := range anns {
		if a.Path == "bad.go" {
			foundBad = true
		}
		if a.Path == "good.go" {
			t.Fatalf("good.go is gofmt-clean; should not be flagged")
		}
	}
	if !foundBad {
		t.Fatalf("bad.go should be flagged by gofmt -l; anns=%+v", anns)
	}

	// End-to-end through Run: the clean file must be reported clean, the dirty
	// one must appear in the injected specialist section citing gofmt.
	r := Run(context.Background(), dir, []string{"bad.go", "good.go"}, Options{tools: []Tool{tool}})
	clean := r.FormatterCleanFiles()
	if !clean["good.go"] || clean["bad.go"] {
		t.Fatalf("clean-file signal wrong: %+v", clean)
	}
	sec := FormatSpecialistSection(r)
	if !strings.Contains(sec, "gofmt") || !strings.Contains(sec, "bad.go") {
		t.Fatalf("specialist section should cite gofmt+bad.go:\n%s", sec)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
