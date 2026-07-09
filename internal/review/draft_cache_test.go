package review

import (
	"os"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// useTempDraftCache points the draft cache at a fresh temp dir for one test by
// overriding APPR_AI_SAL_CACHE_DIR (appdirs resolves the draft-cache subdir as
// a sibling of it).
func useTempDraftCache(t *testing.T) {
	t.Helper()
	t.Setenv("APPR_AI_SAL_CACHE_DIR", t.TempDir())
}

func sampleDraft() *Draft {
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 42}
	return &Draft{
		Ref:        ref,
		PR:         &gh.PR{Owner: "acme", Repo: "widget", Number: 42, Repository: "acme/widget", HeadSHA: "sha-old"},
		Diff:       "diff --git a/foo.go b/foo.go\n@@ -1,1 +1,1 @@\n-old\n+new\n",
		Strictness: aiconfig.ReviewBalanced,
		Specialists: []SpecialistResult{
			{
				Specialist: SpecSecurity,
				Summary:    "one finding",
				Findings: []Finding{
					{Path: "foo.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "watch out", AnchorExcerpt: "new"},
				},
			},
		},
	}
}

func TestDraftCacheRoundTrip(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	d := sampleDraft()

	if err := dc.Save(d, "sha-old"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok := dc.Load(d.Ref, "sha-old")
	if !ok {
		t.Fatalf("Load returned !ok after Save")
	}
	if got.Version != draftCacheVersion {
		t.Errorf("Version = %d, want %d", got.Version, draftCacheVersion)
	}
	if got.HeadSHA != "sha-old" {
		t.Errorf("HeadSHA = %q, want sha-old", got.HeadSHA)
	}
	if got.Diff != d.Diff {
		t.Errorf("Diff not round-tripped:\n got %q\nwant %q", got.Diff, d.Diff)
	}
	if len(got.Specialists) != 1 || len(got.Specialists[0].Findings) != 1 {
		t.Fatalf("Specialists not round-tripped: %+v", got.Specialists)
	}
	f := got.Specialists[0].Findings[0]
	if f.Path != "foo.go" || f.Line != 1 || f.AnchorExcerpt != "new" || f.Comment != "watch out" {
		t.Errorf("finding not round-tripped: %+v", f)
	}
}

func TestDraftCacheMissingIsFailOpen(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	if _, ok := dc.Load(gh.Ref{Owner: "acme", Repo: "widget", Number: 42}, "nope"); ok {
		t.Fatalf("Load of absent entry returned ok=true; want fail-open miss")
	}
}

func TestDraftCacheVersionMismatchFallsBack(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	d := sampleDraft()
	if err := dc.Save(d, "sha-old"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Rewrite the on-disk document with a bogus version, simulating a schema
	// bump / older writer. Load must ignore it → full review.
	path := dc.pathFor(d.Ref, "sha-old")
	var raw CachedDraft
	if _, err := agentstore.ReadJSONFile(path, &raw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	raw.Version = draftCacheVersion + 999
	if err := agentstore.WriteJSONAtomic(path, &raw); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, ok := dc.Load(d.Ref, "sha-old"); ok {
		t.Fatalf("Load of version-mismatched entry returned ok=true; want fallback")
	}
}

func TestDraftCacheCorruptFileFailsOpen(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 42}
	path := dc.pathFor(ref, "sha-x")
	if err := os.MkdirAll(dc.dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, ok := dc.Load(ref, "sha-x"); ok {
		t.Fatalf("Load of corrupt entry returned ok=true; want fail-open miss")
	}
}

func TestDraftCacheLoadPriorPicksDifferentSHA(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	d := sampleDraft()

	// Cache two prior reviews for this PR under different SHAs.
	if err := dc.Save(d, "sha-1"); err != nil {
		t.Fatalf("Save sha-1: %v", err)
	}
	d2 := sampleDraft()
	d2.Diff = "diff --git a/foo.go b/foo.go\n@@ -1,1 +1,2 @@\n new\n+more\n"
	if err := dc.Save(d2, "sha-2"); err != nil {
		t.Fatalf("Save sha-2: %v", err)
	}

	// A re-review at a third SHA should find the most-recent prior (sha-2).
	prior, ok := dc.LoadPrior(d.Ref, "sha-3")
	if !ok {
		t.Fatalf("LoadPrior !ok; want a prior review")
	}
	if prior.HeadSHA != "sha-2" {
		t.Errorf("LoadPrior HeadSHA = %q, want sha-2 (most recent)", prior.HeadSHA)
	}

	// LoadPrior for the same SHA as the only cache entry finds nothing (there
	// is no EARLIER review to diff against).
	dc2 := NewDraftCache()
	other := gh.Ref{Owner: "acme", Repo: "gadget", Number: 7}
	only := sampleDraft()
	only.Ref = other
	only.PR = &gh.PR{Owner: "acme", Repo: "gadget", Number: 7}
	if err := dc2.Save(only, "same"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := dc2.LoadPrior(other, "same"); ok {
		t.Fatalf("LoadPrior returned ok for the current SHA; want none")
	}
}

func TestDraftCacheLoadPriorAbsentDir(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	if _, ok := dc.LoadPrior(gh.Ref{Owner: "no", Repo: "one", Number: 1}, "sha"); ok {
		t.Fatalf("LoadPrior on an empty cache returned ok=true")
	}
}

func TestDraftCachePruneOtherSHAs(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	d := sampleDraft()
	if err := dc.Save(d, "sha-1"); err != nil {
		t.Fatalf("Save sha-1: %v", err)
	}
	if err := dc.Save(d, "sha-2"); err != nil {
		t.Fatalf("Save sha-2: %v", err)
	}
	dc.PruneOtherSHAs(d.Ref, "sha-2")
	if _, ok := dc.Load(d.Ref, "sha-1"); ok {
		t.Errorf("sha-1 draft survived prune")
	}
	if _, ok := dc.Load(d.Ref, "sha-2"); !ok {
		t.Errorf("sha-2 draft was pruned but should be kept")
	}
}
