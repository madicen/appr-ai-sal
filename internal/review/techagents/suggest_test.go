package techagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
)

func TestSuggestParsesAndExcludesExisting(t *testing.T) {
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "go.mod"), []byte("module x\n\nrequire github.com/segmentio/kafka-go v0.4.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var capturedUser string
	complete := func(_ context.Context, _ *aiconfig.Config, _, user, _ string) (string, error) {
		capturedUser = user
		// Note: terraform is a dupe of the Existing list; bubble-tea is new.
		return "```json\n[\n" +
			`  {"tech": "Terraform", "label": "Terraform", "seed": "infra", "rationale": "main.tf"},` + "\n" +
			`  {"tech": "kafka", "label": "Kafka", "seed": "events", "rationale": "kafka-go in go.mod"},` + "\n" +
			`  {"tech": "kafka", "label": "Kafka dup", "seed": "dup", "rationale": "dup"}` + "\n" +
			"]\n```", nil
	}

	cands, err := Suggest(context.Background(), SuggestOpts{
		AICfg:    aiconfig.DefaultConfig(),
		RC:       repoconfig.Default(),
		Owner:    "acme",
		Repo:     "widget",
		Worktree: worktree,
		Complete: complete,
		Existing: []string{"terraform"},
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate after dedupe/exclusion, got %d: %+v", len(cands), cands)
	}
	if cands[0].Tech != "kafka" {
		t.Fatalf("expected kafka candidate, got %q", cands[0].Tech)
	}
	if cands[0].Label != "Kafka" {
		t.Fatalf("expected label Kafka, got %q", cands[0].Label)
	}
	if !strings.Contains(capturedUser, "Repository: acme/widget") {
		t.Fatalf("user prompt missing repo header: %q", capturedUser)
	}
	if !strings.Contains(capturedUser, "kafka-go") {
		t.Fatalf("user prompt should embed the harvested go.mod manifest: %q", capturedUser)
	}
}

func TestSuggestNoRepoAccess(t *testing.T) {
	complete := func(_ context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		t.Fatal("Complete should not be called without repo access")
		return "", nil
	}
	_, err := Suggest(context.Background(), SuggestOpts{
		AICfg:    aiconfig.DefaultConfig(),
		RC:       repoconfig.Default(),
		Owner:    "acme",
		Repo:     "widget",
		Worktree: filepath.Join(t.TempDir(), "does-not-exist"),
		Complete: complete,
	})
	if !errors.Is(err, ErrNoRepoAccess) {
		t.Fatalf("expected ErrNoRepoAccess, got %v", err)
	}
}

func TestSuggestPropagatesCompleteError(t *testing.T) {
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	complete := func(_ context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		return "", errors.New("boom")
	}
	_, err := Suggest(context.Background(), SuggestOpts{
		AICfg:    aiconfig.DefaultConfig(),
		RC:       repoconfig.Default(),
		Owner:    "acme",
		Repo:     "widget",
		Worktree: worktree,
		Complete: complete,
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped Complete error, got %v", err)
	}
}

func TestSuggestRejectsNonJSON(t *testing.T) {
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	complete := func(_ context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		return "I could not find any technologies, sorry.", nil
	}
	_, err := Suggest(context.Background(), SuggestOpts{
		AICfg:    aiconfig.DefaultConfig(),
		RC:       repoconfig.Default(),
		Owner:    "acme",
		Repo:     "widget",
		Worktree: worktree,
		Complete: complete,
	})
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected parse error for non-JSON output, got %v", err)
	}
}

func TestParseCandidatesBare(t *testing.T) {
	got, err := parseCandidates(`[{"tech":"redis","label":"Redis","seed":"cache","rationale":"go-redis"}]`)
	if err != nil {
		t.Fatalf("parseCandidates: %v", err)
	}
	if len(got) != 1 || got[0].Tech != "redis" {
		t.Fatalf("unexpected parse result: %+v", got)
	}
}

func TestParseCandidatesWithSurroundingProse(t *testing.T) {
	raw := "Here are the technologies:\n[{\"tech\":\"nginx\",\"label\":\"NGINX\"}]\nHope that helps!"
	got, err := parseCandidates(raw)
	if err != nil {
		t.Fatalf("parseCandidates: %v", err)
	}
	if len(got) != 1 || got[0].Tech != "nginx" {
		t.Fatalf("unexpected parse result: %+v", got)
	}
}

func TestDedupeCandidatesDropsEmptyKeys(t *testing.T) {
	in := []Candidate{
		{Tech: "", Label: "Postgres"},
		{Tech: "!!!", Label: ""},
		{Tech: "kafka", Label: "Kafka"},
	}
	out := dedupeCandidates(in, nil)
	// "" tech falls back to label "Postgres" -> postgres; "!!!"+"" -> dropped.
	gotKeys := map[string]bool{}
	for _, c := range out {
		gotKeys[c.Tech] = true
	}
	if !gotKeys["postgres"] || !gotKeys["kafka"] {
		t.Fatalf("expected postgres + kafka, got %+v", out)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(out), out)
	}
}
