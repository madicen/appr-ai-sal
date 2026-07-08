package gh

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// This file holds the low-level git plumbing the review runner's shared
// bare-repo cache (R7) is built on. The high-level orchestration — cache-path
// computation, per-repo locking, worktree reuse, and GC — lives in
// internal/review; here we only wrap the individual git invocations so they
// share the package's runPlain/runPlainIn helpers and error formatting.
//
// The PR head ref (refs/pull/<n>/head) lives on the *base* repository even
// when the PR originates from a fork, so fetching it into a bare mirror of
// the base repo always retrieves the actual head commit without needing the
// fork's clone URL.

// EnsureBareRepo makes sure a usable bare repository for ref exists at
// bareDir, creating it (git init --bare + an origin remote pointing at the
// base repo) when it is absent or corrupt. It performs no network I/O beyond
// the (offline) init; the actual objects are pulled by FetchPRHead. Idempotent:
// calling it on an already-valid bare repo is a no-op.
func EnsureBareRepo(ctx context.Context, ref Ref, bareDir string) error {
	if isBareRepo(ctx, bareDir) {
		return nil
	}
	// The path exists but git doesn't recognise it as a bare repo (partial
	// clone, truncated init, leftover directory). Remove it so we can start
	// clean rather than fighting a corrupt cache.
	if _, err := os.Stat(bareDir); err == nil {
		_ = os.RemoveAll(bareDir)
	}
	if err := os.MkdirAll(filepath.Dir(bareDir), 0o755); err != nil {
		return fmt.Errorf("create repos cache dir: %w", err)
	}
	if err := runPlain(ctx, "git", "init", "--bare", bareDir); err != nil {
		return fmt.Errorf("git init --bare: %w", err)
	}
	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", ref.Owner, ref.Repo)
	if err := runPlainIn(ctx, bareDir, "git", "remote", "add", "origin", cloneURL); err != nil {
		return fmt.Errorf("git remote add origin: %w", err)
	}
	return nil
}

// FetchPRHead fetches the PR's head commit into the bare repo under a
// namespaced local ref (refs/appr-ai-sal/pull/<n>/head) and returns the
// resolved head SHA. Because objects already present from a prior fetch (of
// this or any other PR of the same repo) are reused, repeat reviews transfer
// only the delta. The namespaced ref avoids relying on FETCH_HEAD, which a
// concurrent fetch could clobber.
func FetchPRHead(ctx context.Context, bareDir string, ref Ref) (string, error) {
	remoteRef := fmt.Sprintf("refs/pull/%d/head", ref.Number)
	localRef := fmt.Sprintf("refs/appr-ai-sal/pull/%d/head", ref.Number)
	spec := fmt.Sprintf("+%s:%s", remoteRef, localRef)
	if err := runPlainIn(ctx, bareDir, "git", "fetch", "--no-tags", "origin", spec); err != nil {
		return "", fmt.Errorf("git fetch %s: %w", remoteRef, err)
	}
	sha, err := RevParse(ctx, bareDir, localRef)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// AddWorktree adds a detached working tree at dir, checked out at sha, from
// the bare repo at bareDir. dir must not already be a registered worktree
// (callers prune first). The checkout matches what a fresh full clone of the
// PR head would contain, so specialists see the PR head exactly as before.
func AddWorktree(ctx context.Context, bareDir, dir, sha string) error {
	if err := runPlainIn(ctx, bareDir, "git", "worktree", "add", "--detach", dir, sha); err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}
	return nil
}

// PruneWorktrees runs `git worktree prune` against bareDir so git forgets
// bookkeeping for worktree directories that were removed out from under it
// (e.g. by the age/keep-per-PR GC). Best-effort: keeps the bare repo's
// worktree list from accumulating stale entries and refs.
func PruneWorktrees(ctx context.Context, bareDir string) error {
	if err := runPlainIn(ctx, bareDir, "git", "worktree", "prune"); err != nil {
		return fmt.Errorf("git worktree prune: %w", err)
	}
	return nil
}

// RevParse resolves rev (a ref name or SHA) to a full object SHA in the git
// repository at dir. Used to resolve fetched refs and to validate a candidate
// worktree's checked-out HEAD.
func RevParse(ctx context.Context, dir, rev string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", rev)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse %s in %s: %s", rev, dir, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// WorktreeHeadSHA returns the checked-out HEAD SHA of the worktree at dir, or
// an error when dir is not a git working tree. Used to confirm a cached
// worktree is genuinely parked at the expected head SHA before reusing it.
func WorktreeHeadSHA(ctx context.Context, dir string) (string, error) {
	return RevParse(ctx, dir, "HEAD")
}

// isBareRepo reports whether dir is a directory git recognises as a bare
// repository. A false result (missing, not a dir, corrupt, or not bare)
// tells EnsureBareRepo to (re)create it.
func isBareRepo(ctx context.Context, dir string) bool {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return false
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-bare-repository")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}
