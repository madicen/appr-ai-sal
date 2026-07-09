package gh

import (
	"context"
	"testing"
)

// 0.4 fix #9: the authenticated viewer login is resolved once and cached for
// the session, so ViewerLogin returns the cached value without shelling out to
// `gh` again (the GraphQL ListPRs response already carries viewer{login}).
func TestViewerLoginUsesSessionCache(t *testing.T) {
	// Save and restore the process-wide cache so this test is hermetic.
	viewerLoginMu.Lock()
	prev := viewerLoginCache
	viewerLoginCache = ""
	viewerLoginMu.Unlock()
	t.Cleanup(func() {
		viewerLoginMu.Lock()
		viewerLoginCache = prev
		viewerLoginMu.Unlock()
	})

	// A blank login must never poison the cache.
	cacheViewerLogin("   ")
	if got := cachedViewerLogin(); got != "" {
		t.Fatalf("empty login must not be cached, got %q", got)
	}

	cacheViewerLogin("madicen")
	if got := cachedViewerLogin(); got != "madicen" {
		t.Fatalf("cachedViewerLogin = %q, want %q", got, "madicen")
	}

	// With a populated cache, ViewerLogin returns it without exec'ing gh.
	// (If it tried to run gh in this sandbox it would error, so a nil error
	// plus the cached value proves the cache short-circuits the call.)
	got, err := ViewerLogin(context.Background())
	if err != nil {
		t.Fatalf("ViewerLogin should not error when cache is warm: %v", err)
	}
	if got != "madicen" {
		t.Fatalf("ViewerLogin = %q, want cached %q", got, "madicen")
	}
}
