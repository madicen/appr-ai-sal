package review

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
)

// defaultBudgetCfg returns a BudgetConfig using the baked-in globs and the
// default caps — the same shape newBudgetConfig produces for a nil repo config
// on an unknown provider.
func defaultBudgetCfg() BudgetConfig {
	return BudgetConfig{
		ElisionGlobs:   repoconfig.DefaultDiffElisionGlobs(),
		ByteCap:        defaultDiffByteCap,
		PerFileLineCap: defaultDiffPerFileLineCap,
	}
}

// TestBudgetDiffSmallDiffByteIdentical is the core no-regression guarantee: a
// small diff with nothing to elide and comfortably under the byte cap must pass
// through UNCHANGED, so ordinary PRs produce a byte-identical prompt to before
// R3 existed.
func TestBudgetDiffSmallDiffByteIdentical(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
 
-func main() {}
+func main() { println("hi") }
+// trailing
diff --git a/util.go b/util.go
--- a/util.go
+++ b/util.go
@@ -10,3 +10,4 @@ func helper() {
 	x := 1
 	_ = x
+	_ = 2
 }
`
	shaped, report := budgetDiff(diff, defaultBudgetCfg())
	if report.Truncated {
		t.Fatalf("small diff should not be truncated, report=%+v", report)
	}
	if shaped != diff {
		t.Fatalf("small diff must pass through byte-identical.\n--- got ---\n%q\n--- want ---\n%q", shaped, diff)
	}
}

// TestBudgetDiffElidesLockfiles verifies a default-glob file (go.sum) is
// dropped to a manifest entry while a normal code file survives, and that the
// report + manifest carry the elision.
func TestBudgetDiffElidesLockfiles(t *testing.T) {
	diff := `diff --git a/go.sum b/go.sum
--- a/go.sum
+++ b/go.sum
@@ -1,3 +1,4 @@
 github.com/foo/bar v1.0.0 h1:abc=
 github.com/foo/bar v1.0.0/go.mod h1:def=
+github.com/baz/qux v2.0.0 h1:ghi=
diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main
+var X = 1
`
	shaped, report := budgetDiff(diff, defaultBudgetCfg())
	if !report.Truncated {
		t.Fatalf("expected truncation when a lockfile is present")
	}
	if len(report.Elided) != 1 || report.Elided[0].Path != "go.sum" {
		t.Fatalf("expected go.sum elided, got %+v", report.Elided)
	}
	if report.Elided[0].Reason != "lockfile" {
		t.Errorf("go.sum reason = %q, want lockfile", report.Elided[0].Reason)
	}
	if !strings.Contains(shaped, "go.sum (elided: lockfile") {
		t.Errorf("shaped diff missing go.sum manifest entry:\n%s", shaped)
	}
	// main.go must still be a real, parseable stanza; go.sum must not.
	files := ParseDiff(shaped)
	if FindFile(files, "main.go") == nil {
		t.Errorf("main.go should survive in shaped diff, files=%v", files)
	}
	if FindFile(files, "go.sum") != nil {
		t.Errorf("go.sum should NOT appear as a parseable stanza after elision")
	}
}

// TestBudgetDiffElidesVendorAndGenerated covers the directory-prefix glob
// ("vendor/") and the basename-substring glob ("*_generated*"), the two
// matcher rules that path.Match alone cannot express.
func TestBudgetDiffElidesVendorAndGenerated(t *testing.T) {
	diff := `diff --git a/vendor/x/y.go b/vendor/x/y.go
--- a/vendor/x/y.go
+++ b/vendor/x/y.go
@@ -1,1 +1,2 @@
 package y
+var Z = 1
diff --git a/pkg/api_generated.go b/pkg/api_generated.go
--- a/pkg/api_generated.go
+++ b/pkg/api_generated.go
@@ -1,1 +1,2 @@
 package pkg
+var G = 1
diff --git a/pkg/real.go b/pkg/real.go
--- a/pkg/real.go
+++ b/pkg/real.go
@@ -1,1 +1,2 @@
 package pkg
+var R = 1
`
	_, report := budgetDiff(diff, defaultBudgetCfg())
	if !report.Truncated {
		t.Fatalf("expected truncation")
	}
	got := map[string]string{}
	for _, e := range report.Elided {
		got[e.Path] = e.Reason
	}
	if got["vendor/x/y.go"] != "vendored" {
		t.Errorf("vendor file reason = %q, want vendored (elided=%+v)", got["vendor/x/y.go"], report.Elided)
	}
	if got["pkg/api_generated.go"] != "generated" {
		t.Errorf("generated file reason = %q, want generated", got["pkg/api_generated.go"])
	}
	if _, ok := got["pkg/real.go"]; ok {
		t.Errorf("pkg/real.go should NOT be elided")
	}
}

// TestBudgetDiffElidesBinary covers the "binary files differ" stanza path.
func TestBudgetDiffElidesBinary(t *testing.T) {
	diff := `diff --git a/logo.png b/logo.png
new file mode 100644
Binary files /dev/null and b/logo.png differ
diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,1 +1,2 @@
 package a
+var Y = 1
`
	_, report := budgetDiff(diff, defaultBudgetCfg())
	if len(report.Elided) != 1 || report.Elided[0].Path != "logo.png" || report.Elided[0].Reason != "binary" {
		t.Fatalf("expected logo.png elided as binary, got %+v", report.Elided)
	}
}

// TestBudgetDiffPreservesLineNumbers is the correctness guarantee that keeps
// inline findings postable: when a lockfile is elided ahead of a code file,
// the surviving code file's post-image line numbers are unchanged, so a finding
// the model anchors to a shaped-diff line still points at the right line in the
// full diff / on GitHub.
func TestBudgetDiffPreservesLineNumbers(t *testing.T) {
	diff := `diff --git a/go.sum b/go.sum
--- a/go.sum
+++ b/go.sum
@@ -1,2 +1,3 @@
 aaa
 bbb
+ccc
diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -100,3 +100,4 @@ func F() {
 	before := 1
 	_ = before
+	added := 2
 }
`
	shaped, report := budgetDiff(diff, defaultBudgetCfg())
	if !report.Truncated {
		t.Fatalf("expected go.sum elision")
	}
	raw := FindFile(ParseDiff(diff), "a.go")
	got := FindFile(ParseDiff(shaped), "a.go")
	if raw == nil || got == nil {
		t.Fatalf("a.go missing (raw=%v shaped=%v)", raw, got)
	}
	rawLine := addedLineNo(t, raw, "added := 2")
	gotLine := addedLineNo(t, got, "added := 2")
	if rawLine != gotLine {
		t.Fatalf("line number drifted after shaping: raw=%d shaped=%d", rawLine, gotLine)
	}
	if gotLine != 102 {
		t.Errorf("expected the added line at post-image 102, got %d", gotLine)
	}
}

func addedLineNo(t *testing.T, fd *FileDiff, text string) int {
	t.Helper()
	for _, h := range fd.Hunks {
		for _, l := range h.Lines {
			if l.Kind == DiffAdded && strings.TrimSpace(l.Text) == text {
				return l.NewNo
			}
		}
	}
	t.Fatalf("added line %q not found in %s", text, fd.Path)
	return 0
}

// TestBudgetDiffPerFileLineCap trims the tail of a single oversized file while
// keeping the leading lines and their real line numbers.
func TestBudgetDiffPerFileLineCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/big.txt b/big.txt\n--- a/big.txt\n+++ b/big.txt\n@@ -0,0 +1,50 @@\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "+line %d\n", i)
	}
	diff := b.String()
	cfg := defaultBudgetCfg()
	cfg.PerFileLineCap = 10 // header lines + a few body lines, then a marker
	shaped, report := budgetDiff(diff, cfg)
	if !report.Truncated || len(report.Truncations) != 1 {
		t.Fatalf("expected 1 truncation, got %+v", report)
	}
	if report.Truncations[0].Path != "big.txt" {
		t.Errorf("truncated path = %q, want big.txt", report.Truncations[0].Path)
	}
	if !strings.Contains(shaped, "lines omitted") {
		t.Errorf("shaped diff missing omitted-lines marker:\n%s", shaped)
	}
	if got := strings.Count(shaped, "+line "); got >= 50 {
		t.Errorf("expected fewer than 50 body lines after cap, got %d", got)
	}
}

// TestBudgetDiff5MB is the acceptance-criteria fixture: a synthetic ~5 MB diff
// must be shaped to fit under the byte cap with truncation reported, so an HTTP
// provider run never 400s on an oversized prompt.
func TestBudgetDiff5MB(t *testing.T) {
	diff := makeBigDiff(60, 2000)
	if len(diff) < 5*1024*1024 {
		t.Fatalf("fixture too small: %d bytes, want >= 5 MiB", len(diff))
	}
	cfg := defaultBudgetCfg() // 256 KiB default byte cap
	shaped, report := budgetDiff(diff, cfg)
	if !report.Truncated {
		t.Fatalf("5 MB diff must be truncated")
	}
	if len(shaped) > cfg.ByteCap {
		t.Fatalf("shaped diff %d bytes exceeds byte cap %d", len(shaped), cfg.ByteCap)
	}
	if len(report.Elided) == 0 {
		t.Fatalf("expected trailing files elided by the byte cap, report=%+v", summarizeReport(report))
	}
	if report.OriginalBytes != len(diff) || report.ShapedBytes != len(shaped) {
		t.Errorf("report byte accounting off: original=%d shaped=%d (diff=%d shaped=%d)",
			report.OriginalBytes, report.ShapedBytes, len(diff), len(shaped))
	}
	// The manifest must be present and parseable-safe (kept stanzas still
	// parse; the manifest sits in the preamble ParseDiff skips).
	if !strings.Contains(shaped, "[appr-ai-sal]") {
		t.Errorf("shaped diff missing budget manifest header")
	}
	if len(ParseDiff(shaped)) == 0 {
		t.Errorf("shaped diff should still contain at least one parseable file stanza")
	}
}

func summarizeReport(r BudgetReport) string {
	return fmt.Sprintf("truncated=%v elided=%d truncations=%d orig=%d shaped=%d",
		r.Truncated, len(r.Elided), len(r.Truncations), r.OriginalBytes, r.ShapedBytes)
}

// makeBigDiff builds a synthetic unified diff of nFiles new files, each adding
// linesPerFile padded lines, so the total comfortably exceeds a few megabytes.
func makeBigDiff(nFiles, linesPerFile int) string {
	var b strings.Builder
	pad := strings.Repeat("x", 40)
	for f := 0; f < nFiles; f++ {
		fmt.Fprintf(&b, "diff --git a/file%03d.go b/file%03d.go\n", f, f)
		b.WriteString("new file mode 100644\n")
		fmt.Fprintf(&b, "--- /dev/null\n+++ b/file%03d.go\n", f)
		fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", linesPerFile)
		for i := 0; i < linesPerFile; i++ {
			fmt.Fprintf(&b, "+line %d %s\n", i, pad)
		}
	}
	return b.String()
}

// TestNewBudgetConfigProviderCaps verifies the per-provider byte-budget table
// and config overrides resolve as documented.
func TestNewBudgetConfigProviderCaps(t *testing.T) {
	gem := aiconfig.DefaultConfig()
	gem.Provider = aiconfig.ProviderGemini
	if bc := newBudgetConfig(nil, gem); bc.ByteCap != 786432 {
		t.Errorf("gemini byte cap = %d, want 786432", bc.ByteCap)
	}
	ol := aiconfig.DefaultConfig()
	ol.Provider = aiconfig.ProviderOllama
	if bc := newBudgetConfig(nil, ol); bc.ByteCap != defaultDiffByteCap {
		t.Errorf("ollama byte cap = %d, want %d", bc.ByteCap, defaultDiffByteCap)
	}
	// A repo-config override wins over the provider table.
	rc := repoconfig.Default()
	rc.DiffByteCap = 4096
	rc.DiffPerFileLineCap = 42
	if bc := newBudgetConfig(rc, gem); bc.ByteCap != 4096 || bc.PerFileLineCap != 42 {
		t.Errorf("override not applied: %+v", bc)
	}
	// nil repo config falls back to the baked-in globs.
	if bc := newBudgetConfig(nil, ol); len(bc.ElisionGlobs) == 0 {
		t.Errorf("nil repo config should still yield default elision globs")
	}
}

// TestBudgetReportDisclosureLine locks the human-facing disclosure wording used
// in both the Progress warning and the review body.
func TestBudgetReportDisclosureLine(t *testing.T) {
	r := BudgetReport{
		Truncated:   true,
		Elided:      []ElidedFile{{Path: "go.sum"}, {Path: "package-lock.json"}},
		Truncations: []TruncatedFile{{Path: "huge.go", OmittedLines: 40}},
	}
	got := r.DisclosureLine()
	want := "review ran on a truncated diff: files go.sum, package-lock.json elided; file huge.go truncated"
	if got != want {
		t.Fatalf("DisclosureLine() = %q, want %q", got, want)
	}
	if (BudgetReport{}).DisclosureLine() != "" {
		t.Errorf("empty report should disclose nothing")
	}
}

// TestRenderBodyDisclosesTruncation verifies the review body carries the R3
// disclosure callout when the diff was shaped (both the normal body path and
// the no-findings body path).
func TestRenderBodyDisclosesTruncation(t *testing.T) {
	budget := &BudgetReport{
		Truncated: true,
		Elided:    []ElidedFile{{Path: "go.sum", Reason: "lockfile"}},
	}
	// Normal body path (a PR-wide finding keeps HasNoFindings() false).
	d := &Draft{
		Specialists: []SpecialistResult{{
			Specialist: SpecSecurity,
			Findings:   []Finding{{Severity: SeverityWarning, Comment: "watch this"}},
		}},
		DiffBudget: budget,
	}
	body := d.RenderBody()
	if !strings.Contains(body, "review ran on a truncated diff") {
		t.Errorf("body missing truncation disclosure:\n%s", body)
	}
	if !strings.Contains(body, "[!WARNING]") {
		t.Errorf("body missing warning callout:\n%s", body)
	}

	// No-findings body path must disclose too.
	clean := &Draft{DiffBudget: budget}
	if !clean.HasNoFindings() {
		t.Fatal("clean draft should report HasNoFindings")
	}
	if !strings.Contains(clean.RenderBody(), "review ran on a truncated diff") {
		t.Errorf("no-findings body missing truncation disclosure:\n%s", clean.RenderBody())
	}

	// A draft with no budget (full diff reviewed) discloses nothing.
	if strings.Contains((&Draft{DiffBudget: nil}).RenderBody(), "truncated diff") {
		t.Errorf("draft without budget should not mention truncation")
	}
}

// TestRunSpecialistOnBudgetedDiffNoOversizePayload is the end-to-end acceptance
// check: a ~5 MB diff, shaped by the budgeter, is fed through runReviewSpecialist
// against a fake HTTP (openai_compatible) provider. The provider records the
// request body size; the test asserts the call SUCCEEDS (no 400) and the body
// the runner would have sent stayed well under a size that could blow a context
// window — proving the budgeter shaped the diff before it was inlined.
func TestRunSpecialistOnBudgetedDiffNoOversizePayload(t *testing.T) {
	var mu sync.Mutex
	maxBody := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 0, 1<<20)
		tmp := make([]byte, 32*1024)
		for {
			n, err := r.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		mu.Lock()
		if len(buf) > maxBody {
			maxBody = len(buf)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// message content is itself a JSON string the specialist parser reads.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"no security concerns\",\"findings\":[]}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer srv.Close()

	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderOpenAICompatible
	cfg.BaseURL = srv.URL
	cfg.Model = "qwen"

	rawDiff := makeBigDiff(60, 2000)
	shaped, report := budgetDiff(rawDiff, newBudgetConfig(repoconfig.Default(), cfg))
	if !report.Truncated {
		t.Fatalf("fixture should have been shaped")
	}

	pr := &gh.PR{Repository: "o/r", Number: 1, Title: "big", Author: "a", BaseRef: "main", HeadRef: "feat"}
	res := runReviewSpecialist(context.Background(), cfg, SpecSecurity, "", pr, shaped, "", "", "", "")
	if res.Err != nil {
		t.Fatalf("specialist run errored on shaped diff: %v", res.Err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %d", len(res.Findings))
	}

	mu.Lock()
	body := maxBody
	mu.Unlock()
	if body == 0 {
		t.Fatalf("provider never received a request")
	}
	// The whole request (system + user prompt including the shaped diff) must
	// stay well under a megabyte — the raw 5 MB diff would have produced a body
	// an order of magnitude larger.
	if body > 700*1024 {
		t.Fatalf("request body %d bytes too large — budgeter did not shape the diff", body)
	}
	if body >= len(rawDiff) {
		t.Fatalf("request body %d not smaller than raw diff %d", body, len(rawDiff))
	}
}
