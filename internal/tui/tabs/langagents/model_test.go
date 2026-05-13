package langagents

import (
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	la "github.com/madicen/appr-ai-sal/internal/review/langagents"
)

// TestScopedModeShowsOnlyProvidedLanguages confirms that opening the
// tab with PRLanguages renders exactly those languages, in the order
// given — not the full known-language list. This is the contract the
// detail-mode opener relies on.
func TestScopedModeShowsOnlyProvidedLanguages(t *testing.T) {
	setEmptyCacheDir(t)
	m := New(Opts{
		PRLanguages: []la.Language{"go", "python", "swift"},
	}).(*Model)
	if got := len(m.rows); got != 3 {
		t.Fatalf("scoped row count = %d, want 3", got)
	}
	want := []la.Language{"go", "python", "swift"}
	for i, w := range want {
		if m.rows[i].Language != w {
			t.Errorf("row %d = %q, want %q", i, m.rows[i].Language, w)
		}
	}
}

// TestScopedModeWithEmptySliceRendersNoRows: passing a non-nil empty
// scope is "this PR touches nothing we recognise" — header should
// still indicate scoped mode but no rows are rendered. This is
// distinct from passing nil, which falls back to unscoped (cached).
func TestScopedModeWithEmptySliceRendersNoRows(t *testing.T) {
	setEmptyCacheDir(t)
	m := New(Opts{
		PRLanguages: []la.Language{},
	}).(*Model)
	if len(m.rows) != 0 {
		t.Errorf("empty scope row count = %d, want 0", len(m.rows))
	}
	if m.scope == nil {
		t.Error("empty PRLanguages should still set scope (opt into scoped mode)")
	}
	if got := m.title(); got != "Language experts · PR scope" {
		t.Errorf("title with empty scope = %q, want scoped fallback", got)
	}
}

// TestUnscopedModeShowsOnlyCachedLanguages: when PRLanguages is nil
// and the cache has populated entries, only those entries appear.
// Languages without a cached brief are not rendered (the user is
// expected to drill into a PR to discover and generate them).
func TestUnscopedModeShowsOnlyCachedLanguages(t *testing.T) {
	setEmptyCacheDir(t)
	mustSaveAgent(t, "go")
	mustSaveAgent(t, "rust")
	m := New(Opts{}).(*Model)
	// Trigger an initial cache load synchronously by sending the
	// cacheLoadedMsg the way Init's command would after running.
	cache, err := la.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	updated, _ := m.Update(cacheLoadedMsg{Cache: cache})
	m = updated.(*Model)
	if got := len(m.rows); got != 2 {
		t.Fatalf("unscoped row count = %d, want 2 cached", got)
	}
	got := []la.Language{m.rows[0].Language, m.rows[1].Language}
	want := []la.Language{"go", "rust"} // alphabetical
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestPRLabelAppearsInTitle confirms the header carries the caller's
// PR label when provided, so the user sees which PR scoped the tab.
func TestPRLabelAppearsInTitle(t *testing.T) {
	setEmptyCacheDir(t)
	m := New(Opts{
		PRLanguages: []la.Language{"go"},
		PRLabel:     "owner/repo#42",
	}).(*Model)
	if got := m.title(); got != "Language experts · owner/repo#42" {
		t.Errorf("scoped title = %q, want to include label", got)
	}
}

// TestScopeCanonicaliesAndDeduplicates: callers can pass any mix of
// extensions, aliases, and duplicates; the model holds a clean set.
func TestScopeCanonicaliesAndDeduplicates(t *testing.T) {
	setEmptyCacheDir(t)
	m := New(Opts{
		PRLanguages: []la.Language{"golang", ".go", "GO", "py", "python"},
	}).(*Model)
	if len(m.rows) != 2 {
		t.Fatalf("canonicalised scope row count = %d, want 2", len(m.rows))
	}
	if m.rows[0].Language != "go" || m.rows[1].Language != "python" {
		t.Errorf("canonicalised rows = %q, %q; want go, python", m.rows[0].Language, m.rows[1].Language)
	}
}

// --- helpers ---

func setEmptyCacheDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CACHE_DIR", dir+"/cache")
}

func mustSaveAgent(t *testing.T, lang la.Language) {
	t.Helper()
	err := la.SaveAgent(la.Agent{
		Language:    lang,
		Context:     "test brief body for " + lang,
		GeneratedAt: time.Now().UTC(),
		Provider:    "test",
		Model:       "test-model",
	})
	if err != nil {
		t.Fatalf("SaveAgent(%q): %v", lang, err)
	}
}

// Silence unused import when tea isn't referenced in test bodies.
var _ tea.Msg = cacheLoadedMsg{}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tui-langagents-test-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("APPR_AI_SAL_CACHE_DIR", dir+"/cache")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
