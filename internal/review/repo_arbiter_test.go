package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestFinalizeRepoArbiterDropsSecuritySuppression(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecSecurity, Findings: []Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "x"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: "security", Path: "a.go", Line: 1, Side: "RIGHT", Reason: "nit"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.suppressKeySet) != 0 {
		t.Fatalf("expected security suppression refused, got %d keys", len(ar.suppressKeySet))
	}
	if len(ar.DroppedSuppressions) == 0 {
		t.Fatal("expected dropped reason")
	}
}

func TestFinalizeRepoArbiterDropsErrorSeveritySuppression(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecTesting, Findings: []Finding{
				{Path: "b.go", Line: 2, Side: "", Severity: SeverityError, Comment: "bad"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: "testing", Path: "b.go", Line: 2, Side: "RIGHT"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.suppressKeySet) != 0 {
		t.Fatal("error severity must not suppress")
	}
}

func TestFinalizeRepoArbiterKeepsTestingSuppression(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecTesting, Findings: []Finding{
				{Path: "c.go", Line: 3, Side: "", Severity: SeverityInfo, Comment: "add test"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: "testing", Path: "c.go", Line: 3},
		},
	}
	d.RepoArbiter = ar
	FinalizeRepoArbiter(ar, d)
	if len(ar.suppressKeySet) != 1 {
		t.Fatalf("want 1 suppressed key, got %d", len(ar.suppressKeySet))
	}
	post := d.FlatPostableFindingsForPost()
	if len(post) != 0 {
		t.Fatalf("expected all inline suppressed, got %d", len(post))
	}
}

func TestFlatPostableFindingsForPostUnchangedWithoutArbiter(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "d.go", Line: 1, Severity: SeverityInfo, Comment: "x"},
			}},
		},
	}
	if len(d.FlatPostableFindings()) != len(d.FlatPostableFindingsForPost()) {
		t.Fatal("without arbiter sets, counts should match")
	}
}

func TestParseRepoArbiterJSONMinimal(t *testing.T) {
	s := `{"user_summary":"ok","rationale_bullets":["a"],"verdict_override":"","summary_mode":"none","summary_text":"","suppress":[]}`
	p, err := parseRepoArbiterJSON(s)
	if err != nil || p.UserSummary != "ok" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestEffectiveVibeCoachVerdictOverride(t *testing.T) {
	d := &Draft{
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges, Summary: "block", Prompts: []AuthorPrompt{{Title: "t", AgentPrompt: "do"}}},
		RepoArbiter: &RepoArbiterResult{
			VerdictOverride:  VibeVerdictApprove,
			EffectiveVerdict: VibeVerdictApprove,
			SummaryMode:      "append",
			SummaryAddendum:  "low risk",
		},
	}
	vc := d.effectiveVibeCoach()
	if NormalizeVibeVerdict(vc.Verdict) != VibeVerdictApprove {
		t.Fatalf("verdict %q", vc.Verdict)
	}
	if !strings.Contains(vc.Summary, "Repo experts") {
		t.Fatalf("summary %q", vc.Summary)
	}
}

