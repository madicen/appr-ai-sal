package review

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// TestRepoSlug covers the bare-repo directory slug computation, including the
// sanitisation of runes that could escape the cache dir.
func TestRepoSlug(t *testing.T) {
	cases := []struct {
		owner, repo, want string
	}{
		{"acme", "widget", "acme-widget"},
		{"my-org", "my.repo_2", "my-org-my.repo_2"},
		{"a/b", "c\\d", "a-b-c-d"}, // path separators sanitised away
		{"", "", "repo-repo"},      // empty components fall back to "repo"
	}
	for _, tc := range cases {
		got := repoSlug(gh.Ref{Owner: tc.owner, Repo: tc.repo})
		if got != tc.want {
			t.Errorf("repoSlug(%q,%q) = %q, want %q", tc.owner, tc.repo, got, tc.want)
		}
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("repoSlug(%q,%q) = %q leaks a path separator", tc.owner, tc.repo, got)
		}
	}
}

// TestBareRepoDir asserts the bare repo lives under a "repos" sibling of the
// worktrees dir named by the APPR_AI_SAL_CACHE_DIR override.
func TestBareRepoDir(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "worktrees")
	t.Setenv("APPR_AI_SAL_CACHE_DIR", wt)

	got := bareRepoDir(gh.Ref{Owner: "acme", Repo: "widget", Number: 7})
	want := filepath.Join(base, "repos", "acme-widget.git")
	if got != want {
		t.Errorf("bareRepoDir = %q, want %q", got, want)
	}
}

// TestLockRepo verifies the per-repo lock map returns one mutex per cleaned
// path and distinct mutexes for distinct repos.
func TestLockRepo(t *testing.T) {
	a1 := lockRepo("/tmp/repos/a.git")
	// A second lock for the SAME repo must block, so we don't try to acquire
	// it here; instead confirm a DIFFERENT repo is independent.
	b := lockRepo("/tmp/repos/b.git")
	b()
	a1()

	// After release, re-locking the same path (via a trailing-slash variant
	// that Clean collapses) must reuse the same mutex and succeed.
	a2 := lockRepo("/tmp/repos/./a.git")
	a2()
}

// TestWorktreeMarker covers the marker round-trip used for headSHA reuse.
func TestWorktreeMarker(t *testing.T) {
	dir := t.TempDir()
	if got := readWorktreeMarkerSHA(dir); got != "" {
		t.Errorf("missing marker should read empty, got %q", got)
	}
	writeWorktreeMarker(dir, "abc123")
	if got := readWorktreeMarkerSHA(dir); got != "abc123" {
		t.Errorf("marker sha = %q, want abc123", got)
	}
	// touch must not disturb the recorded content.
	touchWorktreeMarker(dir)
	if got := readWorktreeMarkerSHA(dir); got != "abc123" {
		t.Errorf("touch changed marker content: %q", got)
	}
}

// TestFindReusableWorktreeNoMatch covers the reuse decision's filtering
// without needing git: a marker SHA mismatch and a wrong PR group both skip
// the candidate before any git call is made.
func TestFindReusableWorktreeNoMatch(t *testing.T) {
	base := t.TempDir()
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 7}

	// Same PR group, different recorded SHA → not reusable.
	d1 := filepath.Join(base, "acme-widget-7-100")
	mustMkdir(t, d1)
	writeWorktreeMarker(d1, "deadbeef")

	// Different PR (number) → wrong group, skipped.
	d2 := filepath.Join(base, "acme-widget-8-200")
	mustMkdir(t, d2)
	writeWorktreeMarker(d2, "cafebabe")

	if dir, ok := findReusableWorktree(context.Background(), base, ref, "cafebabe"); ok {
		t.Errorf("expected no reusable worktree, got %q", dir)
	}
}

