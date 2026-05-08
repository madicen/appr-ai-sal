package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/madicen/appr-ai-sal/internal/gh"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
)

// strip removes ANSI styling so assertions are robust to whether lipgloss
// is rendering colour in this test process. We assert on the user-visible
// text; the colour is layered on top by lipgloss when the renderer profile
// supports it.
func strip(s string) string {
	return ansi.Strip(s)
}

func TestRenderBuildAgentsHintLabel(t *testing.T) {
	cases := []struct {
		state    repoagentsstore.Freshness
		wantText string
	}{
		{repoagentsstore.FreshnessUnknown, "ctrl+b build agents"},
		{repoagentsstore.FreshnessFresh, "ctrl+b build agents"},
		{repoagentsstore.FreshnessMissing, "ctrl+b build agents (missing!)"},
		{repoagentsstore.FreshnessIncomplete, "ctrl+b build agents (partial)"},
		{repoagentsstore.FreshnessStale, "ctrl+b build agents (stale)"},
	}

	for _, tc := range cases {
		t.Run(tc.state.String(), func(t *testing.T) {
			m := &Model{
				repoAgentsFreshnessCache: map[string]repoAgentsFreshnessEntry{
					"acme/widget": {state: tc.state, computed: time.Now()},
				},
			}
			got := strip(m.renderBuildAgentsHint("acme", "widget"))
			if got != tc.wantText {
				t.Errorf("got %q, want %q", got, tc.wantText)
			}
		})
	}
}

func TestBuildRepoAgentsChipLabel(t *testing.T) {
	cases := []struct {
		state    repoagentsstore.Freshness
		wantText string
	}{
		{repoagentsstore.FreshnessUnknown, " build repo agents (ctrl+b) "},
		{repoagentsstore.FreshnessFresh, " build repo agents (ctrl+b) "},
		{repoagentsstore.FreshnessMissing, " build repo agents (ctrl+b) — missing "},
		{repoagentsstore.FreshnessIncomplete, " build repo agents (ctrl+b) — partial "},
		{repoagentsstore.FreshnessStale, " build repo agents (ctrl+b) — stale "},
	}

	for _, tc := range cases {
		t.Run(tc.state.String(), func(t *testing.T) {
			m := &Model{
				currentPR: &gh.PR{Owner: "acme", Repo: "widget"},
				repoAgentsFreshnessCache: map[string]repoAgentsFreshnessEntry{
					"acme/widget": {state: tc.state, computed: time.Now()},
				},
			}
			got := strip(m.buildRepoAgentsChip())
			if got != tc.wantText {
				t.Errorf("got %q, want %q", got, tc.wantText)
			}
		})
	}
}

func TestBuildRepoAgentsChipNoCurrentPRRendersNeutral(t *testing.T) {
	m := &Model{}
	got := strip(m.buildRepoAgentsChip())
	if !strings.Contains(got, "build repo agents (ctrl+b)") {
		t.Fatalf("expected base label, got %q", got)
	}
	if strings.Contains(got, "missing") || strings.Contains(got, "stale") || strings.Contains(got, "partial") {
		t.Errorf("expected no warning suffix when no PR is loaded, got %q", got)
	}
}

func TestRepoAgentsFreshnessUsesAndPopulatesCache(t *testing.T) {
	m := &Model{}

	// Pre-populate the cache so we know we're not touching disk in this test.
	m.repoAgentsFreshnessCache = map[string]repoAgentsFreshnessEntry{
		"acme/widget": {state: repoagentsstore.FreshnessStale, computed: time.Now()},
	}

	if got := m.repoAgentsFreshness("acme", "widget"); got != repoagentsstore.FreshnessStale {
		t.Errorf("cached lookup: got %v, want stale", got)
	}

	// Lookups normalise to lowercase.
	if got := m.repoAgentsFreshness("ACME", "Widget"); got != repoagentsstore.FreshnessStale {
		t.Errorf("case-insensitive lookup: got %v, want stale", got)
	}

	// Empty owner / repo short-circuits to Unknown without touching disk.
	if got := m.repoAgentsFreshness("", "widget"); got != repoagentsstore.FreshnessUnknown {
		t.Errorf("empty owner: got %v, want unknown", got)
	}
	if got := m.repoAgentsFreshness("acme", ""); got != repoagentsstore.FreshnessUnknown {
		t.Errorf("empty repo: got %v, want unknown", got)
	}
}

func TestRepoAgentsFreshnessExpiredCacheRereads(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")

	m := &Model{}
	// Stale entry well beyond the TTL — must be re-read on the next call.
	m.repoAgentsFreshnessCache = map[string]repoAgentsFreshnessEntry{
		"acme/widget": {
			state:    repoagentsstore.FreshnessFresh,
			computed: time.Now().Add(-time.Hour),
		},
	}
	got := m.repoAgentsFreshness("acme", "widget")
	// The on-disk cache is empty, so a re-read should report missing.
	if got != repoagentsstore.FreshnessMissing {
		t.Errorf("expected expired cache to re-read and return missing, got %v", got)
	}
}

func TestInvalidateRepoAgentsFreshness(t *testing.T) {
	m := &Model{
		repoAgentsFreshnessCache: map[string]repoAgentsFreshnessEntry{
			"acme/widget":  {state: repoagentsstore.FreshnessFresh, computed: time.Now()},
			"other/repo":   {state: repoagentsstore.FreshnessStale, computed: time.Now()},
			"third/branch": {state: repoagentsstore.FreshnessMissing, computed: time.Now()},
		},
	}
	m.invalidateRepoAgentsFreshness()
	if m.repoAgentsFreshnessCache != nil {
		t.Errorf("expected cache to be cleared, got %d entries", len(m.repoAgentsFreshnessCache))
	}
}
