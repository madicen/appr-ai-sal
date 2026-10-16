package evals

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/review"
)

func TestExtractClaimRefs(t *testing.T) {
	refs := extractClaimRefs("Uses `DoesNotExist` in pkg/foo.go and blob/checksum.go:9.")
	got := map[string]bool{}
	for _, r := range refs {
		got[r.Kind+":"+r.Text] = true
	}
	for _, want := range []string{
		"ident:DoesNotExist",
		"path:pkg/foo.go",
		"pathline:blob/checksum.go:9",
	} {
		if !got[want] {
			t.Errorf("missing ref %q in %#v", want, refs)
		}
	}
}

func TestResolveClaimsInventedVsPresent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pkg", "real.go"), "package pkg\n\nfunc RealHelper() {}\n")
	mustWrite(t, filepath.Join(dir, "pkg", "foo.go"), "package pkg\n")

	findings := []review.Finding{
		{Comment: "Call `InventedFn` here."},
		{Comment: "Prefer `RealHelper` from pkg/real.go."},
		{Comment: "See pkg/foo.go:99 for context."}, // OOB line
		{Comment: "No refs at all."},
	}
	unsupported, checked := resolveClaims(dir, findings)
	if checked != 3 {
		t.Fatalf("checked=%d, want 3 (findings with extractable refs)", checked)
	}
	if unsupported != 2 {
		t.Fatalf("unsupported=%d, want 2 (InventedFn + OOB line)", unsupported)
	}
}

func TestResolveClaimsNoWorktree(t *testing.T) {
	unsupported, checked := resolveClaims("", []review.Finding{
		{Comment: "Uses `Foo`."},
	})
	if unsupported != 0 || checked != 0 {
		t.Fatalf("no worktree should yield 0/0, got %d/%d", unsupported, checked)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
