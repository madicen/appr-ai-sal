package repoagents

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	ra "github.com/madicen/appr-ai-sal/internal/review/repoagents"
)

func newFocusTestModel(t *testing.T, opts Opts) *Model {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")
	if opts.Complete == nil {
		opts.Complete = ra.CompleteFunc(func(_ context.Context, _ *aiconfig.Config, _, _, _ string) (string, error) {
			return "stub-context", nil
		})
	}
	if opts.AICfg == nil {
		opts.AICfg = aiconfig.DefaultConfig()
	}
	if opts.RC == nil {
		opts.RC = repoconfig.Default()
	}
	if opts.Width == 0 {
		opts.Width = 140
	}
	if opts.BodyHeight == 0 {
		opts.BodyHeight = 30
	}
	return New(opts)
}

// drainBatch unwraps a tea.Batch into its constituent commands. Returns nil
// when cmd is nil. Used by tests so we can assert on what the model
// dispatches without spinning up a full Program loop.
func drainBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := make([]tea.Msg, 0, len(batch))
		for _, sub := range batch {
			out = append(out, drainBatch(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestFocusRepoSelectsTargetIndex verifies that Opts.FocusRepo lands the
// active row on the requested owner/repo even when it isn't the
// alphabetical first one in the seed list.
func TestFocusRepoSelectsTargetIndex(t *testing.T) {
	m := newFocusTestModel(t, Opts{
		InitialRepos: []string{"acme/widget", "globex/engine", "stark/iron"},
		FocusRepo:    "globex/engine",
	})
	if m.currentRepoKey() != "globex/engine" {
		t.Fatalf("FocusRepo should select globex/engine, got %q", m.currentRepoKey())
	}
}

// TestFocusRepoAddsMissingSeed ensures that a focus repo that wasn't in the
// initial seed list is appended (so "Build agents for THIS PR" works on a
// repo we just discovered from PR detail).
func TestFocusRepoAddsMissingSeed(t *testing.T) {
	m := newFocusTestModel(t, Opts{
		InitialRepos: []string{"acme/widget"},
		FocusRepo:    "stack/product-crawler",
	})
	if m.currentRepoKey() != "stack/product-crawler" {
		t.Fatalf("FocusRepo should be selected even when missing from seeds, got %q", m.currentRepoKey())
	}
	found := false
	for _, r := range m.repos {
		if r == "stack/product-crawler" {
			found = true
		}
	}
	if !found {
		t.Errorf("FocusRepo should be appended to repos list, got %v", m.repos)
	}
}

// TestFocusRepoLowercased confirms the focus key is normalized so callers
// can pass e.g. "StackAdapt/Product-Crawler" without mismatching the
// lowercased seed list.
func TestFocusRepoLowercased(t *testing.T) {
	m := newFocusTestModel(t, Opts{
		InitialRepos: []string{"stackadapt/product-crawler"},
		FocusRepo:    "StackAdapt/Product-Crawler",
	})
	if m.currentRepoKey() != "stackadapt/product-crawler" {
		t.Fatalf("FocusRepo should be lowercased, got %q", m.currentRepoKey())
	}
}

// TestAutoRegenAllFiresRegenStartedForEverySpecialist verifies that
// Init() with AutoRegenAll dispatches a regenStartedMsg for every fixed
// specialist — i.e. one key press in PR detail rebuilds all five agents.
func TestAutoRegenAllFiresRegenStartedForEverySpecialist(t *testing.T) {
	m := newFocusTestModel(t, Opts{
		InitialRepos: []string{"acme/widget"},
		FocusRepo:    "acme/widget",
		AutoRegenAll: true,
	})
	if m.pendingAutoRegen != "acme/widget" {
		t.Errorf("pendingAutoRegen should be 'acme/widget' before Init runs, got %q", m.pendingAutoRegen)
	}
	cmd := m.Init()
	if m.pendingAutoRegen != "" {
		t.Errorf("pendingAutoRegen should be cleared after Init runs, got %q", m.pendingAutoRegen)
	}
	msgs := drainBatch(cmd)
	got := map[string]bool{}
	for _, msg := range msgs {
		if r, ok := msg.(regenStartedMsg); ok {
			got[r.Specialist] = true
		}
	}
	for _, spec := range ra.Specialists {
		if !got[spec] {
			t.Errorf("AutoRegenAll should dispatch regenStartedMsg for %q (got %v)", spec, got)
		}
	}
	for _, spec := range ra.Specialists {
		if !m.busy[busyKey("acme", "widget", spec)] {
			t.Errorf("regenerateAllForCurrentRepo should mark busy[%s|%s]", "acme/widget", spec)
		}
	}
}

// TestAutoRegenAllSkippedWhenFocusMissing protects against regenerating
// against the wrong repo when callers pass AutoRegenAll without a usable
// focus key.
func TestAutoRegenAllSkippedWhenFocusMissing(t *testing.T) {
	m := newFocusTestModel(t, Opts{
		InitialRepos: []string{"acme/widget"},
		AutoRegenAll: true, // no FocusRepo
	})
	cmd := m.Init()
	for _, msg := range drainBatch(cmd) {
		if _, ok := msg.(regenStartedMsg); ok {
			t.Fatalf("AutoRegenAll without FocusRepo must not start regenerations")
		}
	}
}

// TestFocusRepoSurvivesReposLoadedResort is the regression for a subtle
// bug where opening the tab focused on a specific repo landed correctly
// at New() time, but the async reposLoadedMsg handler then merged disk
// repos, re-sorted the list alphabetically, and left repoIdx pointing
// at a stale numeric position — so the user ended up viewing whatever
// repo happened to fall at that index (usually the alphabetically
// first one), not the one they asked for.
//
// We simulate the wire-level sequence: New(FocusRepo=...) → Init() →
// reposLoadedMsg arrives with disk repos that, when merged, shift the
// focus key away from its original index. The selection must follow
// the focus, not the index.
func TestFocusRepoSurvivesReposLoadedResort(t *testing.T) {
	m := newFocusTestModel(t, Opts{
		// At New time the seed list (focus appended + sorted) is just
		// ["globex/engine"], so repoIdx = 0.
		InitialRepos: []string{},
		FocusRepo:    "globex/engine",
	})
	if got := m.CurrentRepoKey(); got != "globex/engine" {
		t.Fatalf("pre-condition: New() should land on focus; got %q", got)
	}

	// reposLoadedMsg with disk repos that sort BEFORE the focus key.
	// After the merge the sorted list is:
	//   ["aaa/foo", "bbb/bar", "globex/engine"]
	// The pre-fix code keeps repoIdx = 0 and we'd land on "aaa/foo".
	updated, _ := m.Update(reposLoadedMsg{
		Repos: []string{"aaa/foo", "bbb/bar"},
	})
	m2 := updated.(*Model)

	if got := m2.CurrentRepoKey(); got != "globex/engine" {
		t.Fatalf("focus must survive reposLoadedMsg resort; got %q want globex/engine (repoIdx=%d, repos=%v)",
			got, m2.repoIdx, m2.repos)
	}

	// A second reposLoadedMsg should NOT re-apply focus (focusRepo
	// was cleared on first apply). This pins the "one-shot" contract:
	// after the initial reload, the user's manual selection wins over
	// any further disk merges.
	if err := m2.SelectRepo("bbb/bar"); !err {
		t.Fatal("SelectRepo bbb/bar should succeed")
	}
	updated2, _ := m2.Update(reposLoadedMsg{Repos: []string{"zzz/late"}})
	m3 := updated2.(*Model)
	if got := m3.CurrentRepoKey(); got != "bbb/bar" {
		t.Fatalf("second reposLoadedMsg must not re-focus past the user's choice; got %q want bbb/bar", got)
	}
}

// TestSelectRepoSetsActiveAndAddsMissing covers the public SelectRepo entry
// point used to retarget an already-open tab.
func TestSelectRepoSetsActiveAndAddsMissing(t *testing.T) {
	m := newFocusTestModel(t, Opts{InitialRepos: []string{"a/b", "c/d"}})
	if !m.SelectRepo("c/d") {
		t.Fatal("SelectRepo on existing repo should return true")
	}
	if m.currentRepoKey() != "c/d" {
		t.Fatalf("currentRepoKey %q want c/d", m.currentRepoKey())
	}
	if !m.SelectRepo("e/f") {
		t.Fatal("SelectRepo for missing repo should add and return true")
	}
	if m.currentRepoKey() != "e/f" {
		t.Fatalf("currentRepoKey %q want e/f after SelectRepo on new repo", m.currentRepoKey())
	}
	if m.SelectRepo("invalid") {
		t.Errorf("SelectRepo on owner-only string should return false")
	}
}
