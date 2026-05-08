package repoagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTempCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")
	return dir
}

func TestSpecialistsAndIsKnown(t *testing.T) {
	for _, s := range Specialists {
		if !IsKnownSpecialist(s) {
			t.Errorf("specialist %q expected to be recognised", s)
		}
		if !IsKnownSpecialist(strings.ToUpper(s)) {
			t.Errorf("specialist %q expected to be case-insensitive", s)
		}
	}
	if IsKnownSpecialist("vibe-coach") {
		t.Errorf("vibe-coach should not be a repo-agent specialist")
	}
	if IsKnownSpecialist("") {
		t.Errorf("empty string should not be a known specialist")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	setupTempCache(t)
	owner, repo := "acme", "widget"

	got, err := Load(owner, repo)
	if err != nil {
		t.Fatalf("Load fresh: %v", err)
	}
	if got == nil || len(got.Agents) != 0 {
		t.Fatalf("expected empty RepoAgents, got %+v", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	a1 := Agent{
		Specialist:  "testing",
		Context:     "this repo tests with go test ./...",
		GeneratedAt: now,
		Model:       "claude-sonnet",
		Provider:    "claude",
	}
	a2 := Agent{
		Specialist:  "formatting",
		Context:     "go fmt + golangci-lint enabled",
		GeneratedAt: now,
		Manual:      true,
	}

	if err := SaveAgent(owner, repo, a1); err != nil {
		t.Fatalf("SaveAgent #1: %v", err)
	}
	if err := SaveAgent(owner, repo, a2); err != nil {
		t.Fatalf("SaveAgent #2: %v", err)
	}

	loaded, err := Load(owner, repo)
	if err != nil {
		t.Fatalf("Load after saves: %v", err)
	}
	if len(loaded.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(loaded.Agents))
	}
	if loaded.ContextFor("testing") != a1.Context {
		t.Fatalf("testing roundtrip: got %q want %q", loaded.ContextFor("testing"), a1.Context)
	}
	if got, _ := loaded.Get("formatting"); !got.Manual {
		t.Fatalf("expected formatting agent to be marked manual")
	}

	if err := Delete(owner, repo, "testing"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	loaded, err = Load(owner, repo)
	if err != nil {
		t.Fatalf("Load after delete: %v", err)
	}
	if _, ok := loaded.Get("testing"); ok {
		t.Fatalf("testing should be deleted")
	}
	if !loaded.HasAny() {
		t.Fatalf("formatting should still be present")
	}
}

func TestListReposPicksUpSavedFiles(t *testing.T) {
	setupTempCache(t)
	if err := SaveAgent("acme", "widget", Agent{Specialist: "design", Context: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveAgent("globex", "engine", Agent{Specialist: "design", Context: "y"}); err != nil {
		t.Fatal(err)
	}

	repos, err := ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %v", repos)
	}
	wantContains := []string{"acme/widget", "globex/engine"}
	got := strings.Join(repos, ",")
	for _, w := range wantContains {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in %q", w, got)
		}
	}
}

func TestSortedSpecialistsMatchesSpecialistOrder(t *testing.T) {
	r := &RepoAgents{Owner: "acme", Repo: "widget", Agents: map[string]Agent{
		"security":   {Specialist: "security"},
		"docs":       {Specialist: "docs"},
		"formatting": {Specialist: "formatting"},
	}}
	got := r.SortedSpecialists()
	want := []string{"formatting", "docs", "security"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order: got %v want %v", got, want)
	}
}

func TestDeleteRepoRemovesFile(t *testing.T) {
	dir := setupTempCache(t)
	_ = SaveAgent("acme", "widget", Agent{Specialist: "design", Context: "x"})
	if err := DeleteRepo("acme", "widget"); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "appr-ai-sal", "repo-profiles", "acme__widget", "repo-agents.json")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, stat err = %v", err)
	}
	repos, _ := ListRepos()
	if len(repos) != 0 {
		t.Fatalf("expected empty list after DeleteRepo, got %v", repos)
	}
}
