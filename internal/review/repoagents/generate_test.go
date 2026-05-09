package repoagents

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

func TestGenerateUsesProvidedCompleteAndPersistsAgent(t *testing.T) {
	setupTempCache(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("# repo norms\n\n- always test handlers\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var capturedSystem, capturedUser string
	complete := func(ctx context.Context, cfg *aiconfig.Config, system, user, _ string) (string, error) {
		capturedSystem = system
		capturedUser = user
		return "## Testing brief\n\n- run `go test ./...` from repo root.", nil
	}
	histCalled := false
	hist := func(_ context.Context, owner, repo string, prLimit, maxBytes int) (string, error) {
		histCalled = true
		return "merged-pr-1: handled validation\n", nil
	}

	rc := repoconfig.Default()
	rc.IncludePRHistory = true

	agent, err := Generate(context.Background(), GenerateOpts{
		AICfg:      aiconfig.DefaultConfig(),
		RC:         rc,
		Owner:      "acme",
		Repo:       "widget",
		Worktree:   worktree,
		Specialist: "testing",
		Complete:   complete,
		History:    hist,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if agent == nil {
		t.Fatalf("nil agent returned with no error")
	}
	if !histCalled {
		t.Fatalf("expected HistoryFetcher to be called")
	}
	if !strings.Contains(capturedSystem, "Repo agent: testing") {
		t.Fatalf("system prompt should be the testing generator: %q", capturedSystem[:min(120, len(capturedSystem))])
	}
	if !strings.Contains(capturedUser, "Repository: acme/widget") {
		t.Fatalf("user prompt missing repo header: %q", capturedUser[:min(120, len(capturedUser))])
	}
	if !strings.Contains(capturedUser, "AGENTS.md") {
		t.Fatalf("user prompt should embed convention bundle (AGENTS.md): %q", capturedUser[:min(200, len(capturedUser))])
	}
	if !strings.Contains(capturedUser, "merged-pr-1") {
		t.Fatalf("user prompt should embed history digest: %q", capturedUser[:min(400, len(capturedUser))])
	}
	if !strings.Contains(agent.Context, "Testing brief") {
		t.Fatalf("agent body should reflect Complete output: %q", agent.Context)
	}

	// Persist & reload to confirm SourceHash and metadata survive.
	if err := SaveAgent("acme", "widget", *agent); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	got, err := Load("acme", "widget")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded, ok := got.Get("testing")
	if !ok {
		t.Fatalf("testing agent missing after save")
	}
	if loaded.SourceHash == "" {
		t.Fatalf("expected non-empty SourceHash to be persisted")
	}
	if loaded.Manual {
		t.Fatalf("Generate-produced agent should not be marked manual")
	}
}

func TestGenerateRejectsUnknownSpecialist(t *testing.T) {
	complete := func(ctx context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		t.Fatal("Complete should not be called for unknown specialist")
		return "", nil
	}
	_, err := Generate(context.Background(), GenerateOpts{
		AICfg:      aiconfig.DefaultConfig(),
		Owner:      "a",
		Repo:       "b",
		Specialist: "vibe-coach",
		Complete:   complete,
	})
	if err == nil {
		t.Fatal("expected error for unknown specialist")
	}
}

func TestGeneratePropagatesCompleteError(t *testing.T) {
	complete := func(ctx context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
		return "", errors.New("boom")
	}
	_, err := Generate(context.Background(), GenerateOpts{
		AICfg:      aiconfig.DefaultConfig(),
		Owner:      "a",
		Repo:       "b",
		Specialist: "testing",
		Complete:   complete,
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped error from Complete, got %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
