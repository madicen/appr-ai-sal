package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/madicen/appr-ai-sal/internal/appdirs"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// This file implements the R7 shared bare-repo cache that backs
// prepareWorktree (see runner.go). The strategy:
//
//   - Keep one bare mirror per owner/repo under <cache>/repos/<owner>-<repo>.git.
//   - On each run fetch only the PR head delta into that bare repo (repeat
//     reviews of the same repo — even different PRs — reuse objects, so only
//     the delta transfers).
//   - `git worktree add` a per-run tree checked out at the head SHA, which is
//     far cheaper than a fresh full clone.
//   - Reuse an existing worktree when the head SHA is unchanged.
//   - Guard every bare-repo mutation (fetch / worktree add / prune) with a
//     per-repo lock so concurrent runs sharing one bare repo can't corrupt it.
//   - Fail open: any error here bubbles up to prepareWorktree, which falls
//     back to a fresh full clone so a run never dies because of the cache.

// reposCacheDir is the directory holding the shared bare repos. It is a
// sibling of the worktrees dir under the default XDG layout (and under the
// APPR_AI_SAL_CACHE_DIR override, which names the worktrees dir directly).
func reposCacheDir() string {
	return appdirs.CacheSubdir(appdirs.ReposSubdir)
}

// bareRepoDir returns the on-disk path of the shared bare repo for ref's
// owner/repo, e.g. <cache>/repos/acme-widget.git.
func bareRepoDir(ref gh.Ref) string {
	return filepath.Join(reposCacheDir(), repoSlug(ref)+".git")
}

// repoSlug maps owner/repo to a single filesystem-safe directory component
// ("owner-repo"). Owner and repo may legitimately contain '-', '.' and '_';
// any other rune is replaced with '-' so the slug never escapes the cache
// dir or collides with a path separator. The mapping need not be reversible —
// it only has to be stable and unique per owner/repo.
func repoSlug(ref gh.Ref) string {
	return sanitizeSlugComponent(ref.Owner) + "-" + sanitizeSlugComponent(ref.Repo)
}

func sanitizeSlugComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "repo"
	}
	return b.String()
}

// worktreeDirName is the per-run worktree directory name. It keeps the
// historical "<owner>-<repo>-<number>-<unix>" layout so the age/keep-per-PR
// GC (purgeStaleWorktrees / splitWorktreeName) keeps working unchanged; the
// head SHA is recorded in the marker file for reuse instead of the name.
func worktreeDirName(ref gh.Ref) string {
	return fmt.Sprintf("%s-%s-%d-%d", ref.Owner, ref.Repo, ref.Number, time.Now().UnixNano())
}

// repoLocks serialises mutations of a single bare repo across this process's
// goroutines. Two runs for different PRs of the same repo share one bare repo,
// so their fetch / worktree-add / prune must not interleave. Keyed by the
// cleaned bare-repo path.
var (
	repoLocksMu sync.Mutex
	repoLocks   = map[string]*sync.Mutex{}
)

// lockRepo acquires the per-repo lock for bareDir and returns the unlock func.
func lockRepo(bareDir string) func() {
	key := filepath.Clean(bareDir)
	repoLocksMu.Lock()
	m := repoLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		repoLocks[key] = m
	}
	repoLocksMu.Unlock()
	m.Lock()
	return m.Unlock
}

