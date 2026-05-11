package langagents

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCanonicalAliases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"go", "go"},
		{"golang", "go"},
		{".go", "go"},
		{"main.go", "go"},
		{"py", "python"},
		{"python", "python"},
		{".py", "python"},
		{".pyi", "python"},
		{"ts", "typescript"},
		{"javascript", "typescript"},
		{".tsx", "typescript"},
		{"rust", "rust"},
		{".rs", "rust"},
		{"terraform", "hcl"},
		{".tf", "hcl"},
		{".tfvars", "hcl"},
		{"swift", "swift"},
		{".swift", "swift"},
		{"", ""},
		{"klingon", ""},
		{".xyz", ""},
	}
	for _, tc := range cases {
		if got := Canonical(tc.in); got != tc.want {
			t.Errorf("Canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLanguageForPath(t *testing.T) {
	if got := LanguageForPath("internal/foo/bar.go"); got != "go" {
		t.Errorf("LanguageForPath(.go) = %q, want go", got)
	}
	if got := LanguageForPath("Main.kt"); got != "kotlin" {
		t.Errorf("LanguageForPath(.kt) = %q, want kotlin", got)
	}
	if got := LanguageForPath("README"); got != "" {
		t.Errorf("LanguageForPath(no ext) = %q, want empty", got)
	}
}

func TestAllKnownLanguagesIsAlphabetical(t *testing.T) {
	ls := AllKnownLanguages()
	if len(ls) == 0 {
		t.Fatal("AllKnownLanguages returned empty list")
	}
	for i := 1; i < len(ls); i++ {
		if ls[i-1] > ls[i] {
			t.Errorf("AllKnownLanguages not sorted: %q > %q", ls[i-1], ls[i])
		}
	}
}

func TestLoadReturnsFalseWhenCacheEmpty(t *testing.T) {
	setEmptyCacheDir(t)
	_, ok := Load("go")
	if ok {
		t.Fatal("Load(go) on empty cache should return ok=false")
	}
}

func TestSaveAndLoadCacheRoundTrip(t *testing.T) {
	setEmptyCacheDir(t)
	a := Agent{
		Language:    "go",
		Context:     "## Naming\n\nGo uses MixedCaps.",
		GeneratedAt: time.Now().UTC(),
		Provider:    "test",
		Model:       "test-model",
	}
	if err := SaveAgent(a); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	got, ok := Load("go")
	if !ok {
		t.Fatal("Load(go) after save returned !ok")
	}
	if got.Context != a.Context {
		t.Errorf("context round-trip mismatch")
	}
}

func TestBriefsForDiffEmptyCacheReportsAllMissing(t *testing.T) {
	setEmptyCacheDir(t)
	touches := map[string]int{
		"main.go":   200,
		"helper.py": 40,
	}
	briefs, missing := BriefsForDiff(touches)
	if len(briefs) != 0 {
		t.Errorf("want 0 briefs on empty cache, got %d", len(briefs))
	}
	// Both languages should be reported missing (Go first by touches).
	if len(missing) != 2 || missing[0] != "go" || missing[1] != "python" {
		t.Errorf("missing = %v, want [go python]", missing)
	}
}

func TestBriefsForDiffPicksDominantCachedLanguage(t *testing.T) {
	setEmptyCacheDir(t)
	mustSave(t, "go", "Go brief body")
	mustSave(t, "python", "Python brief body")

	touches := map[string]int{
		"main.go":   200,
		"helper.py": 3, // below MinTouchesToInject — should not displace
	}
	briefs, missing := BriefsForDiff(touches)
	if len(missing) != 0 {
		t.Errorf("missing should be empty, got %v", missing)
	}
	if len(briefs) != 1 {
		t.Fatalf("want 1 brief (python too small to inject), got %d", len(briefs))
	}
	if briefs[0].Language != "go" {
		t.Errorf("dominant language = %q, want go", briefs[0].Language)
	}
}

func TestBriefsForDiffIncludesSecondaryAboveMinTouches(t *testing.T) {
	setEmptyCacheDir(t)
	mustSave(t, "go", "Go brief")
	mustSave(t, "python", "Python brief")

	touches := map[string]int{
		"a.go": 100,
		"b.py": 40,
	}
	briefs, _ := BriefsForDiff(touches)
	if len(briefs) != 2 {
		t.Fatalf("want 2 briefs (both above threshold), got %d", len(briefs))
	}
	if briefs[0].Language != "go" || briefs[1].Language != "python" {
		t.Errorf("order = %q, %q; want go, python", briefs[0].Language, briefs[1].Language)
	}
}

func TestBriefsForDiffReportsMissingWhenOnlySomeCached(t *testing.T) {
	setEmptyCacheDir(t)
	mustSave(t, "go", "Go brief")

	touches := map[string]int{
		"src/main.swift": 80,
		"src/main.go":    20,
	}
	briefs, missing := BriefsForDiff(touches)
	if len(briefs) != 1 || briefs[0].Language != "go" {
		t.Errorf("brief slot should be Go, got %+v", briefs)
	}
	if len(missing) != 1 || missing[0] != "swift" {
		t.Errorf("missing = %v, want [swift]", missing)
	}
}

func TestFormatBriefsSectionStripsBriefH1(t *testing.T) {
	briefs := []Brief{{
		Language: "go",
		Body:     "# Language brief: Go\n\n## Naming\n\nGo uses MixedCaps.",
		Touches:  100,
	}}
	out := FormatBriefsSection(briefs)
	if strings.Contains(out, "# Language brief") {
		t.Errorf("FormatBriefsSection should strip the brief's own H1; got:\n%s", out)
	}
	if !strings.Contains(out, "### Go") {
		t.Error("section should include '### Go' header for the brief")
	}
	if !strings.Contains(out, "MixedCaps") {
		t.Error("section should preserve brief body content")
	}
}

func TestComputeLanguageMissingWhenNoCache(t *testing.T) {
	if got := ComputeLanguage("go", nil, time.Now(), DefaultStaleAfter); got != FreshnessMissing {
		t.Errorf("freshness with nil cache = %s, want missing", got)
	}
}

func TestComputeLanguageFreshWhenRecent(t *testing.T) {
	cache := &LangAgents{Agents: map[Language]Agent{
		"go": {Language: "go", Context: "x", GeneratedAt: time.Now().Add(-time.Hour)},
	}}
	if got := ComputeLanguage("go", cache, time.Now(), DefaultStaleAfter); got != FreshnessFresh {
		t.Errorf("recent brief = %s, want fresh", got)
	}
}

func TestComputeLanguageStaleByAge(t *testing.T) {
	cache := &LangAgents{Agents: map[Language]Agent{
		"go": {Language: "go", Context: "x", GeneratedAt: time.Now().Add(-90 * 24 * time.Hour)},
	}}
	if got := ComputeLanguage("go", cache, time.Now(), DefaultStaleAfter); got != FreshnessStale {
		t.Errorf("90-day-old go = %s, want stale", got)
	}
}

func TestComputePREmptyTouchedIsFresh(t *testing.T) {
	// PR touches no languages we recognise (e.g. a docs-only PR with
	// no .md handler in the extension map). There's nothing to warn
	// about, so the aggregator returns Fresh.
	if got := ComputePR([]Language{}, nil, time.Now(), DefaultStaleAfter); got != FreshnessFresh {
		t.Errorf("empty touched = %s, want fresh", got)
	}
}

func TestComputePRNilTouchedIsUnknown(t *testing.T) {
	if got := ComputePR(nil, nil, time.Now(), DefaultStaleAfter); got != FreshnessUnknown {
		t.Errorf("nil touched = %s, want unknown", got)
	}
}

func TestComputePRReturnsMissingWhenAnyLanguageMissing(t *testing.T) {
	cache := &LangAgents{Agents: map[Language]Agent{
		"go": {Language: "go", Context: "x", GeneratedAt: time.Now()},
	}}
	got := ComputePR([]Language{"go", "swift"}, cache, time.Now(), DefaultStaleAfter)
	if got != FreshnessMissing {
		t.Errorf("touched go+swift with only go cached = %s, want missing", got)
	}
}

func TestComputePRReturnsStaleWhenAllPresentButOneStale(t *testing.T) {
	cache := &LangAgents{Agents: map[Language]Agent{
		"go":     {Language: "go", Context: "x", GeneratedAt: time.Now()},
		"python": {Language: "python", Context: "y", GeneratedAt: time.Now().Add(-90 * 24 * time.Hour)},
	}}
	got := ComputePR([]Language{"go", "python"}, cache, time.Now(), DefaultStaleAfter)
	if got != FreshnessStale {
		t.Errorf("touched go+python with stale python = %s, want stale", got)
	}
}

func TestComputePRFreshWhenAllPresentAndFresh(t *testing.T) {
	cache := &LangAgents{Agents: map[Language]Agent{
		"go":     {Language: "go", Context: "x", GeneratedAt: time.Now()},
		"python": {Language: "python", Context: "y", GeneratedAt: time.Now()},
	}}
	got := ComputePR([]Language{"go", "python"}, cache, time.Now(), DefaultStaleAfter)
	if got != FreshnessFresh {
		t.Errorf("touched go+python both fresh = %s, want fresh", got)
	}
}

func TestComputeLanguageStaleOnZeroTimestamp(t *testing.T) {
	cache := &LangAgents{Agents: map[Language]Agent{
		"go": {Language: "go", Context: "x"},
	}}
	if got := ComputeLanguage("go", cache, time.Now(), DefaultStaleAfter); got != FreshnessStale {
		t.Errorf("zero-timestamp go = %s, want stale", got)
	}
}

func TestSummariseForDiffReportsTouchedAndMissing(t *testing.T) {
	setEmptyCacheDir(t)
	s := SummariseForDiff(map[string]int{
		"a.go":    100,
		"b.swift": 50,
		"c.tf":    10,
	})
	if got := len(s.Touched); got != 3 {
		t.Fatalf("Touched len = %d, want 3", got)
	}
	if s.Touched[0] != "go" {
		t.Errorf("dominant touched = %q, want go", s.Touched[0])
	}
	// All three are missing because cache is empty.
	if !s.HasMissing() {
		t.Error("expected at least one missing language")
	}
	if len(s.Missing) != 3 {
		t.Errorf("missing = %v, want all 3", s.Missing)
	}
}

func TestGeneratorPromptIsEmbedded(t *testing.T) {
	// Smoke check: loading the generator system prompt should never
	// fail in a built binary. Catches accidental damage to the
	// //go:embed directive or to the file path.
	p, err := loadGeneratorPrompt()
	if err != nil {
		t.Fatalf("loadGeneratorPrompt: %v", err)
	}
	if !strings.Contains(p, "language brief") {
		t.Errorf("generator prompt looks empty or wrong; got first 80 chars: %q", firstN(p, 80))
	}
}

func TestTableCanonicalNamesAreLowercased(t *testing.T) {
	// Table keys must be canonical language names so the convention
	// gate's lookup (which canonicalises the file extension first)
	// finds them. This guards against typo regressions in table.go.
	for lang := range Table {
		if Canonical(lang) != lang {
			t.Errorf("Table key %q is not canonical (Canonical = %q)", lang, Canonical(lang))
		}
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func setEmptyCacheDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPR_AI_SAL_CACHE_DIR", dir+"/cache")
}

func mustSave(t *testing.T, lang Language, body string) {
	t.Helper()
	err := SaveAgent(Agent{
		Language:    lang,
		Context:     body,
		GeneratedAt: time.Now().UTC(),
		Provider:    "test",
		Model:       "test-model",
	})
	if err != nil {
		t.Fatalf("SaveAgent(%q): %v", lang, err)
	}
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "langagents-test-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("APPR_AI_SAL_CACHE_DIR", dir+"/cache")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
