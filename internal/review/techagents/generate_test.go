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

func TestGenerateUsesCompleteAndPersistsAgent(t *testing.T) {
	setupTempCache(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("# repo norms\n\n- flows live under flows/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var capturedSystem, capturedUser string
	complete := func(ctx context.Context, cfg *aiconfig.Config, system, user, _ string) (string, error) {
		capturedSystem = system
		capturedUser = user
		return "## Kestra brief\n\n- flows live under `flows/`.\n", nil
	}
	histCalled := false
	hist := func(_ context.Context, owner, repo string, prLimit, maxBytes int) (string, error) {
		histCalled = true
		return "merged-pr-1: bumped kestra plugin\n", nil
	}

	rc := repoconfig.Default()

	agent, err := Generate(context.Background(), GenerateOpts{
		AICfg:    aiconfig.DefaultConfig(),
		RC:       rc,
		Owner:    "acme",
		Repo:     "kestra-workflows",
		Worktree: worktree,
		Tech:     "Kestra",
		Label:    "Kestra",
		Seed:     "Kestra workflow engine; YAML-based",
		Complete: complete,
		History:  hist,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if agent == nil {
		t.Fatal("nil agent returned with no error")
	}
	if !histCalled {
		t.Fatal("expected HistoryFetcher to be called")
	}
	if !strings.Contains(capturedSystem, "Tech expert") {
		t.Fatalf("system prompt should be the tech generator: %q", trim120(capturedSystem))
	}
	if !strings.Contains(capturedUser, "Repository: acme/kestra-workflows") {
		t.Fatalf("user prompt missing repo header: %q", trim120(capturedUser))
	}
	if !strings.Contains(capturedUser, "Technology: Kestra") {
		t.Fatalf("user prompt missing technology label: %q", trim120(capturedUser))
	}
	if !strings.Contains(capturedUser, "Kestra workflow engine") {
		t.Fatalf("user prompt should embed the seed verbatim: %q", trim120(capturedUser))
	}
	if !strings.Contains(capturedUser, "AGENTS.md") {
		t.Fatalf("user prompt should embed convention bundle: %q", trim120(capturedUser))
	}
	if !strings.Contains(capturedUser, "merged-pr-1") {
		t.Fatalf("user prompt should embed history digest: %q", trim120(capturedUser))
	}

	if agent.Tech != "kestra" {
		t.Fatalf("agent Tech should be canonical, got %q", agent.Tech)
	}
	if agent.Label != "Kestra" {
		t.Fatalf("agent Label should preserve display form, got %q", agent.Label)
	}
	if !strings.Contains(agent.Context, "Kestra brief") {
		t.Fatalf("agent body should reflect Complete output: %q", agent.Context)
	}
	if agent.SourceHash == "" {
		t.Fatal("expected a non-empty SourceHash")
	}
	if agent.Manual {
		t.Fatal("Generate-produced agent should not be marked manual")
	}

	// Persist & reload to confirm metadata survives.
	if err := SaveAgent("acme", "kestra-workflows", *agent); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	got, err := Load("acme", "kestra-workflows")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded, ok := got.Get("kestra")
	if !ok {
		t.Fatal("kestra agent missing after save")
	}
	if loaded.SourceHash == "" {
		t.Fatal("expected SourceHash to be persisted")
	}
}

func TestGenerateSourceHashIsDeterministic(t *testing.T) {
	setupTempCache(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("repo norms"), 0o644); err != nil {
		t.Fatal(err)
	}
	complete := func(_ context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		return "stub body", nil
	}

	mk := func(seed string) *Agent {
		t.Helper()
		a, err := Generate(context.Background(), GenerateOpts{
			AICfg:    aiconfig.DefaultConfig(),
			RC:       repoconfig.Default(),
			Owner:    "a",
			Repo:     "b",
			Worktree: worktree,
			Tech:     "kestra",
			Seed:     seed,
			Complete: complete,
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return a
	}

	first := mk("seed one")
	dupe := mk("seed one")
	other := mk("seed two")

	if first.SourceHash != dupe.SourceHash {
		t.Fatalf("identical inputs should hash equal: %q vs %q", first.SourceHash, dupe.SourceHash)
	}
	if first.SourceHash == other.SourceHash {
		t.Fatalf("different seeds should hash differently: %q vs %q", first.SourceHash, other.SourceHash)
	}
}

func TestGenerateRejectsEmptyTech(t *testing.T) {
	complete := func(ctx context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		t.Fatal("Complete should not be called for empty tech")
		return "", nil
	}
	_, err := Generate(context.Background(), GenerateOpts{
		AICfg:    aiconfig.DefaultConfig(),
		Owner:    "a",
		Repo:     "b",
		Tech:     "",
		Complete: complete,
	})
	if err == nil {
		t.Fatal("expected error for empty tech")
	}
}

func TestGeneratePropagatesCompleteError(t *testing.T) {
	complete := func(ctx context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		return "", errors.New("boom")
	}
	_, err := Generate(context.Background(), GenerateOpts{
		AICfg:    aiconfig.DefaultConfig(),
		Owner:    "a",
		Repo:     "b",
		Tech:     "kestra",
		Complete: complete,
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped error from Complete, got %v", err)
	}
}

func trim120(s string) string {
	if len(s) <= 120 {
		return s
	}
	return s[:120] + "…"
}
