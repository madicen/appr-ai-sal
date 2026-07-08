package review

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/appdirs"
	"github.com/madicen/appr-ai-sal/internal/applog"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// draft_cache.go persists a completed review Draft keyed by
// (owner/repo#N, headSHA) so a later re-review of the SAME PR with NEW commits
// (B2, incremental re-review) can compute an interdiff and carry forward prior
// findings instead of re-reviewing the whole PR from scratch.
//
// The cache is deliberately lean and fully fail-open:
//   - Only the fields needed to compute an interdiff (the reviewed Diff) and to
//     carry findings forward (the per-specialist Findings) are stored. The
//     synthesis stages (arbiter / vibe-coach / witness) and all TUI-runtime
//     state (skip sets, demoted-hidden, memory suppression) are intentionally
//     NOT cached — they are regenerated on every run.
//   - A missing, unreadable, unparseable, or version-mismatched cache entry is
//     treated as "no cache": the runner falls back to a full review, which is
//     byte-identical to the pre-B2 behaviour (the first-review backward-compat
//     guarantee). Corruption can therefore never break a review.
//
// Location: <cache>/draft-cache/<owner>__<repo>__<N>__<sha>.json (a sibling of
// the worktrees dir, honouring APPR_AI_SAL_CACHE_DIR / XDG like every other
// cache — see internal/appdirs).

// draftCacheVersion is the on-disk schema version. Bump it whenever the cached
// shape changes in a way that would make an older document misleading to carry
// forward; Load ignores any document whose Version does not match (→ full
// review), so an old/newer cache is silently discarded rather than mis-read.
const draftCacheVersion = 1

// draftCacheSubdir is the cache subdirectory the per-PR draft documents live
// under (a sibling of the worktrees dir).
const draftCacheSubdir = "draft-cache"

// CachedDraft is the serialized, version-stamped subset of a Draft persisted
// between reviews of one PR. Only what B2 needs to re-review incrementally is
// stored (see the file comment).
type CachedDraft struct {
	// Version is the schema version (draftCacheVersion). A mismatch makes Load
	// discard the document so the runner does a full review.
	Version int `json:"version"`
	// SavedAt is when the document was written — used to pick the most-recent
	// prior draft when several earlier SHAs are cached.
	SavedAt time.Time `json:"saved_at"`
	// HeadSHA is the PR head commit the review ran against — half of the cache
	// key and the "reviewed at commit X" the interdiff is computed relative to.
	HeadSHA string `json:"head_sha"`
	// Ref identifies the PR (owner/repo#N) the draft belongs to.
	Ref gh.Ref `json:"ref"`
	// Repository is the "owner/name" string (diagnostic / display).
	Repository string `json:"repository,omitempty"`
	// Diff is the unified diff the review ran against. It is the snapshot the
	// interdiff is computed against — the Draft already carries the full diff,
	// so B2 needs no extra per-file post-image hash (see the report note).
	Diff string `json:"diff"`
	// Strictness is the review intensity the prior run used.
	Strictness aiconfig.ReviewStrictness `json:"strictness,omitempty"`
	// Specialists carries the prior run's per-agent findings. Only the JSON
	// finding fields survive (path/line/side/severity/comment/anchor_excerpt/…);
	// the diagnostic/TUI-only Finding fields are json:"-" and are not needed to
	// carry a finding forward. AnchorExcerpt is retained precisely so the Q6
	// excerpt-relocation can re-anchor a carried finding on the new diff.
	Specialists []SpecialistResult `json:"specialists"`
}

// DraftCache reads and writes CachedDraft documents under the draft-cache
// subdir. The zero value is not usable; construct with NewDraftCache.
type DraftCache struct {
	dir string
}

// NewDraftCache returns a DraftCache rooted at the app's draft-cache subdir
// (honouring APPR_AI_SAL_CACHE_DIR / XDG via appdirs).
func NewDraftCache() *DraftCache {
	return &DraftCache{dir: appdirs.CacheSubdir(draftCacheSubdir)}
}