// prepareWorktreeFromCache is the R7 strategy: ensure the shared bare repo,
// fetch the PR head delta, reuse an unchanged-SHA worktree if one exists, and
// otherwise `git worktree add` a fresh per-run tree. All bare-repo mutation is
// serialised by the per-repo lock. Any error returned here triggers the
// fresh-clone fallback in prepareWorktree.
func prepareWorktreeFromCache(ctx context.Context, ref gh.Ref, base string) (string, error) {
	bareDir := bareRepoDir(ref)
	unlock := lockRepo(bareDir)
	defer unlock()

	if err := gh.EnsureBareRepo(ctx, ref, bareDir); err != nil {
		return "", err
	}
	sha, err := gh.FetchPRHead(ctx, bareDir, ref)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", fmt.Errorf("resolved empty head sha for %s", ref.String())
	}

	// Reuse: if a worktree for this PR already sits at the same head SHA,
	// keep it (refresh its marker mtime so the GC treats it as recently used).
	if dir, ok := findReusableWorktree(ctx, base, ref, sha); ok {
		touchWorktreeMarker(dir)
		applog.Info("worktree reuse", "ref", ref.String(), "sha", shortSHA(sha), "dir", dir)
		return dir, nil
	}

	// Drop bookkeeping for any worktree dir the GC removed so a stale entry
	// can't block the add below, then add a fresh per-run tree.
	_ = gh.PruneWorktrees(ctx, bareDir)
	dir := filepath.Join(base, worktreeDirName(ref))
	if err := gh.AddWorktree(ctx, bareDir, dir, sha); err != nil {
		// Roll back a partial add so the fallback clone starts clean and git
		// doesn't keep a half-registered worktree.
		_ = os.RemoveAll(dir)
		_ = gh.PruneWorktrees(ctx, bareDir)
		return "", err
	}
	writeWorktreeMarker(dir, sha)
	applog.Info("worktree add", "ref", ref.String(), "sha", shortSHA(sha), "dir", dir, "bare", bareDir)
	return dir, nil
}

// findReusableWorktree looks for an existing marked worktree for ref whose
// recorded head SHA matches sha AND whose git HEAD is genuinely parked at
// that SHA. Returns the first match. The double check (marker + live git
// HEAD) means a marker that lies (e.g. a manually mangled tree) is skipped
// rather than trusted.
func findReusableWorktree(ctx context.Context, base string, ref gh.Ref, sha string) (string, bool) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", false
	}
	group := fmt.Sprintf("%s-%s-%d", ref.Owner, ref.Repo, ref.Number)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		g, _ := splitWorktreeName(e.Name())
		if g != group {
			continue
		}
		dir := filepath.Join(base, e.Name())
		if readWorktreeMarkerSHA(dir) != sha {
			continue
		}
		if head, herr := gh.WorktreeHeadSHA(ctx, dir); herr == nil && head == sha {
			return dir, true
		}
	}
	return "", false
}

// pruneBareRepoWorktrees runs `git worktree prune` against every bare repo in
// the repos cache so git forgets bookkeeping for worktree dirs the age/
// keep-per-PR GC removed. Best-effort and lock-guarded per repo so it can't
// race a concurrent add on the same bare repo. Never blocks a run.
func pruneBareRepoWorktrees(ctx context.Context) {
	reposDir := reposCacheDir()
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".git") {
			continue
		}
		bareDir := filepath.Join(reposDir, e.Name())
		unlock := lockRepo(bareDir)
		_ = gh.PruneWorktrees(ctx, bareDir)
		unlock()
	}
}

// writeWorktreeMarker drops the GC marker in dir, recording the checked-out
// head SHA as its content so a later run can reuse the tree when the SHA is
// unchanged (see findReusableWorktree). The GC keys off the marker's mtime,
// not its content, so an empty sha is harmless.
func writeWorktreeMarker(dir, sha string) {
	_ = os.WriteFile(filepath.Join(dir, worktreeMarkerName), []byte(strings.TrimSpace(sha)+"\n"), 0o644)
}

// touchWorktreeMarker refreshes the marker's mtime so a reused worktree is
// treated as recently used by the age-based GC.
func touchWorktreeMarker(dir string) {
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, worktreeMarkerName), now, now)
}

// readWorktreeMarkerSHA returns the head SHA recorded in dir's marker, or ""
// when the marker is missing/empty.
func readWorktreeMarkerSHA(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, worktreeMarkerName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// shortSHA trims a full SHA to a log-friendly prefix.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
