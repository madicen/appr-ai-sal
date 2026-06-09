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
