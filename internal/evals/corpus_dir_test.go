package evals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCorpusDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sample-case")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("case.json", `{"id":"sample-case","title":"Sample","target":"security","repo":"acme/x","number":1}`)
	write("diff.patch", "diff --git a/a.go b/a.go\n")
	write("expectations.json", `{"must_appear":[],"must_not_appear":[]}`)
	// Non-case sibling should be ignored.
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("docs"), 0o644)

	cases, err := LoadCorpusDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Meta.ID != "sample-case" {
		t.Fatalf("got %#v", cases)
	}
}
