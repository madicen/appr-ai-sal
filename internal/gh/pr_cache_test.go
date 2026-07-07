package gh

import (
	"context"
	"testing"
)

// resetPRCacheForTest clears the process-wide cache and swaps in the given
// fake fetchers, restoring everything on cleanup. Same-package access keeps
// the cache internals testable without exporting them.
func resetPRCacheForTest(t *testing.T, fetch func(context.Context, Ref) (*PR, error), head func(context.Context, Ref) (string, error)) {
	t.Helper()
	prCacheMu.Lock()
	prevCache := prCache
	prCache = map[string]prCacheEntry{}
	prCacheMu.Unlock()
	prevFetch, prevHead := prFetch, prHeadSHAFetch
	prFetch = fetch
	prHeadSHAFetch = head
	t.Cleanup(func() {
		prCacheMu.Lock()
		prCache = prevCache
		prCacheMu.Unlock()
		prFetch = prevFetch
		prHeadSHAFetch = prevHead
	})
}

func TestGetPRCached_HitReusesWithoutRefetch(t *testing.T) {
	ref := Ref{Owner: "o", Repo: "r", Number: 7}
	var fullFetches, headFetches int
	resetPRCacheForTest(t,
		func(context.Context, Ref) (*PR, error) {
			fullFetches++
			return &PR{Number: 7, HeadSHA: "sha-1", Title: "first"}, nil
		},
		func(context.Context, Ref) (string, error) {
			headFetches++
			return "sha-1", nil // head unchanged
		},
	)

	// First call: cache miss → one full fetch, no head revalidation.
	pr, err := GetPRCached(context.Background(), ref)
	if err != nil {
		t.Fatalf("first GetPRCached: %v", err)
	}
	if pr.Title != "first" {
		t.Fatalf("unexpected PR: %+v", pr)
	}
	if fullFetches != 1 || headFetches != 0 {
		t.Fatalf("after first call: full=%d head=%d, want 1/0", fullFetches, headFetches)
	}

	// Second call: cache hit; head SHA unchanged → cheap revalidation only, no
	// full refetch.
	if _, err := GetPRCached(context.Background(), ref); err != nil {
		t.Fatalf("second GetPRCached: %v", err)
	}
	if fullFetches != 1 {
		t.Fatalf("cache hit should not refetch; full=%d, want 1", fullFetches)
	}
	if headFetches != 1 {
		t.Fatalf("cache hit should revalidate via head SHA once; head=%d, want 1", headFetches)
	}
}

func TestGetPRCached_HeadDriftTriggersRefetch(t *testing.T) {
	ref := Ref{Owner: "o", Repo: "r", Number: 8}
	var fullFetches int
	heads := []string{"sha-1", "sha-2"} // second revalidation reports a moved head
	resetPRCacheForTest(t,
		func(context.Context, Ref) (*PR, error) {
			fullFetches++
			if fullFetches == 1 {
				return &PR{Number: 8, HeadSHA: "sha-1", Title: "old"}, nil
			}
			return &PR{Number: 8, HeadSHA: "sha-2", Title: "new"}, nil
		},
		func(context.Context, Ref) (string, error) {
			return heads[len(heads)-1], nil // "sha-2": differs from cached "sha-1"
		},
	)

	if _, err := GetPRCached(context.Background(), ref); err != nil {
		t.Fatalf("first GetPRCached: %v", err)
	}
	pr, err := GetPRCached(context.Background(), ref)
	if err != nil {
		t.Fatalf("second GetPRCached: %v", err)
	}
	if fullFetches != 2 {
		t.Fatalf("head drift should force a refetch; full=%d, want 2", fullFetches)
	}
	if pr.Title != "new" || pr.HeadSHA != "sha-2" {
		t.Fatalf("expected refreshed PR after head drift, got %+v", pr)
	}
}

func TestInvalidatePRCache(t *testing.T) {
	ref := Ref{Owner: "o", Repo: "r", Number: 9}
	var fullFetches int
	resetPRCacheForTest(t,
		func(context.Context, Ref) (*PR, error) {
			fullFetches++
			return &PR{Number: 9, HeadSHA: "sha-1"}, nil
		},
		func(context.Context, Ref) (string, error) { return "sha-1", nil },
	)
	if _, err := GetPRCached(context.Background(), ref); err != nil {
		t.Fatalf("GetPRCached: %v", err)
	}
	InvalidatePRCache(ref)
	if _, err := GetPRCached(context.Background(), ref); err != nil {
		t.Fatalf("GetPRCached after invalidate: %v", err)
	}
	if fullFetches != 2 {
		t.Fatalf("invalidate should force a fresh full fetch; full=%d, want 2", fullFetches)
	}
}
