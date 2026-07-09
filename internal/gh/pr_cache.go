package gh

import (
	"context"
	"sync"
	"time"
)

// PR-data cache (R6.4).
//
// GetPR is a full `gh pr view` shell-out; the TUI re-fetches it whenever the
// user refreshes a PR, even when nothing changed. This process-lifetime cache
// keys a fetched PR by ref and validates it with the tuple (headSHA,
// updatedAt). Revalidation is cheap: a single GetPRHeadSHA call (a small REST
// GET) instead of the full view. When the head SHA is unchanged the cached PR
// is reused; a force-push (new head SHA) invalidates it and triggers a fresh
// fetch, so refreshing an already-current PR costs one lightweight request
// rather than a full detail fetch.

// prCacheEntry is one cached rich PR view plus the tuple it was valid for.
type prCacheEntry struct {
	pr        *PR
	headSHA   string
	updatedAt time.Time
}

var (
	prCacheMu sync.Mutex
	prCache   = map[string]prCacheEntry{}

	// Injectable fetchers so the cache logic is unit-testable without a live
	// gh / network: tests swap these to count calls and vary the head SHA.
	prFetch        = GetPR
	prHeadSHAFetch = GetPRHeadSHA
)

// GetPRCached returns the rich PR view for ref, reusing a cached copy when the
// PR's head SHA is unchanged since we last fetched it. On a cache miss (or when
// the cheap head-SHA revalidation shows the PR moved) it fetches fresh via
// GetPR and refreshes the cache entry. See the package comment above for the
// (headSHA, updatedAt) keying rationale.
func GetPRCached(ctx context.Context, ref Ref) (*PR, error) {
	key := ref.String()
	prCacheMu.Lock()
	ent, ok := prCache[key]
	prCacheMu.Unlock()

	if ok && ent.pr != nil {
		// Cheap revalidation: fetch only the head SHA. If it matches what the
		// cached view was built against, the view is still current — reuse it.
		// A lookup error is non-fatal: fall through to a full fetch.
		if sha, err := prHeadSHAFetch(ctx, ref); err == nil && sha != "" && sha == ent.headSHA {
			return ent.pr, nil
		}
	}

	pr, err := prFetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	prCacheMu.Lock()
	prCache[key] = prCacheEntry{pr: pr, headSHA: pr.HeadSHA, updatedAt: pr.UpdatedAt}
	prCacheMu.Unlock()
	return pr, nil
}

// InvalidatePRCache drops any cached view for ref. Callers that know the PR
// changed out-of-band (e.g. after posting a review) can force the next
// GetPRCached to refetch.
func InvalidatePRCache(ref Ref) {
	prCacheMu.Lock()
	delete(prCache, ref.String())
	prCacheMu.Unlock()
}
