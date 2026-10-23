package evals

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadHeldOutSeedCases(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "docs", "evals", "held-out", "cases")
	if _, err := os.Stat(root); err != nil {
		t.Skip("held-out cases not present")
	}
	cases, err := LoadCorpusDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 1 {
		t.Fatal("expected ≥1 held-out case")
	}
}
