package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
)

// Mirrors repoagents_freshness_test.go: confirms the lang-agents chip
// and status hint flip between neutral / yellow (stale) / red (missing)
// based on the PR-aggregated freshness reading. We strip ANSI so the
// assertions are robust to whether lipgloss is rendering colour in this
// test process — the colour is layered on top by lipgloss based on the
// renderer profile.

func TestRenderBuildLangAgentsHintLabel(t *testing.T) {
	cases := []struct {
		state    langagentsstore.Freshness
		wantText string
	}{
		{langagentsstore.FreshnessUnknown, "ctrl+l lang experts"},
		{langagentsstore.FreshnessFresh, "ctrl+l lang experts"},
		{langagentsstore.FreshnessMissing, "ctrl+l lang experts (missing!)"},
		{langagentsstore.FreshnessStale, "ctrl+l lang experts (stale)"},
	}

	for _, tc := range cases {
		t.Run(tc.state.String(), func(t *testing.T) {
			m := &Model{
				// Pre-populate both maps so the helper never
				// touches disk: prLanguages says "we know
				// this PR touches go" and the freshness
				// cache supplies the desired state directly.
				prLanguages: map[string][]langagentsstore.Language{
					"acme/widget#42": {"go"},
				},
				langAgentsFreshnessCache: map[string]langAgentsFreshnessEntry{
					"acme/widget#42": {state: tc.state, computed: time.Now()},
				},
			}
			got := strip(m.renderBuildLangAgentsHint("acme", "widget", 42))
			if got != tc.wantText {
				t.Errorf("got %q, want %q", got, tc.wantText)
			}
		})
	}
}

func TestRenderBuildLangAgentsHintUnknownWhenPRNotVisited(t *testing.T) {
	m := &Model{}
	got := strip(m.renderBuildLangAgentsHint("acme", "widget", 42))
	if got != "ctrl+l lang experts" {
		t.Errorf("un-visited PR should render neutral, got %q", got)
	}
}

func TestBuildLangAgentsChipLabel(t *testing.T) {
	cases := []struct {
		state    langagentsstore.Freshness
		wantText string
	}{
		{langagentsstore.FreshnessUnknown, " build lang experts (ctrl+l) "},
		{langagentsstore.FreshnessFresh, " build lang experts (ctrl+l) "},
		{langagentsstore.FreshnessMissing, " build lang experts (ctrl+l) — missing "},
		{langagentsstore.FreshnessStale, " build lang experts (ctrl+l) — stale "},
	}

	for _, tc := range cases {
		t.Run(tc.state.String(), func(t *testing.T) {
			m := &Model{
				currentPR: &gh.PR{Owner: "acme", Repo: "widget", Number: 42},
				prLanguages: map[string][]langagentsstore.Language{
					"acme/widget#42": {"go"},
				},
				langAgentsFreshnessCache: map[string]langAgentsFreshnessEntry{
					"acme/widget#42": {state: tc.state, computed: time.Now()},
				},
			}
			got := strip(m.buildLangAgentsChip())
			if got != tc.wantText {
				t.Errorf("got %q, want %q", got, tc.wantText)
			}
		})
	}
}

func TestBuildLangAgentsChipNoCurrentPRRendersNeutral(t *testing.T) {
	m := &Model{}
	got := strip(m.buildLangAgentsChip())
	if !strings.Contains(got, "build lang experts (ctrl+l)") {
		t.Fatalf("expected base label, got %q", got)
	}
	if strings.Contains(got, "missing") || strings.Contains(got, "stale") {
		t.Errorf("expected no warning suffix when no PR is loaded, got %q", got)
	}
}

func TestLangAgentsFreshnessUnknownWhenPRMissingFromCache(t *testing.T) {
	m := &Model{}
	if got := m.langAgentsFreshness("acme", "widget", 42); got != langagentsstore.FreshnessUnknown {
		t.Errorf("un-visited PR: got %v, want unknown", got)
	}
}

func TestLangAgentsFreshnessShortCircuitsOnEmptyInput(t *testing.T) {
	m := &Model{}
	if got := m.langAgentsFreshness("", "widget", 42); got != langagentsstore.FreshnessUnknown {
		t.Errorf("empty owner: got %v, want unknown", got)
	}
	if got := m.langAgentsFreshness("acme", "", 42); got != langagentsstore.FreshnessUnknown {
		t.Errorf("empty repo: got %v, want unknown", got)
	}
	if got := m.langAgentsFreshness("acme", "widget", 0); got != langagentsstore.FreshnessUnknown {
		t.Errorf("zero number: got %v, want unknown", got)
	}
}

func TestLangAgentsFreshnessUsesCacheWithinTTL(t *testing.T) {
	m := &Model{
		prLanguages: map[string][]langagentsstore.Language{
			"acme/widget#42": {"go"},
		},
		langAgentsFreshnessCache: map[string]langAgentsFreshnessEntry{
			"acme/widget#42": {state: langagentsstore.FreshnessStale, computed: time.Now()},
		},
	}
	if got := m.langAgentsFreshness("acme", "widget", 42); got != langagentsstore.FreshnessStale {
		t.Errorf("cached lookup: got %v, want stale", got)
	}
	// Case-insensitive lookup uses the same key.
	if got := m.langAgentsFreshness("ACME", "Widget", 42); got != langagentsstore.FreshnessStale {
		t.Errorf("case-insensitive lookup: got %v, want stale", got)
	}
}

func TestInvalidateLangAgentsFreshnessClearsCache(t *testing.T) {
	m := &Model{
		langAgentsFreshnessCache: map[string]langAgentsFreshnessEntry{
			"acme/widget#42": {state: langagentsstore.FreshnessMissing, computed: time.Now()},
			"acme/widget#43": {state: langagentsstore.FreshnessStale, computed: time.Now()},
		},
	}
	m.invalidateLangAgentsFreshness()
	if m.langAgentsFreshnessCache != nil {
		t.Errorf("expected cache to be cleared, got %d entries", len(m.langAgentsFreshnessCache))
	}
}