// TestPrepareWorktreeFailsOpen verifies prepareWorktree falls back to the
// fresh-clone strategy when the bare-repo cache strategy errors, and returns
// the cache result directly on success — the R7 fail-open contract — without
// touching the network.
func TestPrepareWorktreeFailsOpen(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CACHE_DIR", filepath.Join(t.TempDir(), "worktrees"))
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 7}

	origCache, origFresh := cacheWorktreeStrategy, freshCloneStrategy
	t.Cleanup(func() { cacheWorktreeStrategy, freshCloneStrategy = origCache, origFresh })

	// Cache path errors → fresh clone strategy is used.
	cacheWorktreeStrategy = func(context.Context, gh.Ref, string) (string, error) {
		return "", errors.New("boom")
	}
	freshCalled := false
	freshCloneStrategy = func(_ context.Context, _ gh.Ref, base string) (string, error) {
		freshCalled = true
		return filepath.Join(base, "fresh"), nil
	}
	got, err := prepareWorktree(context.Background(), ref)
	if err != nil {
		t.Fatalf("prepareWorktree returned error on fallback: %v", err)
	}
	if !freshCalled {
		t.Errorf("fresh-clone fallback was not invoked when cache errored")
	}
	if !strings.HasSuffix(got, "fresh") {
		t.Errorf("expected fallback worktree, got %q", got)
	}

	// Cache path succeeds → fresh clone strategy must NOT run.
	freshCalled = false
	cacheWorktreeStrategy = func(_ context.Context, _ gh.Ref, base string) (string, error) {
		return filepath.Join(base, "cached"), nil
	}
	got, err = prepareWorktree(context.Background(), ref)
	if err != nil {
		t.Fatalf("prepareWorktree cache-success error: %v", err)
	}
	if freshCalled {
		t.Errorf("fresh-clone strategy ran despite cache success")
	}
	if !strings.HasSuffix(got, "cached") {
		t.Errorf("expected cached worktree, got %q", got)
	}
}

// TestEnsureBareRepo exercises the offline bare-repo creation + idempotency
// (no network: it only inits a bare repo and adds a remote).
func TestEnsureBareRepo(t *testing.T) {
	requireGit(t)
	bare := filepath.Join(t.TempDir(), "repos", "acme-widget.git")
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 1}

	if err := gh.EnsureBareRepo(context.Background(), ref, bare); err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	// The remote URL should point at the base repo.
	out := runGit(t, bare, "remote", "get-url", "origin")
	if !strings.Contains(out, "acme/widget") {
		t.Errorf("origin url = %q, want it to contain acme/widget", out)
	}
	// Idempotent: a second call on the valid bare repo is a no-op success.
	if err := gh.EnsureBareRepo(context.Background(), ref, bare); err != nil {
		t.Fatalf("EnsureBareRepo (2nd) : %v", err)
	}
}

// TestWorktreeAddReuseAndPrune is the end-to-end git integration test for the
// R7 worktree machinery, run against a local temp repo (no network). It
// covers: worktree add checks out the right SHA, findReusableWorktree reuses
// an unchanged-SHA tree, and PruneWorktrees cleans up git bookkeeping after
// the dir is removed (the GC/prune interaction).
func TestWorktreeAddReuseAndPrune(t *testing.T) {
	requireGit(t)
	root := t.TempDir()

	// A source repo with one commit.
	src := filepath.Join(root, "src")
	mustMkdir(t, src)
	runGit(t, src, "init", "-q")
	runGit(t, src, "config", "user.email", "t@example.com")
	runGit(t, src, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "init")
	sha := strings.TrimSpace(runGit(t, src, "rev-parse", "HEAD"))

	// A bare mirror of the source, standing in for the shared cache.
	bare := filepath.Join(root, "repos", "acme-widget.git")
	mustMkdir(t, filepath.Dir(bare))
	runGit(t, root, "clone", "--bare", "-q", src, bare)

	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 7}
	base := filepath.Join(root, "worktrees")
	mustMkdir(t, base)
	dir := filepath.Join(base, worktreeDirName(ref))

	// Add a detached worktree at the SHA.
	if err := gh.AddWorktree(context.Background(), bare, dir, sha); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "file.txt")); err != nil {
		t.Errorf("worktree missing checked-out file: %v", err)
	}
	head, err := gh.WorktreeHeadSHA(context.Background(), dir)
	if err != nil || head != sha {
		t.Fatalf("WorktreeHeadSHA = %q,%v want %q", head, err, sha)
	}
	writeWorktreeMarker(dir, sha)

	// Reuse: an unchanged-SHA tree is found and returned.
	got, ok := findReusableWorktree(context.Background(), base, ref, sha)
	if !ok || got != dir {
		t.Fatalf("findReusableWorktree = %q,%v want %q,true", got, ok, dir)
	}
	// A different SHA must not reuse it.
	if _, ok := findReusableWorktree(context.Background(), base, ref, "0000000000000000000000000000000000000000"); ok {
		t.Errorf("findReusableWorktree matched a different SHA")
	}

	// GC/prune interaction: remove the dir (as purgeStaleWorktrees would) and
	// confirm PruneWorktrees drops the stale bookkeeping.
	if list := runGit(t, bare, "worktree", "list"); !strings.Contains(list, dir) {
		t.Fatalf("worktree not registered before prune: %q", list)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := gh.PruneWorktrees(context.Background(), bare); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	if list := runGit(t, bare, "worktree", "list"); strings.Contains(list, dir) {
		t.Errorf("prune left stale worktree bookkeeping: %q", list)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping git-integration test in -short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping git-integration test")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}
