package review

import (
	"os"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// sampleSession builds a SessionState around sampleDraft (defined in
// draft_cache_test.go) with a couple of card decisions and a cursor.
func sampleSession() *SessionState {
	d := sampleDraft()
	return &SessionState{
		HeadSHA:   "sha-old",
		Ref:       d.Ref,
		Draft:     d.SessionSnapshot(),
		Cursor:    3,
		ActiveTab: 2,
		CoachHash: "hash-xyz",
		Decisions: []CardDecision{
			{Key: "k1", Decision: "posted"},
			{Key: "k2", Decision: "skipped"},
			{Key: "memory:0:k3", Decision: "pending", Resurfaced: false},
		},
	}
}

func TestSessionCacheRoundTrip(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	s := sampleSession()

	if err := dc.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, ok := dc.LoadSession(s.Ref, "sha-old")
	if !ok {
		t.Fatalf("LoadSession !ok after SaveSession")
	}
	if got.Version != sessionCacheVersion {
		t.Errorf("Version = %d, want %d", got.Version, sessionCacheVersion)
	}
	if got.Cursor != 3 || got.ActiveTab != 2 || got.CoachHash != "hash-xyz" {
		t.Errorf("scalar fields not round-tripped: cursor=%d tab=%d coach=%q", got.Cursor, got.ActiveTab, got.CoachHash)
	}
	if len(got.Decisions) != 3 {
		t.Fatalf("decisions len = %d, want 3", len(got.Decisions))
	}
	if got.Decisions[1].Key != "k2" || got.Decisions[1].Decision != "skipped" {
		t.Errorf("decision not round-tripped: %+v", got.Decisions[1])
	}
	if got.Draft == nil || got.Draft.Diff != s.Draft.Diff {
		t.Errorf("draft snapshot not round-tripped")
	}
}

// A session is keyed by (ref, headSHA). Loading against a DIFFERENT SHA must
// miss — this is the U2 invalidation guarantee: a session captured against an
// old commit is never resumed onto new code.
func TestSessionCacheSHAMismatchInvalidates(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	s := sampleSession()
	if err := dc.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, ok := dc.LoadSession(s.Ref, "sha-NEW"); ok {
		t.Fatalf("LoadSession for a different head SHA returned ok=true; want miss (stale)")
	}
	// The exact-SHA lookup still finds it.
	if _, ok := dc.LoadSession(s.Ref, "sha-old"); !ok {
		t.Fatalf("LoadSession for the matching head SHA missed")
	}
}

func TestSessionCacheMissingIsFailOpen(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	if _, ok := dc.LoadSession(gh.Ref{Owner: "acme", Repo: "widget", Number: 42}, "nope"); ok {
		t.Fatalf("LoadSession of absent entry returned ok=true; want fail-open miss")
	}
}

func TestSessionCacheCorruptFailsOpen(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 42}
	path := dc.sessionPathFor(ref, "sha-x")
	if err := os.MkdirAll(dc.dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, ok := dc.LoadSession(ref, "sha-x"); ok {
		t.Fatalf("LoadSession of corrupt entry returned ok=true; want fail-open miss")
	}
}

func TestSessionCacheVersionMismatchFallsBack(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	s := sampleSession()
	if err := dc.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	path := dc.sessionPathFor(s.Ref, "sha-old")
	var raw SessionState
	if _, err := agentstore.ReadJSONFile(path, &raw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	raw.Version = sessionCacheVersion + 999
	if err := agentstore.WriteJSONAtomic(path, &raw); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, ok := dc.LoadSession(s.Ref, "sha-old"); ok {
		t.Fatalf("LoadSession of version-mismatched entry returned ok=true; want fallback")
	}
}

func TestSessionCacheClear(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	s := sampleSession()
	if err := dc.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	dc.ClearSession(s.Ref, "sha-old")
	if _, ok := dc.LoadSession(s.Ref, "sha-old"); ok {
		t.Fatalf("LoadSession after ClearSession returned ok=true; want gone")
	}
}

// The B2 draft cache and the U2 session file share the (ref, headSHA) key but
// live in sibling files. Saving both, then LoadPrior at a new SHA, must return
// the DRAFT document — never the session (which would mis-parse into a mostly
// empty CachedDraft with a matching Version).
func TestSessionCoexistsWithDraftCacheLoadPrior(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	d := sampleDraft()
	if err := dc.Save(d, "sha-1"); err != nil {
		t.Fatalf("Save draft: %v", err)
	}
	s := sampleSession()
	s.HeadSHA = "sha-1"
	if err := dc.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	prior, ok := dc.LoadPrior(d.Ref, "sha-2")
	if !ok {
		t.Fatalf("LoadPrior !ok; want the draft at sha-1")
	}
	if prior.HeadSHA != "sha-1" {
		t.Errorf("LoadPrior HeadSHA = %q, want sha-1", prior.HeadSHA)
	}
	if prior.Diff == "" {
		t.Errorf("LoadPrior returned an empty-diff document — a session file was mis-parsed as a prior draft")
	}
}

// PruneOtherSHAs keeps the current SHA's draft AND its session, and removes both
// documents for every other SHA.
func TestSessionPruneOtherSHAsKeepsCurrentSession(t *testing.T) {
	useTempDraftCache(t)
	dc := NewDraftCache()
	d := sampleDraft()
	if err := dc.Save(d, "sha-1"); err != nil {
		t.Fatalf("Save sha-1: %v", err)
	}
	if err := dc.Save(d, "sha-2"); err != nil {
		t.Fatalf("Save sha-2: %v", err)
	}
	s1 := sampleSession()
	s1.HeadSHA = "sha-1"
	if err := dc.SaveSession(s1); err != nil {
		t.Fatalf("SaveSession sha-1: %v", err)
	}
	s2 := sampleSession()
	s2.HeadSHA = "sha-2"
	if err := dc.SaveSession(s2); err != nil {
		t.Fatalf("SaveSession sha-2: %v", err)
	}

	dc.PruneOtherSHAs(d.Ref, "sha-2")

	if _, ok := dc.Load(d.Ref, "sha-1"); ok {
		t.Errorf("sha-1 draft survived prune")
	}
	if _, ok := dc.LoadSession(d.Ref, "sha-1"); ok {
		t.Errorf("sha-1 session survived prune")
	}
	if _, ok := dc.Load(d.Ref, "sha-2"); !ok {
		t.Errorf("sha-2 draft was pruned but should be kept")
	}
	if _, ok := dc.LoadSession(d.Ref, "sha-2"); !ok {
		t.Errorf("sha-2 session was pruned but should be kept (in-progress approval)")
	}
}

// A snapshot of a finalized draft (arbiter suppressions applied) must restore
// with the arbiter's unexported key sets rebuilt, so the suppressed finding is
// still filtered from the postable set after resume — with no LLM re-run.
func TestSessionDraftRoundTripRebuildsArbiterKeySets(t *testing.T) {
	d := &Draft{
		Ref:  gh.Ref{Owner: "acme", Repo: "widget", Number: 1},
		PR:   &gh.PR{Owner: "acme", Repo: "widget", Number: 1, HeadSHA: "sha"},
		Diff: "diff --git a/foo.go b/foo.go\n@@ -1,1 +1,1 @@\n-old\n+new\n",
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Findings: []Finding{
				{Path: "foo.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "nit"},
			}},
		},
		RepoArbiter: &RepoArbiterResult{
			EffectiveVerdict: VibeVerdictComment,
			Suppressed: []SuppressedFindingRef{
				{Specialist: SpecFormatting, Path: "foo.go", Line: 1, Side: "RIGHT", Reason: "tolerated"},
			},
		},
	}
	FinalizeRepoArbiter(d.RepoArbiter, d)
	if len(d.RepoArbiter.suppressKeySet) != 1 {
		t.Fatalf("precondition: expected 1 suppress key, got %d", len(d.RepoArbiter.suppressKeySet))
	}
	before := d.FlatPostableFindingsForPost()
	if len(before) != 0 {
		t.Fatalf("precondition: suppressed finding should not be postable, got %d", len(before))
	}

	restored := d.SessionSnapshot().Restore()
	if restored.RepoArbiter == nil {
		t.Fatalf("restored arbiter is nil")
	}
	if len(restored.RepoArbiter.suppressKeySet) != 1 {
		t.Errorf("restored suppressKeySet len = %d, want 1 (RebuildArbiterKeySets failed)", len(restored.RepoArbiter.suppressKeySet))
	}
	if got := restored.FlatPostableFindingsForPost(); len(got) != 0 {
		t.Errorf("restored draft postable findings = %d, want 0 (suppression not restored)", len(got))
	}
}

// Snapshot/Restore must preserve the resume-critical side-lists (DemotedHidden,
// MemorySuppressed, UserPostDemotedKeys) and the vibe-coach result.
func TestSessionDraftSnapshotPreservesSideLists(t *testing.T) {
	d := sampleDraft()
	d.DemotedHidden = []FlatFinding{
		{Specialist: SpecDescription, Finding: Finding{Comment: "empty description"}},
	}
	d.MemorySuppressed = []MemorySuppressedFinding{
		{Specialist: SpecSecurity, Finding: Finding{Path: "a.go", Line: 2, Comment: "skipped before"}, SkipCount: 3},
	}
	d.UserPostDemotedKeys = map[string]struct{}{"dk": {}}
	d.VibeCoach = &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "looks ok"}

	restored := d.SessionSnapshot().Restore()
	if len(restored.DemotedHidden) != 1 || restored.DemotedHidden[0].Finding.Comment != "empty description" {
		t.Errorf("DemotedHidden not preserved: %+v", restored.DemotedHidden)
	}
	if len(restored.MemorySuppressed) != 1 || restored.MemorySuppressed[0].SkipCount != 3 {
		t.Errorf("MemorySuppressed not preserved: %+v", restored.MemorySuppressed)
	}
	if _, ok := restored.UserPostDemotedKeys["dk"]; !ok {
		t.Errorf("UserPostDemotedKeys not preserved: %+v", restored.UserPostDemotedKeys)
	}
	if restored.VibeCoach == nil || restored.VibeCoach.Summary != "looks ok" {
		t.Errorf("VibeCoach not preserved: %+v", restored.VibeCoach)
	}
}