// sanitizeSHA folds a commit SHA to a filesystem-safe token. SHAs are hex so
// this is normally a no-op; any stray character is replaced with '_' so the
// key can never escape the cache directory.
func sanitizeSHA(sha string) string {
	sha = strings.ToLower(strings.TrimSpace(sha))
	var b strings.Builder
	for _, r := range sha {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// prefixFor returns the "<owner>__<repo>__<N>__" filename prefix shared by
// every cached draft for a PR (across its various head SHAs).
func (c *DraftCache) prefixFor(ref gh.Ref) string {
	return fmt.Sprintf("%s__%d__", agentstore.Slug(ref.Owner, ref.Repo), ref.Number)
}

// fileNameFor returns the "<owner>__<repo>__<N>__<sha>.json" document name for
// one (PR, head SHA) pair.
func (c *DraftCache) fileNameFor(ref gh.Ref, sha string) string {
	return c.prefixFor(ref) + sanitizeSHA(sha) + ".json"
}

// pathFor returns the absolute document path for one (PR, head SHA) pair.
func (c *DraftCache) pathFor(ref gh.Ref, sha string) string {
	return filepath.Join(c.dir, c.fileNameFor(ref, sha))
}

// Save writes d as the cached draft for headSHA. It is a no-op (nil error)
// when there is nothing to key on (nil draft or empty SHA). The write is
// atomic (temp + rename via agentstore.WriteJSONAtomic).
func (c *DraftCache) Save(d *Draft, headSHA string) error {
	if d == nil || strings.TrimSpace(headSHA) == "" {
		return nil
	}
	cd := &CachedDraft{
		Version:     draftCacheVersion,
		SavedAt:     time.Now().UTC(),
		HeadSHA:     strings.TrimSpace(headSHA),
		Ref:         d.Ref,
		Strictness:  d.Strictness,
		Diff:        d.Diff,
		Specialists: d.Specialists,
	}
	if d.PR != nil {
		cd.Repository = d.PR.Repository
	}
	return agentstore.WriteJSONAtomic(c.pathFor(d.Ref, headSHA), cd)
}

// Load reads the cached draft for exactly (ref, headSHA). It returns
// (nil, false) for any reason the document cannot be trusted — absent,
// unreadable, unparseable, or a version mismatch — so the caller falls back to
// a full review. It never returns an error: fail-open is the contract.
func (c *DraftCache) Load(ref gh.Ref, headSHA string) (*CachedDraft, bool) {
	return c.loadFile(c.pathFor(ref, headSHA))
}

// loadFile reads and validates one cache document by path.
func (c *DraftCache) loadFile(path string) (*CachedDraft, bool) {
	var cd CachedDraft
	found, err := agentstore.ReadJSONFile(path, &cd)
	if err != nil {
		applog.Warn("draft cache: read failed (ignoring, full review)", "path", path, "err", err.Error())
		return nil, false
	}
	if !found {
		return nil, false
	}
	if cd.Version != draftCacheVersion {
		applog.Info("draft cache: version mismatch (ignoring, full review)",
			"path", path, "have", cd.Version, "want", draftCacheVersion)
		return nil, false
	}
	return &cd, true
}

// LoadPrior returns the most-recent cached draft for ref that was reviewed
// against a head SHA DIFFERENT from currentSHA (i.e. the prior review to
// re-review incrementally against). It returns (nil, false) when no such
// document exists. Corrupt/version-mismatched documents are skipped, so a bad
// file never hides a good older one.
func (c *DraftCache) LoadPrior(ref gh.Ref, currentSHA string) (*CachedDraft, bool) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return nil, false // absent dir → first review of any PR
	}
	prefix := c.prefixFor(ref)
	currentName := c.fileNameFor(ref, currentSHA)
	var best *CachedDraft
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		// U2 session documents (…__<sha>.session.json) share the prefix and
		// end in ".json" but are NOT B2 draft documents — skip them so a
		// session file is never mis-parsed as a prior review (its shape would
		// unmarshal into a mostly-empty CachedDraft with a matching Version).
		if strings.HasSuffix(name, sessionFileSuffix) {
			continue
		}
		if name == currentName {
			continue // the in-progress head SHA, not a prior review
		}
		cd, ok := c.loadFile(filepath.Join(c.dir, name))
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(cd.HeadSHA), strings.TrimSpace(currentSHA)) {
			continue // same commit under a differently-cased/keyed name
		}
		if best == nil || cd.SavedAt.After(best.SavedAt) {
			best = cd
		}
	}
	if best == nil {
		return nil, false
	}
	return best, true
}

// PruneOtherSHAs deletes every cached draft for ref except the one for
// keepSHA, so the cache keeps at most one document per PR (the latest review).
// Fail-open: individual delete errors are ignored. Called after a successful
// Save so stale earlier-SHA drafts don't accumulate.
func (c *DraftCache) PruneOtherSHAs(ref gh.Ref, keepSHA string) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	prefix := c.prefixFor(ref)
	keepName := c.fileNameFor(ref, keepSHA)
	// The U2 session document for the kept SHA shares the prefix and ends in
	// ".json" too; keep it beside the kept draft (an in-progress approval for
	// the current head SHA must survive pruning). Session docs for OTHER SHAs
	// are stale and get pruned like their draft siblings.
	keepSessionName := c.sessionFileNameFor(ref, keepSHA)
	names := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		if name == keepName || name == keepSessionName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_ = os.Remove(filepath.Join(c.dir, name))
	}
}
