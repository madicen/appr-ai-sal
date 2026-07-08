package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/staticpass"
)

// TestDowngradeFormatterSilencedFindings covers the Q5.d "linter is silent"
// false-positive filter: a mechanical formatting nit on a formatter-clean file
// is demoted (and its suggestion stripped); everything else is untouched.
func TestDowngradeFormatterSilencedFindings(t *testing.T) {
	clean := map[string]bool{"pkg/clean.go": true}

	t.Run("demotes formatting nit on clean file", func(t *testing.T) {
		in := []Finding{{
			Path:       "pkg/clean.go",
			Line:       10,
			Severity:   SeverityWarning,
			Comment:    "Inconsistent indentation here; run gofmt to fix the whitespace.",
			Suggestion: "\tx := 1",
		}}
		out := downgradeFormatterSilencedFindings(SpecFormatting, in, clean)
		if out[0].Severity != SeverityInfo {
			t.Fatalf("expected demotion to info, got %q", out[0].Severity)
		}
		if out[0].Suggestion != "" || out[0].SuggestionStrippedReason == "" {
			t.Fatalf("expected suggestion stripped with reason, got sugg=%q reason=%q", out[0].Suggestion, out[0].SuggestionStrippedReason)
		}
		if out[0].ActionabilityNote == "" {
			t.Fatalf("expected actionability note explaining the downgrade")
		}
	})

	t.Run("leaves non-formatting finding on clean file", func(t *testing.T) {
		in := []Finding{{
			Path:     "pkg/clean.go",
			Line:     10,
			Severity: SeverityWarning,
			Comment:  "This magic literal 42 should be a named constant.",
		}}
		out := downgradeFormatterSilencedFindings(SpecFormatting, in, clean)
		if out[0].Severity != SeverityWarning {
			t.Fatalf("non-formatting finding must be untouched, got %q", out[0].Severity)
		}
	})

	t.Run("leaves finding on a non-clean file", func(t *testing.T) {
		in := []Finding{{
			Path:     "pkg/dirty.go", // not in clean set
			Line:     3,
			Severity: SeverityWarning,
			Comment:  "trailing whitespace here",
		}}
		out := downgradeFormatterSilencedFindings(SpecFormatting, in, clean)
		if out[0].Severity != SeverityWarning {
			t.Fatalf("finding on non-clean file must be untouched, got %q", out[0].Severity)
		}
	})

	t.Run("only silence-aware specs are affected", func(t *testing.T) {
		in := []Finding{{
			Path:     "pkg/clean.go",
			Line:     3,
			Severity: SeverityError,
			Comment:  "indentation is inconsistent",
		}}
		// security is not FormatterSilenceAware: never touched.
		out := downgradeFormatterSilencedFindings(SpecSecurity, in, clean)
		if out[0].Severity != SeverityError {
			t.Fatalf("security finding must not be silenced, got %q", out[0].Severity)
		}
	})

	t.Run("no clean files is a no-op", func(t *testing.T) {
		in := []Finding{{Path: "a.go", Line: 1, Severity: SeverityWarning, Comment: "indentation"}}
		out := downgradeFormatterSilencedFindings(SpecFormatting, in, nil)
		if out[0].Severity != SeverityWarning {
			t.Fatalf("no clean files should be a no-op")
		}
	})
}

// TestBuildReviewUserPromptInjectsStaticSection proves the static-analysis
// pre-pass block reaches a specialist's prompt (the Q5.a injection).
func TestBuildReviewUserPromptInjectsStaticSection(t *testing.T) {
	pr := &gh.PR{Number: 1, Title: "x", Repository: "o/r", BaseRef: "main", HeadRef: "feat"}
	static := staticpass.WrapSpecialistSection("gofmt flags `x.go`; do not re-report.")
	got := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced, "", "", static, "", "")
	if !strings.Contains(got, staticpass.SpecialistSectionHeading) {
		t.Fatalf("prompt missing static-analysis heading:\n%s", got)
	}
	if !strings.Contains(got, "do not re-report") {
		t.Fatalf("prompt missing static-analysis body:\n%s", got)
	}
	// It must sit before the diff (ground truth precedes the code).
	if idxStatic, idxDiff := strings.Index(got, staticpass.SpecialistSectionHeading), strings.Index(got, "Unified diff"); idxStatic < 0 || idxStatic > idxDiff {
		t.Fatalf("static section should precede the diff: static=%d diff=%d", idxStatic, idxDiff)
	}
}

// TestStaticPassGofmtGroundsFormatting is the Q5 acceptance test, run
// hermetically against REAL gofmt (which ships with the Go toolchain): a Go
// fixture with a gofmt violation makes the injected specialist section cite
// gofmt (so formatting is told not to hand-flag it), and a formatting nit on
// the gofmt-CLEAN sibling file is downgraded by the "linter is silent" gate.
func TestStaticPassGofmtGroundsFormatting(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
	dir := t.TempDir()
	writeGoFile(t, dir, "dirty.go", "package p\nfunc F()  int {\nreturn 1\n}\n")
	writeGoFile(t, dir, "clean.go", "package p\n\nfunc G() int { return 2 }\n")

	sp := staticpass.Run(context.Background(), dir, []string{"dirty.go", "clean.go"}, staticpass.Options{})

	// The injected specialist section cites gofmt + the dirty file.
	section := staticpass.WrapSpecialistSection(staticpass.FormatSpecialistSection(sp))
	if !strings.Contains(section, "gofmt") || !strings.Contains(section, "dirty.go") {
		t.Fatalf("specialist section should cite gofmt+dirty.go:\n%s", section)
	}
	if !strings.Contains(section, "Do not re-report") {
		t.Fatalf("section should instruct not to re-report tool findings:\n%s", section)
	}

	// The clean sibling is in the formatter-clean set; a hand-rolled whitespace
	// nit there is downgraded. The dirty file is NOT clean, so a nit there is
	// preserved for the reviewer.
	clean := sp.FormatterCleanFiles()
	if !clean["clean.go"] || clean["dirty.go"] {
		t.Fatalf("clean-file signal wrong: %+v", clean)
	}
	findings := []Finding{
		{Path: "clean.go", Line: 3, Severity: SeverityWarning, Comment: "fix the indentation / whitespace here"},
		{Path: "dirty.go", Line: 2, Severity: SeverityWarning, Comment: "fix the indentation / whitespace here"},
	}
	out := downgradeFormatterSilencedFindings(SpecFormatting, findings, clean)
	if out[0].Severity != SeverityInfo {
		t.Fatalf("formatting nit on gofmt-clean file should be demoted, got %q", out[0].Severity)
	}
	if out[1].Severity != SeverityWarning {
		t.Fatalf("formatting nit on gofmt-DIRTY file should be preserved, got %q", out[1].Severity)
	}
}

func writeGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
