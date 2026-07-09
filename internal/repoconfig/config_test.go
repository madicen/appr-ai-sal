package repoconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseRepoRootsLines(t *testing.T) {
	t.Parallel()
	got, err := ParseRepoRootsLines(`
# comment
owner/foo=/abs/a
OWNER/bar = /abs/b
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"owner/foo": "/abs/a",
		"owner/bar": "/abs/b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestParseRepoRootsLinesErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseRepoRootsLines("badline"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiffBudgetKnobsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo-context.json")
	cfg := Default()
	cfg.DiffElisionGlobs = []string{"*.snap", "generated/"}
	cfg.DiffByteCap = 123456
	cfg.DiffPerFileLineCap = 321
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.DiffElisionGlobs, []string{"*.snap", "generated/"}) {
		t.Errorf("globs = %#v, want [*.snap generated/]", got.DiffElisionGlobs)
	}
	if got.DiffByteCap != 123456 {
		t.Errorf("byte cap = %d, want 123456", got.DiffByteCap)
	}
	if got.DiffPerFileLineCap != 321 {
		t.Errorf("per-file cap = %d, want 321", got.DiffPerFileLineCap)
	}
}

func TestDiffElisionGlobsOrDefault(t *testing.T) {
	// Unset → baked-in defaults (non-empty, includes go.sum).
	c := Default()
	def := c.DiffElisionGlobsOrDefault()
	if len(def) == 0 {
		t.Fatal("default globs should be non-empty")
	}
	found := false
	for _, g := range def {
		if g == "go.sum" {
			found = true
		}
	}
	if !found {
		t.Errorf("default globs should include go.sum, got %v", def)
	}
	// Set → the override, verbatim.
	c.DiffElisionGlobs = []string{"only.this"}
	if got := c.DiffElisionGlobsOrDefault(); len(got) != 1 || got[0] != "only.this" {
		t.Errorf("override globs = %v, want [only.this]", got)
	}
	// Negative caps normalize to 0 (= use default), never negative/unbounded.
	c.DiffByteCap = -5
	c.DiffPerFileLineCap = -9
	c.Normalize()
	if c.DiffByteCap != 0 || c.DiffPerFileLineCap != 0 {
		t.Errorf("negative caps should normalize to 0, got byte=%d line=%d", c.DiffByteCap, c.DiffPerFileLineCap)
	}
}

func TestApplyParallelExecutionEnv(t *testing.T) {
	c := Default()
	t.Setenv("APPR_AI_SAL_PARALLEL_SPECIALISTS", "true")
	t.Setenv("APPR_AI_SAL_PARALLEL_REPO_EXPERTS", "1")
	t.Setenv("APPR_AI_SAL_PARALLEL_PR_AGENTS", "yes")
	ApplyParallelExecutionEnv(c)
	if !c.ParallelSpecialists || !c.ParallelRepoExperts || !c.ParallelPRAgents {
		t.Fatalf("got specialists=%v repoExperts=%v prAgents=%v", c.ParallelSpecialists, c.ParallelRepoExperts, c.ParallelPRAgents)
	}
	t.Setenv("APPR_AI_SAL_PARALLEL_SPECIALISTS", "0")
	t.Setenv("APPR_AI_SAL_PARALLEL_REPO_EXPERTS", "false")
	t.Setenv("APPR_AI_SAL_PARALLEL_PR_AGENTS", "off")
	ApplyParallelExecutionEnv(c)
	if c.ParallelSpecialists || c.ParallelRepoExperts || c.ParallelPRAgents {
		t.Fatalf("expected all false after falsy env, got specialists=%v repoExperts=%v prAgents=%v",
			c.ParallelSpecialists, c.ParallelRepoExperts, c.ParallelPRAgents)
	}
}

func TestLoadParallelFlagsFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo-context.json")
	if err := os.WriteFile(path, []byte(`{"parallel_specialists":true,"parallel_repo_experts":true,"parallel_pr_agents":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.ParallelSpecialists || !c.ParallelRepoExperts || !c.ParallelPRAgents {
		t.Fatalf("got %+v", c)
	}
}

// 0.4 fix #12: bool-presence detection via *bool must distinguish an omitted
// key (keep the Default() value, e.g. tech_agents defaults true) from an
// explicit false (honour it), without the fragile bytes.Contains raw scans.
func TestLoadBoolPresenceExplicitFalseVsAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo-context.json")
	// tech_agents defaults true; set it explicitly false. convention_witness
	// (also default true) is omitted and must stay true.
	if err := os.WriteFile(path, []byte(`{"tech_agents":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.TechAgents {
		t.Errorf("explicit tech_agents:false must be honoured, got true")
	}
	if !c.ConventionWitness {
		t.Errorf("omitted convention_witness must keep its default (true), got false")
	}
	if !c.PRAgents {
		t.Errorf("omitted pr_agents must keep its default (true), got false")
	}
}

// A false value must not be defeated even when the same token appears as a
// substring inside an unrelated string value elsewhere in the file — the old
// bytes.Contains scan could be fooled by that.
func TestLoadBoolPresenceIgnoresKeyInStringValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo-context.json")
	// repo_roots value mentions "pr_agents" as a path substring; pr_agents
	// itself is set false and must be honoured.
	body := `{"pr_agents":false,"repo_roots":{"acme/pr_agents":"/tmp/pr_agents"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PRAgents {
		t.Errorf("explicit pr_agents:false must be honoured despite substring noise")
	}
}

// R2: the parallel defaults were flipped to true (the concurrency cap now
// makes parallel dispatch safe). An absent key must resolve to true, while an
// explicit false in JSON must still be honoured.
func TestLoadParallelDefaultsFlippedToTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo-context.json")
	// An empty object: neither parallel_specialists nor parallel_pr_agents is
	// present, so both must take the new default of true.
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	// Clear any env overrides that could mask the default.
	t.Setenv("APPR_AI_SAL_PARALLEL_SPECIALISTS", "")
	os.Unsetenv("APPR_AI_SAL_PARALLEL_SPECIALISTS")
	t.Setenv("APPR_AI_SAL_PARALLEL_PR_AGENTS", "")
	os.Unsetenv("APPR_AI_SAL_PARALLEL_PR_AGENTS")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.ParallelSpecialists {
		t.Errorf("absent parallel_specialists must default to true")
	}
	if !c.ParallelPRAgents {
		t.Errorf("absent parallel_pr_agents must default to true")
	}
	// The Default() constructor must agree with the loaded default.
	d := Default()
	if !d.ParallelSpecialists || !d.ParallelPRAgents {
		t.Errorf("Default() must set both parallel toggles true, got specialists=%v prAgents=%v",
			d.ParallelSpecialists, d.ParallelPRAgents)
	}
}

// An explicit false for the now-default-true parallel toggles must still be
// honoured (the *bool presence loader distinguishes absent from explicit).
func TestLoadParallelExplicitFalseHonoured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo-context.json")
	if err := os.WriteFile(path, []byte(`{"parallel_specialists":false,"parallel_pr_agents":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	os.Unsetenv("APPR_AI_SAL_PARALLEL_SPECIALISTS")
	os.Unsetenv("APPR_AI_SAL_PARALLEL_PR_AGENTS")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ParallelSpecialists {
		t.Errorf("explicit parallel_specialists:false must be honoured, got true")
	}
	if c.ParallelPRAgents {
		t.Errorf("explicit parallel_pr_agents:false must be honoured, got true")
	}
}

// R2: max_concurrent_inference defaults to 3 when unset, and any <= 0 value
// resolves to 3 (never unlimited, never zero which would deadlock).
func TestMaxConcurrentInferenceDefaultResolution(t *testing.T) {
	t.Parallel()
	if got := Default().MaxConcurrentInference; got != 3 {
		t.Errorf("Default().MaxConcurrentInference = %d, want 3", got)
	}
	cases := []struct {
		in   int
		want int
	}{
		{0, 3},
		{-1, 3},
		{-100, 3},
		{1, 1},
		{3, 3},
		{10, 10},
	}
	for _, tc := range cases {
		// Getter resolves defensively.
		c := &Config{MaxConcurrentInference: tc.in}
		if got := c.MaxConcurrentInferenceOrDefault(); got != tc.want {
			t.Errorf("MaxConcurrentInferenceOrDefault(%d) = %d, want %d", tc.in, got, tc.want)
		}
		// Normalize rewrites <= 0 to the default in place.
		n := &Config{MaxConcurrentInference: tc.in}
		n.Normalize()
		if n.MaxConcurrentInference != tc.want {
			t.Errorf("Normalize() with in=%d -> %d, want %d", tc.in, n.MaxConcurrentInference, tc.want)
		}
	}
	// A nil config resolves to the default without panicking.
	var nilCfg *Config
	if got := nilCfg.MaxConcurrentInferenceOrDefault(); got != 3 {
		t.Errorf("nil config MaxConcurrentInferenceOrDefault() = %d, want 3", got)
	}
}

// max_concurrent_inference set explicitly in JSON is honoured after Load.
func TestLoadMaxConcurrentInferenceFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo-context.json")
	if err := os.WriteFile(path, []byte(`{"max_concurrent_inference":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxConcurrentInference != 7 {
		t.Errorf("explicit max_concurrent_inference:7 not honoured, got %d", c.MaxConcurrentInference)
	}
}

func TestFormatParseRepoRootsRoundTrip(t *testing.T) {
	t.Parallel()
	in := map[string]string{"z/a": "/p1", "a/b": "/p2"}
	s := FormatRepoRootsLines(in)
	got, err := ParseRepoRootsLines(s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %#v want %#v", got, in)
	}
}
