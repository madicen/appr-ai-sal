package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// TestFinalizeRepoArbiterSuppressesPRWideFinding is the core of the
// "arbiter can suppress PR-agent findings" request: a PR agent files a
// PR-wide (no diff anchor) warning, and the arbiter suppresses it with a
// path:"" line:0 reference.
func TestFinalizeRepoArbiterSuppressesPRWideFinding(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecScope, Findings: []Finding{
				{Path: "", Line: 0, Severity: SeverityWarning, Comment: "split this into two PRs"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: SpecScope, Path: "", Line: 0, Reason: "repo ships these together"},
		},
	}
	d.RepoArbiter = ar
	FinalizeRepoArbiter(ar, d)

	if len(ar.suppressKeySet) != 1 {
		t.Fatalf("want 1 suppressed PR-wide key, got %d (dropped=%v)", len(ar.suppressKeySet), ar.DroppedSuppressions)
	}
	if len(ar.Suppressed) != 1 {
		t.Fatalf("want suppression kept, got %d", len(ar.Suppressed))
	}
	// The suppressed PR-wide finding must not leak into the rendered body...
	if body := d.RenderBody(); strings.Contains(body, "split this into two PRs") {
		t.Fatalf("suppressed PR-wide finding leaked into body:\n%s", body)
	}
	// ...nor into the vibe-coach's view of the specialists.
	vc := SpecialistsForVibeCoach(d, d.Specialists)
	for _, s := range vc {
		for _, f := range s.Findings {
			if f.Comment == "split this into two PRs" {
				t.Fatal("suppressed PR-wide finding leaked into vibe-coach input")
			}
		}
	}
}

// TestFinalizeRepoArbiterCannotSuppressPRWideError mirrors the inline
// guard: an error-severity PR-wide finding (e.g. a failing required check)
// must survive even if the arbiter tries to suppress it.
func TestFinalizeRepoArbiterCannotSuppressPRWideError(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecChecks, Findings: []Finding{
				{Path: "", Line: 0, Severity: SeverityError, Comment: "required CI is failing"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: SpecChecks, Path: "", Line: 0},
		},
	}
	d.RepoArbiter = ar
	FinalizeRepoArbiter(ar, d)
	if len(ar.suppressKeySet) != 0 {
		t.Fatalf("error-severity PR-wide finding must not be suppressible, got %d keys", len(ar.suppressKeySet))
	}
	if len(ar.DroppedSuppressions) == 0 {
		t.Fatal("expected a dropped-suppression reason")
	}
}

// TestFinalizeRepoArbiterDemotesPRWideFinding checks the arbiter can demote
// a PR-wide warning to info, and that the strictness floor then drops it
// under a balanced review intensity.
func TestFinalizeRepoArbiterDemotesPRWideFinding(t *testing.T) {
	d := &Draft{
		Strictness: aiconfig.ReviewBalanced,
		Specialists: []SpecialistResult{
			{Specialist: SpecDescription, Findings: []Finding{
				{Path: "", Line: 0, Severity: SeverityWarning, Comment: "title is a bit terse"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Demoted: []DemotedFindingRef{
			{Specialist: SpecDescription, Path: "", Line: 0, To: SeverityInfo, Reason: "repo norm"},
		},
	}
	d.RepoArbiter = ar
	FinalizeRepoArbiter(ar, d)
	if len(ar.Demoted) != 1 {
		t.Fatalf("want 1 demotion kept, got %d (dropped=%v)", len(ar.Demoted), ar.DroppedDemotions)
	}
	if len(d.Specialists[0].Findings) != 0 {
		t.Fatalf("expected demoted-to-info PR-wide finding to be filtered under balanced; got %+v", d.Specialists[0].Findings)
	}
	orig, ok := d.FindingOriginalSeverity(SpecDescription, Finding{Path: "", Line: 0, Side: "RIGHT"})
	if !ok || orig != SeverityWarning {
		t.Fatalf("FindingOriginalSeverity for demoted PR-wide finding = (%v,%v), want (warning,true)", orig, ok)
	}
}

// TestFinalizeRepoArbiterDropsUnmatchedPRWideRef guards against the arbiter
// referencing a PR-wide finding no agent actually filed.
func TestFinalizeRepoArbiterDropsUnmatchedPRWideRef(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecScope, Findings: []Finding{
				{Path: "a.go", Line: 5, Side: "RIGHT", Severity: SeverityWarning, Comment: "inline only"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: SpecScope, Path: "", Line: 0},
		},
	}
	d.RepoArbiter = ar
	FinalizeRepoArbiter(ar, d)
	if len(ar.suppressKeySet) != 0 {
		t.Fatalf("unmatched PR-wide ref must be dropped, got %d keys", len(ar.suppressKeySet))
	}
	if len(ar.DroppedSuppressions) == 0 {
		t.Fatal("expected a dropped-suppression reason for the unmatched PR-wide ref")
	}
}

// TestArbiterDigestInformsOfPRAgents confirms the digest the arbiter reads
// includes PR-agent findings and tags PR-wide ones with the path:"" line:0
// hint so the model knows how to reference them.
func TestArbiterDigestInformsOfPRAgents(t *testing.T) {
	digest := buildSpecialistDigestForRepoExperts([]SpecialistResult{
		{Specialist: SpecScope, Findings: []Finding{
			{Path: "", Line: 0, Severity: SeverityWarning, Comment: "split this PR"},
		}},
	})
	if !strings.Contains(digest, SpecScope) {
		t.Fatalf("digest should name the scope agent:\n%s", digest)
	}
	if !strings.Contains(digest, "split this PR") {
		t.Fatalf("digest should include the PR-agent finding:\n%s", digest)
	}
	if !strings.Contains(digest, `use path "" line 0`) {
		t.Fatalf("digest should hint how to reference PR-wide findings:\n%s", digest)
	}
}

// TestArbiterDigestMarksSummariesAsContextOnly guards the fix for the arbiter
// inventing merge concerns from a specialist's free-text summary: the digest
// must label summaries as context-only so the model treats only findings as
// actionable. A clean specialist that merely remarks on an out-of-lane issue
// (no finding) should not become a blocker.
func TestArbiterDigestMarksSummariesAsContextOnly(t *testing.T) {
	digest := buildSpecialistDigestForRepoExperts([]SpecialistResult{
		{Specialist: SpecFormatting, Summary: "clean except an inconsistent memory unit suffix"},
	})
	if !strings.Contains(digest, "CONTEXT ONLY") {
		t.Fatalf("digest should mark specialist summaries as context-only, not actionable:\n%s", digest)
	}
}
