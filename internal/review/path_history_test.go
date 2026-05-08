package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
)

func TestAggregatePathHistory(t *testing.T) {
	rows := []gh.PRFileTouches{
		{Number: 1, SourceFiles: 3, TestFiles: 2},
		{Number: 2, SourceFiles: 1, DocFiles: 1},
		{Number: 3, SourceFiles: 5, TestFiles: 1, DocFiles: 1},
		{Number: 4, SourceFiles: 2},
	}
	a := AggregatePathHistory(rows)
	if a.MatchedPRs != 4 {
		t.Fatalf("matched = %d, want 4", a.MatchedPRs)
	}
	if a.WithTests != 2 {
		t.Fatalf("with tests = %d, want 2", a.WithTests)
	}
	if a.WithDocs != 2 {
		t.Fatalf("with docs = %d, want 2", a.WithDocs)
	}
	if a.WithSourceOnly != 1 {
		t.Fatalf("source-only = %d, want 1", a.WithSourceOnly)
	}
	if len(a.SamplePRNumbers) != 4 {
		t.Fatalf("samples = %d, want 4", len(a.SamplePRNumbers))
	}
}

func TestFormatPathHistoryAggregateEmpty(t *testing.T) {
	if got := FormatPathHistoryAggregate(PathHistoryAggregate{}); got != "" {
		t.Fatalf("expected empty: %q", got)
	}
}

func TestFormatPathHistoryAggregateRenders(t *testing.T) {
	a := PathHistoryAggregate{
		MatchedPRs:      6,
		WithTests:       4,
		WithDocs:        2,
		WithSourceOnly:  1,
		SamplePRNumbers: []int{1, 2, 3},
		SampleTestPRs:   []int{1, 2},
		SampleDocsPRs:   []int{3},
	}
	out := FormatPathHistoryAggregate(a)
	if !strings.Contains(out, "Matching merged PRs sampled: **6**") {
		t.Fatalf("missing matched count: %q", out)
	}
	if !strings.Contains(out, "#1, #2") {
		t.Fatalf("missing test sample list: %q", out)
	}
	if !strings.Contains(out, "#3") {
		t.Fatalf("missing docs sample list: %q", out)
	}
}

func TestPathSetKeyOrderInvariant(t *testing.T) {
	k1 := pathSetKey([]string{"a/b.go", "c/d.go"})
	k2 := pathSetKey([]string{"c/d.go", "a/b.go"})
	if k1 != k2 {
		t.Fatalf("path set key should be order-invariant: %s vs %s", k1, k2)
	}
}

func TestBuildPRReviewEvidenceSkippedWhenDisabled(t *testing.T) {
	rc := repoconfig.Default()
	rc.IncludeRepoEvidence = false
	got := BuildPRReviewEvidence(context.Background(), rc, &gh.PR{Owner: "x", Repo: "y"}, "diff --git a/a.go b/a.go\n", "/tmp")
	if got != "" {
		t.Fatalf("expected empty when disabled, got %q", got)
	}
}

func TestBuildPRReviewEvidenceStaticSection(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `package pkg

func ExportedNoDoc() {}
`
	if err := os.WriteFile(filepath.Join(dir, "pkg", "f.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := `diff --git a/pkg/f.go b/pkg/f.go
--- a/pkg/f.go
+++ b/pkg/f.go
@@ -1,3 +1,4 @@
 package pkg

+func ExportedNoDoc() {}
`
	rc := repoconfig.Default()
	got := BuildPRReviewEvidence(context.Background(), rc, &gh.PR{Owner: "x", Repo: "y"}, diff, dir)
	if !strings.Contains(got, "Changed source files") {
		t.Fatalf("expected static section: %q", got)
	}
	wrapped := FormatPRReviewEvidenceSection(got)
	if !strings.Contains(wrapped, "## Repo evidence for this PR") {
		t.Fatalf("expected wrapping section header: %q", wrapped)
	}
}

func TestChangedPathsFromDiffDeduplicates(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-x
+y
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1 +1 @@
-x
+y
`
	got := changedPathsFromDiff(diff)
	if len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Fatalf("got %v", got)
	}
}
