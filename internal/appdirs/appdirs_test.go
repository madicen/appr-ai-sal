package appdirs

import (
	"path/filepath"
	"testing"
)

func TestConfigDirPrecedence(t *testing.T) {
	t.Run("APPR_AI_SAL_CONFIG_DIR wins verbatim", func(t *testing.T) {
		t.Setenv("APPR_AI_SAL_CONFIG_DIR", "/custom/cfg")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		if got := ConfigDir(); got != "/custom/cfg" {
			t.Fatalf("ConfigDir() = %q, want /custom/cfg", got)
		}
	})

	t.Run("falls back to XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("APPR_AI_SAL_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		want := filepath.Join("/xdg", appName)
		if got := ConfigDir(); got != want {
			t.Fatalf("ConfigDir() = %q, want %q", got, want)
		}
	})
}

func TestCacheSubdir(t *testing.T) {
	t.Run("override names the worktrees dir directly", func(t *testing.T) {
		t.Setenv("APPR_AI_SAL_CACHE_DIR", "/demo/cache")
		if got := CacheSubdir(WorktreesSubdir); got != "/demo/cache" {
			t.Fatalf("worktrees: got %q, want /demo/cache", got)
		}
		// Other caches are siblings of the override.
		if got := CacheSubdir("repo-profiles"); got != "/demo/repo-profiles" {
			t.Fatalf("repo-profiles: got %q, want /demo/repo-profiles", got)
		}
		if got := CacheSubdir("lang-agents"); got != "/demo/lang-agents" {
			t.Fatalf("lang-agents: got %q, want /demo/lang-agents", got)
		}
	})

	t.Run("falls back to XDG_CACHE_HOME", func(t *testing.T) {
		t.Setenv("APPR_AI_SAL_CACHE_DIR", "")
		t.Setenv("XDG_CACHE_HOME", "/xdgcache")
		want := filepath.Join("/xdgcache", appName, "repo-profiles")
		if got := CacheSubdir("repo-profiles"); got != want {
			t.Fatalf("repo-profiles: got %q, want %q", got, want)
		}
		wantWT := filepath.Join("/xdgcache", appName, "worktrees")
		if got := CacheSubdir(WorktreesSubdir); got != wantWT {
			t.Fatalf("worktrees: got %q, want %q", got, wantWT)
		}
	})
}
