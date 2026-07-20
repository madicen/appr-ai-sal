package model

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/demo"
	"github.com/madicen/appr-ai-sal/internal/gh"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/tuitest"
)

// Golden-file render tests (Phase 5 item 11) for the root model's two biggest
// views — the PR list (review queue) and the PR detail page (tree + diff +
// controls). Both are driven off the demo fixtures at a fixed terminal size
// with a monochrome Ascii profile. The only run-to-run variation is the
// relative "updated N ago" timestamp on list rows, which redactTime replaces
// with a stable placeholder before comparison. Run with -update to refresh.

const (
	goldenTermW = 120
	goldenTermH = 40
)

// redactTime replaces humanSince output (relative ages + the >30d date
// fallback) with a fixed token so the goldens don't depend on the wall clock.
var redactTime = regexp.MustCompile(`\d+[mhd] ago|just now|\d{4}-\d{2}-\d{2}`)

func assertModelGolden(t *testing.T, name, got string) {
	t.Helper()
	got = redactTime.ReplaceAllString(tuitest.Normalize(got), "<time>")
	// tuitest.AssertGolden re-normalizes (idempotent) and applies the -update
	// convention. We pre-redact so the stored golden is stable.
	tuitest.AssertGolden(t, name, got)
}

func newDemoRootModel(t *testing.T) *Model {
	t.Helper()
	seedGoldenFixtures(t)
	m := New(Options{Demo: true, DryRun: true})
	m.Update(tea.WindowSizeMsg{Width: goldenTermW, Height: goldenTermH})
	return m
}

// seedGoldenFixtures pins config/cache under a temp dir and writes the
// same agent-brief mix the VHS demo uses: madicen/appr-ai-sal repo agents
// are complete but old enough to read "stale", lang go brief is fresh,
// and tech experts are intentionally absent ("not configured"). Without
// this, CI (no pre-seeded tmp/demo) renders every chip as "missing".
func seedGoldenFixtures(t *testing.T) {
	t.Helper()
	demoRoot := t.TempDir()
	cacheDir := filepath.Join(demoRoot, "cache")
	cfgDir := filepath.Join(demoRoot, "config")
	for _, d := range []string{cacheDir, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("seedGoldenFixtures mkdir %s: %v", d, err)
		}
	}
	t.Setenv("APPR_AI_SAL_CACHE_DIR", cacheDir)
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", cfgDir)

	now := time.Now().UTC()
	// Repo agents: older than repoagents.DefaultStaleAfter (30d) → "stale".
	repoGeneratedAt := now.Add(-45 * 24 * time.Hour)
	// Lang brief: younger than langagents.DefaultStaleAfter (60d) → "fresh".
	langGeneratedAt := now.Add(-time.Hour)
	agents := map[string]repoagentsstore.Agent{}
	for _, sp := range repoagentsstore.Specialists {
		agents[sp] = repoagentsstore.Agent{
			Specialist:  sp,
			Context:     "# " + sp + " brief\n",
			GeneratedAt: repoGeneratedAt,
			Model:       "demo",
			Provider:    "demo",
		}
	}
	if err := repoagentsstore.Save(&repoagentsstore.RepoAgents{
		Owner: "madicen", Repo: "appr-ai-sal", Agents: agents,
	}); err != nil {
		t.Fatalf("seedGoldenFixtures repo agents: %v", err)
	}
	if err := langagentsstore.SaveCache(&langagentsstore.LangAgents{
		Agents: map[langagentsstore.Language]langagentsstore.Agent{
			"go": {
				Language:    "go",
				Context:     "# Go language brief\n",
				GeneratedAt: langGeneratedAt,
				Model:       "demo",
				Provider:    "demo",
			},
		},
	}); err != nil {
		t.Fatalf("seedGoldenFixtures lang agents: %v", err)
	}
}

func TestGoldenListView(t *testing.T) {
	tuitest.ForceMonochrome(t)
	m := newDemoRootModel(t)
	m.Update(data.PRListMsg{PRs: demo.DemoPullRequests()})
	assertModelGolden(t, "list_view", m.View())
}

func TestGoldenDetailView(t *testing.T) {
	tuitest.ForceMonochrome(t)
	m := newDemoRootModel(t)
	ref := gh.Ref{Owner: "madicen", Repo: "appr-ai-sal", Number: 742}
	pr := demo.LookupPR(ref)
	if pr == nil {
		t.Fatal("demo PR #742 fixture missing")
	}
	m.Update(data.PRDetailMsg{PR: pr, Diff: demo.DemoDiff(ref)})
	assertModelGolden(t, "detail_view", m.View())
}
