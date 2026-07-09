package techagents

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

func TestCanonicalTechNormalises(t *testing.T) {
	cases := map[string]string{
		"Kestra":      "kestra",
		" Terraform ": "terraform",
		"kafka":       "kafka",
		"AWS-CDK":     "aws-cdk",
		"react/next":  "react-next",
		"":            "",
		"   ":         "",
		"!!!":         "",
		"ci_cd":       "ci-cd",
	}
	for in, want := range cases {
		if got := CanonicalTech(in); got != want {
			t.Errorf("CanonicalTech(%q): got %q want %q", in, got, want)
		}
	}
}

func TestStoreRoundTripPreservesLabelAndSeed(t *testing.T) {
	setupTempCache(t)
	owner, repo := "acme", "widget"

	got, err := Load(owner, repo)
	if err != nil {
		t.Fatalf("Load fresh: %v", err)
	}
	if got == nil || len(got.Agents) != 0 {
		t.Fatalf("expected empty TechAgents, got %+v", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	a := Agent{
		Tech:        "Kestra",
		Label:       "Kestra",
		Seed:        "Kestra workflow engine; YAML-based",
		Context:     "## Kestra brief\n\n- flows under flows/.",
		GeneratedAt: now,
		Model:       "claude-sonnet",
		Provider:    "claude",
	}
	if err := SaveAgent(owner, repo, a); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}

	loaded, err := Load(owner, repo)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if len(loaded.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(loaded.Agents))
	}
	got2, ok := loaded.Get("kestra") // canonical lookup
	if !ok {
		t.Fatalf("missing kestra agent after canonical lookup")
	}
	if got2.Tech != "kestra" {
		t.Fatalf("Tech should be canonicalised, got %q", got2.Tech)
	}
	if got2.Label != "Kestra" {
		t.Fatalf("Label should round-trip exactly, got %q", got2.Label)
	}
	if got2.Seed != a.Seed {
		t.Fatalf("Seed should round-trip, got %q want %q", got2.Seed, a.Seed)
	}
	if got2.Context != a.Context {
		t.Fatalf("Context should round-trip, got %q", got2.Context)
	}
	if !loaded.HasAny() {
		t.Fatalf("HasAny should be true after Save")
	}

	// Delete should remove the entry but leave the file behind.
	if err := Delete(owner, repo, "Kestra"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	loaded, err = Load(owner, repo)
	if err != nil {
		t.Fatalf("Load after delete: %v", err)
	}
	if _, ok := loaded.Get("kestra"); ok {
		t.Fatalf("kestra should be deleted")
	}
}

func TestSetCanonicalisesAndPreservesLabelFromKey(t *testing.T) {
	ta := &TechAgents{Owner: "a", Repo: "b"}
	ta.Set("Terraform", Agent{Context: "x"})
	got, ok := ta.Get("terraform")
	if !ok {
		t.Fatalf("expected canonical lookup to hit")
	}
	if got.Label != "Terraform" {
		t.Fatalf("Label should default to caller-supplied tech, got %q", got.Label)
	}
}

func TestSortedTechsIsAlphabeticalAndStable(t *testing.T) {
	ta := &TechAgents{Agents: map[string]Agent{
		"terraform": {Tech: "terraform"},
		"kestra":    {Tech: "kestra"},
		"airflow":   {Tech: "airflow"},
	}}
	got := ta.SortedTechs()
	want := []string{"airflow", "kestra", "terraform"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order: got %v want %v", got, want)
	}
}

func TestListReposPicksUpSavedFiles(t *testing.T) {
	setupTempCache(t)
	if err := SaveAgent("acme", "widget", Agent{Tech: "kestra", Context: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveAgent("globex", "engine", Agent{Tech: "terraform", Context: "y"}); err != nil {
		t.Fatal(err)
	}

	repos, err := ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %v", repos)
	}
	for _, w := range []string{"acme/widget", "globex/engine"} {
		if !strings.Contains(strings.Join(repos, ","), w) {
			t.Fatalf("missing %q in %q", w, repos)
		}
	}
}

func TestDeleteRepoRemovesFile(t *testing.T) {
	dir := setupTempCache(t)
	_ = SaveAgent("acme", "widget", Agent{Tech: "kestra", Context: "x"})
	if err := DeleteRepo("acme", "widget"); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "appr-ai-sal", "repo-profiles", "acme__widget", "tech-agents.json")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, stat err = %v", err)
	}
}

func TestLabelForFallsBackToCanonical(t *testing.T) {
	ta := &TechAgents{Agents: map[string]Agent{
		"kestra": {Tech: "kestra", Label: "Kestra"},
		"k8s":    {Tech: "k8s"}, // no label stored
	}}
	if got := ta.LabelFor("kestra"); got != "Kestra" {
		t.Fatalf("LabelFor(kestra): got %q want %q", got, "Kestra")
	}
	if got := ta.LabelFor("k8s"); got != "k8s" {
		t.Fatalf("LabelFor(k8s) without stored label: got %q want %q", got, "k8s")
	}
	if got := ta.LabelFor("missing"); got != "missing" {
		t.Fatalf("LabelFor missing tech: got %q want %q", got, "missing")
	}
}
