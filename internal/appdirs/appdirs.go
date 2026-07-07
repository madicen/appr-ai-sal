// Package appdirs is the single source of truth for appr-ai-sal's on-disk
// config and cache locations.
//
// Before F3 the XDG-style config-dir resolver was cloned verbatim five times
// (each agent subpackage plus review/prompts and the convention witness) and
// the cache-dir resolver three times (plus an inline copy in runner.go). This
// package collapses all of those into one implementation so relocating the
// tree (via APPR_AI_SAL_CONFIG_DIR / APPR_AI_SAL_CACHE_DIR / the XDG_* vars)
// behaves identically everywhere.
//
// It imports only the standard library so any package — including other leaf
// packages such as aiconfig — can depend on it without risking an import
// cycle.
package appdirs

import (
	"os"
	"path/filepath"
)

// appName is the per-user subdirectory the app owns under the XDG roots.
const appName = "appr-ai-sal"

// WorktreesSubdir is the cache subdir the review runner clones PR heads into.
// It is special-cased in CacheSubdir because the APPR_AI_SAL_CACHE_DIR
// override historically points *directly* at the worktrees directory (the
// other caches are resolved as its siblings). Demo mode and the CLI rely on
// this exact layout.
const WorktreesSubdir = "worktrees"

// ConfigDir returns the app's configuration directory. Precedence:
//
//  1. APPR_AI_SAL_CONFIG_DIR (used verbatim, enabling test/demo isolation)
//  2. XDG_CONFIG_HOME/appr-ai-sal
//  3. ~/.config/appr-ai-sal
//  4. ./.appr-ai-sal (last resort when the home dir is unknowable)
//
// This is the behaviour the five former configDir() clones and
// aiconfig.ConfigDir all implemented.
func ConfigDir() string {
	if v := os.Getenv("APPR_AI_SAL_CONFIG_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, appName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "." + appName
	}
	return filepath.Join(home, ".config", appName)
}

// CacheSubdir returns the app cache directory named sub (e.g. "repo-profiles",
// "lang-agents", "worktrees"). Precedence:
//
//  1. APPR_AI_SAL_CACHE_DIR — this override names the *worktrees* directory
//     itself, so sub=="worktrees" returns it unchanged and any other sub is
//     resolved as a sibling (Clean(join(v, "..", sub))).
//  2. XDG_CACHE_HOME/appr-ai-sal/<sub>
//  3. ~/.cache/appr-ai-sal/<sub>
//  4. ./.cache/appr-ai-sal/<sub> (last resort)
//
// This reproduces the former per-package CacheDir() clones and runner.go's
// inline cacheDir()/RepoProfilesDir() exactly.
func CacheSubdir(sub string) string {
	if v := os.Getenv("APPR_AI_SAL_CACHE_DIR"); v != "" {
		if sub == WorktreesSubdir {
			return v
		}
		return filepath.Clean(filepath.Join(v, "..", sub))
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, appName, sub)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", appName, sub)
	}
	return filepath.Join(home, ".cache", appName, sub)
}
