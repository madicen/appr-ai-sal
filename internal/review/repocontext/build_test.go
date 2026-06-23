package repocontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeniedPath(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"AGENTS.md", false},
		{".git/config", true},
		{"vendor/foo", true},
		{"secrets/id_rsa", true},
		{"x/foo.pem", true},
		{".env", true},
		{".env.local", true},
		{"src/main.go", false},
	}
	for _, tc := range cases {
		if got := deniedPath(tc.rel); got != tc.want {
			t.Errorf("deniedPath(%q) = %v want %v", tc.rel, got, tc.want)
		}
	}
}

func TestBuildCapsAndReadsConventions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(strings.Repeat("x\n", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := Build(ctx, Options{Worktree: dir, MaxBytes: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AGENTS.md") {
		t.Fatalf("expected AGENTS section: %q", out)
	}
	if len(out) > 2500 {
		t.Fatalf("output exceeded relaxed cap: %d", len(out))
	}
}

func TestBuildLocalRootFallback(t *testing.T) {
	wt := t.TempDir()
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "CONTRIBUTING.md"), []byte("from local only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := Build(ctx, Options{Worktree: wt, LocalRoot: local, MaxBytes: 8000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "from local only") || !strings.Contains(out, "local clone") {
		t.Fatalf("expected local clone CONTRIBUTING: %q", out)
	}
}

func TestBuildHarvestsManifestsWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\nrequire github.com/segmentio/kafka-go v0.4.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("provider \"aws\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, "ci.yml"), []byte("name: CI\non: [push]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Without IncludeManifests, manifests are not harvested.
	off, err := Build(ctx, Options{Worktree: dir, MaxBytes: 16000})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off, "kafka-go") {
		t.Fatalf("manifests should not appear when IncludeManifests is off: %q", off)
	}

	on, err := Build(ctx, Options{Worktree: dir, MaxBytes: 16000, IncludeManifests: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(on, "kafka-go") {
		t.Fatalf("expected go.mod content with IncludeManifests: %q", on)
	}
	if !strings.Contains(on, "provider \"aws\"") {
		t.Fatalf("expected root *.tf content with IncludeManifests: %q", on)
	}
	if !strings.Contains(on, "ci.yml") {
		t.Fatalf("expected CI workflow harvested with IncludeManifests: %q", on)
	}
}

func TestTreeSummary(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "cmd"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := Build(ctx, Options{Worktree: dir, MaxBytes: 8000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cmd/") || !strings.Contains(out, ".go") {
		t.Fatalf("expected tree summary: %q", out)
	}
}
