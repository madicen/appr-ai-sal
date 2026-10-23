package evals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWorktreeInventoryListsFilesAndIdents(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pkg.go"), []byte("package p\n\nfunc Checksum() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "x.py"), []byte("def helper():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := buildWorktreeInventory(dir)
	if !strings.Contains(got, "treatment: inventory-v1") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "`pkg.go`") || !strings.Contains(got, "`sub/x.py`") {
		t.Fatalf("missing files: %q", got)
	}
	if !strings.Contains(got, "`Checksum`") || !strings.Contains(got, "`helper`") {
		t.Fatalf("missing idents: %q", got)
	}
}

func TestApplyEvalTreatmentInventory(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	t.Setenv(evalTreatmentEnv, "inventory-v1")
	got := applyEvalTreatment(dir, "existing brief")
	if !strings.Contains(got, "existing brief") || !strings.Contains(got, "inventory-v1") {
		t.Fatalf("got %q", got)
	}
	t.Setenv(evalTreatmentEnv, "")
	got = applyEvalTreatment(dir, "existing brief")
	if got != "existing brief" {
		t.Fatalf("control should be unchanged, got %q", got)
	}
}