func TestFinalizeRepoArbiterAppliesDemoteOneRank(t *testing.T) {
	d := &Draft{
		Strictness: aiconfig.ReviewStrict,
		Specialists: []SpecialistResult{
			{Specialist: SpecTesting, Findings: []Finding{
				{Path: "x.go", Line: 4, Side: "RIGHT", Severity: SeverityError, Comment: "missing test"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Demoted: []DemotedFindingRef{
			{Specialist: "testing", Path: "x.go", Line: 4, Side: "RIGHT", To: SeverityWarning, Reason: "neighbours untested"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.Demoted) != 1 {
		t.Fatalf("kept demotions = %d, want 1", len(ar.Demoted))
	}
	if ar.Demoted[0].From != SeverityError || ar.Demoted[0].To != SeverityWarning {
		t.Fatalf("from/to wrong: %+v", ar.Demoted[0])
	}
	got := d.Specialists[0].Findings[0].Severity
	if got != SeverityWarning {
		t.Fatalf("severity not mutated: got %s want warning", got)
	}
}

func TestFinalizeRepoArbiterDropsSecurityDemote(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecSecurity, Findings: []Finding{
				{Path: "x.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "x"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Demoted: []DemotedFindingRef{
			{Specialist: "security", Path: "x.go", Line: 1, Side: "RIGHT"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.Demoted) != 0 {
		t.Fatalf("expected security demote refused, got %d", len(ar.Demoted))
	}
	if len(ar.DroppedDemotions) == 0 {
		t.Fatal("expected dropped reason for security demote")
	}
	if d.Specialists[0].Findings[0].Severity != SeverityWarning {
		t.Fatal("severity should not have been mutated")
	}
}

func TestFinalizeRepoArbiterDropsCriticalDemote(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "x.go", Line: 2, Side: "RIGHT", Severity: SeverityCritical, Comment: "x"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Demoted: []DemotedFindingRef{
			{Specialist: "docs", Path: "x.go", Line: 2, Side: "RIGHT"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.Demoted) != 0 {
		t.Fatalf("expected critical demote refused, got %d", len(ar.Demoted))
	}
	if d.Specialists[0].Findings[0].Severity != SeverityCritical {
		t.Fatal("severity should not have been mutated")
	}
}

func TestFinalizeRepoArbiterRejectsMultiRankDemote(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "x.go", Line: 3, Side: "RIGHT", Severity: SeverityError, Comment: "x"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Demoted: []DemotedFindingRef{
			{Specialist: "docs", Path: "x.go", Line: 3, Side: "RIGHT", To: SeverityInfo},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.Demoted) != 0 {
		t.Fatalf("expected multi-rank demote refused, got %d", len(ar.Demoted))
	}
	if d.Specialists[0].Findings[0].Severity != SeverityError {
		t.Fatal("severity should not have been mutated")
	}
}

func TestFinalizeRepoArbiterDemoteToInfoFiltersUnderBalanced(t *testing.T) {
	d := &Draft{
		Strictness: aiconfig.ReviewBalanced,
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "x.go", Line: 5, Side: "RIGHT", Severity: SeverityWarning, Comment: "missing doc"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Demoted: []DemotedFindingRef{
			{Specialist: "docs", Path: "x.go", Line: 5, Side: "RIGHT"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(d.Specialists[0].Findings) != 0 {
		t.Fatalf("expected demoted-to-info finding to be filtered out under balanced strictness; got %+v",
			d.Specialists[0].Findings)
	}
}

func TestFinalizeRepoArbiterDemoteToInfoKeptUnderStrict(t *testing.T) {
	d := &Draft{
		Strictness: aiconfig.ReviewStrict,
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "x.go", Line: 6, Side: "RIGHT", Severity: SeverityWarning, Comment: "missing doc"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Demoted: []DemotedFindingRef{
			{Specialist: "docs", Path: "x.go", Line: 6, Side: "RIGHT"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(d.Specialists[0].Findings) != 1 {
		t.Fatalf("expected demoted finding to remain under strict; got %+v", d.Specialists[0].Findings)
	}
	if d.Specialists[0].Findings[0].Severity != SeverityInfo {
		t.Fatalf("severity = %s, want info", d.Specialists[0].Findings[0].Severity)
	}
}

func TestFinalizeRepoArbiterIgnoresDemoteForSuppressedKey(t *testing.T) {
	d := &Draft{
		Strictness: aiconfig.ReviewStrict,
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "x.go", Line: 7, Side: "RIGHT", Severity: SeverityWarning, Comment: "x"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: "docs", Path: "x.go", Line: 7, Side: "RIGHT"},
		},
		Demoted: []DemotedFindingRef{
			{Specialist: "docs", Path: "x.go", Line: 7, Side: "RIGHT"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.Suppressed) != 1 {
		t.Fatalf("expected suppression kept, got %d", len(ar.Suppressed))
	}
	if len(ar.Demoted) != 0 {
		t.Fatalf("expected demote dropped because suppression already wins, got %d", len(ar.Demoted))
	}
}

func TestParseRepoArbiterJSONWithDemote(t *testing.T) {
	s := `{"user_summary":"ok","rationale_bullets":[],"verdict_override":"","summary_mode":"none","summary_text":"","suppress":[],"demote":[{"specialist":"docs","path":"a.go","line":1,"side":"RIGHT","from":"warning","to":"info","reason":"r"}]}`
	p, err := parseRepoArbiterJSON(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Demote) != 1 || p.Demote[0].Reason != "r" {
		t.Fatalf("demote parse failed: %+v", p.Demote)
	}
}

func TestToReviewRespectsSuppression(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "e.go", Line: 1, Severity: SeverityInfo, Comment: "a"},
				{Path: "e.go", Line: 2, Severity: SeverityInfo, Comment: "b"},
			}},
		},
		RepoArbiter: &RepoArbiterResult{
			VerdictOverride:  "",
			EffectiveVerdict: VibeVerdictComment,
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "e.go", 1, "RIGHT"): {},
			},
		},
	}
	rev := d.ToReview()
	if len(rev.Comments) != 1 {
		t.Fatalf("want 1 comment, got %d", len(rev.Comments))
	}
	if rev.Comments[0].Line != 2 {
		t.Fatalf("wrong line %d", rev.Comments[0].Line)
	}
}
