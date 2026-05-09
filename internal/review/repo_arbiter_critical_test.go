package review

import "testing"

func TestFinalizeRepoArbiterDropsCriticalSeveritySuppression(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityCritical, Comment: "stop ship"},
			}},
		},
	}
	ar := &RepoArbiterResult{
		Suppressed: []SuppressedFindingRef{
			{Specialist: "docs", Path: "a.go", Line: 1, Side: "RIGHT"},
		},
	}
	FinalizeRepoArbiter(ar, d)
	if len(ar.suppressKeySet) != 0 {
		t.Fatalf("expected critical suppression refused, got %d keys", len(ar.suppressKeySet))
	}
}
