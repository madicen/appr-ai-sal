package agentstore

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

type doc struct {
	Owner   string            `json:"owner"`
	Repo    string            `json:"repo"`
	Entries map[string]string `json:"entries"`
}

func newStore() Store[doc] {
	return Store[doc]{
		Subdir:   "test-profiles",
		FileName: "doc.json",
		New: func(owner, repo string) *doc {
			return &doc{Owner: owner, Repo: repo, Entries: map[string]string{}}
		},
		Clean: func(d *doc, owner, repo string) *doc {
			out := &doc{Owner: owner, Repo: repo, Entries: map[string]string{}}
			for k, v := range d.Entries {
				out.Entries[k] = v
			}
			return out
		},
	}
}

func TestStoreRoundTrip(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newStore()

	got, err := s.Load("acme", "widget")
	if err != nil {
		t.Fatalf("Load fresh: %v", err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("expected empty doc, got %+v", got)
	}

	got.Entries["a"] = "1"
	if err := s.Save("acme", "widget", got); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save("globex", "engine", &doc{Entries: map[string]string{"b": "2"}}); err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	loaded, err := s.Load("acme", "widget")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Entries["a"] != "1" || loaded.Owner != "acme" {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}

	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %v", repos)
	}

	if err := s.DeleteRepo("acme", "widget"); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if _, err := os.Stat(s.FilePath("acme", "widget")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, stat err = %v", err)
	}
}

func TestSlugRoundTrip(t *testing.T) {
	if got := Slug(" Acme ", "Widget/X"); got != "acme__widget_x" {
		t.Fatalf("Slug: got %q", got)
	}
	owner, repo, ok := SplitSlug("acme__widget")
	if !ok || owner != "acme" || repo != "widget" {
		t.Fatalf("SplitSlug: %q %q %v", owner, repo, ok)
	}
	if _, _, ok := SplitSlug("no-separator"); ok {
		t.Fatalf("SplitSlug should reject names without a separator")
	}
}

func TestStaleScan(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	staleAfter := 30 * 24 * time.Hour

	var fresh StaleScan
	fresh.Observe("body", now.Add(-time.Hour))
	fresh.Observe("  ", now) // blank context ignored
	if fresh.Have != 1 {
		t.Fatalf("Have = %d, want 1", fresh.Have)
	}
	if fresh.Stale(now, staleAfter) {
		t.Fatalf("recent brief should not be stale")
	}

	var old StaleScan
	old.Observe("body", now.Add(-31*24*time.Hour))
	if !old.Stale(now, staleAfter) {
		t.Fatalf("old brief should be stale")
	}

	var zero StaleScan
	zero.Observe("body", time.Time{})
	if !zero.Stale(now, staleAfter) {
		t.Fatalf("zero-timestamp brief should be stale")
	}
	if zero.Stale(now, 0) != true {
		t.Fatalf("zero timestamp is stale even with age check disabled")
	}
}

func TestSourceHashStableAndSensitive(t *testing.T) {
	a := SourceHash("a", "b")
	b := SourceHash("a", "b")
	if a != b {
		t.Fatalf("SourceHash not stable")
	}
	if SourceHash("a", "b") == SourceHash("ab") {
		t.Fatalf("NUL separation should distinguish parts")
	}
}

func TestLoadPromptOverrideThenEmbedded(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", cfg)
	fsys := fstest.MapFS{"prompts/x.md": {Data: []byte("embedded")}}

	got, err := LoadPrompt(fsys, "prompts/x.md", "x.md")
	if err != nil || got != "embedded" {
		t.Fatalf("embedded fallback: got %q err %v", got, err)
	}

	if err := os.MkdirAll(filepath.Join(cfg, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "prompts", "x.md"), []byte("override"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadPrompt(fsys, "prompts/x.md", "x.md")
	if err != nil || got != "override" {
		t.Fatalf("override wins: got %q err %v", got, err)
	}
}
