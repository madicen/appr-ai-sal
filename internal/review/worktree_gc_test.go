package review

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkWorktree(t *testing.T, base, name string, markerAge time.Duration) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	marker := filepath.Join(dir, worktreeMarkerName)
	if err := os.WriteFile(marker, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	mtime := time.Now().Add(-markerAge)
	if err := os.Chtimes(marker, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return dir
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// 0.4 fix #8: purgeStaleWorktrees deletes marked worktrees older than
// worktreeKeepDays, keeps at most worktreeKeepPerPR of the newest per PR, and
// never touches a directory lacking the appr-ai-sal marker.
func TestPurgeStaleWorktrees(t *testing.T) {
	base := t.TempDir()

	// Old (beyond keep-days) marked worktree → purged.
	old := mkWorktree(t, base, "acme-widget-1-1000", (worktreeKeepDays+1)*24*time.Hour)

	// A user directory (no marker) must be left untouched even though it's old.
	userDir := filepath.Join(base, "not-ours")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user dir: %v", err)
	}

	// Three fresh worktrees for the same PR → only the newest
	// worktreeKeepPerPR survive; the oldest is purged by the per-PR cap.
	fresh1 := mkWorktree(t, base, "acme-repo-7-100", time.Hour)
	fresh2 := mkWorktree(t, base, "acme-repo-7-200", time.Hour)
	fresh3 := mkWorktree(t, base, "acme-repo-7-300", time.Hour)

	purgeStaleWorktrees(base)

	if exists(old) {
		t.Errorf("stale worktree older than keep-days should be purged")
	}
	if !exists(userDir) {
		t.Errorf("un-marked user directory must never be deleted")
	}
	// worktreeKeepPerPR is 2 → the two newest (fresh3, fresh2) survive.
	if !exists(fresh3) || !exists(fresh2) {
		t.Errorf("the %d newest per-PR worktrees must survive", worktreeKeepPerPR)
	}
	if exists(fresh1) {
		t.Errorf("oldest worktree beyond the per-PR cap should be purged")
	}
}

func TestSplitWorktreeName(t *testing.T) {
	cases := []struct {
		in    string
		group string
		unix  int64
	}{
		{"acme-widget-42-1700000000", "acme-widget-42", 1700000000},
		{"owner-with-hyphens-repo-7-99", "owner-with-hyphens-repo-7", 99},
		{"no-trailing-number-", "no-trailing-number-", 0},
		{"garbage", "garbage", 0},
	}
	for _, tc := range cases {
		g, u := splitWorktreeName(tc.in)
		if g != tc.group || u != tc.unix {
			t.Errorf("splitWorktreeName(%q) = (%q,%d), want (%q,%d)", tc.in, g, u, tc.group, tc.unix)
		}
	}
}
