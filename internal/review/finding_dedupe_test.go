package review

import "testing"

func dedupeFinding(comment, suggestion string) Finding {
	return Finding{Path: "deploy/app.yaml", Line: 207, Side: "RIGHT", Severity: SeverityWarning, Comment: comment, Suggestion: suggestion}
}

func countFindings(specs []SpecialistResult) int {
	n := 0
	for _, s := range specs {
		n += len(s.Findings)
	}
	return n
}

func findingsForSpecialist(specs []SpecialistResult, name string) []Finding {
	for _, s := range specs {
		if s.Specialist == name {
			return s.Findings
		}
	}
	return nil
}

func TestDedupeCollapsesSameLineDuplicatesKeepingLanePriority(t *testing.T) {
	comment := "The memory unit M should be Mi for Kubernetes binary quantities."
	specs := []SpecialistResult{
		{Specialist: SpecDesign, Findings: []Finding{dedupeFinding(comment, "        memory: 717Mi")}},
		{Specialist: SpecTesting, Findings: []Finding{dedupeFinding(comment, "        memory: 717Mi")}},
		{Specialist: SpecFormatting, Findings: []Finding{dedupeFinding(comment, "        memory: 717Mi")}},
		{Specialist: SpecDiscussion, Findings: []Finding{dedupeFinding(comment, "        memory: 717Mi")}},
	}
	out := dedupeInlineFindingsAcrossSpecialists(specs)
	if got := countFindings(out); got != 1 {
		t.Fatalf("expected 1 finding after dedupe, got %d", got)
	}
	if len(findingsForSpecialist(out, SpecFormatting)) != 1 {
		t.Fatalf("formatting (highest lane priority) should keep the finding")
	}
	for _, name := range []string{SpecDesign, SpecTesting, SpecDiscussion} {
		if len(findingsForSpecialist(out, name)) != 0 {
			t.Fatalf("%s duplicate should be dropped", name)
		}
	}
}

func TestDedupeKeepsDistinctConcernsOnSameLine(t *testing.T) {
	specs := []SpecialistResult{
		{Specialist: SpecFormatting, Findings: []Finding{dedupeFinding("Indentation here is inconsistent with the surrounding block.", "")}},
		{Specialist: SpecSecurity, Findings: []Finding{dedupeFinding("This line hardcodes a plaintext credential token.", "")}},
	}
	out := dedupeInlineFindingsAcrossSpecialists(specs)
	if got := countFindings(out); got != 2 {
		t.Fatalf("distinct concerns on the same line must both survive, got %d", got)
	}
}

func TestDedupeNeverTouchesPRWideFindings(t *testing.T) {
	prWide := Finding{Path: "", Line: 0, Severity: SeverityWarning, Comment: "same overall note"}
	specs := []SpecialistResult{
		{Specialist: SpecDesign, Findings: []Finding{prWide}},
		{Specialist: SpecTesting, Findings: []Finding{prWide}},
	}
	out := dedupeInlineFindingsAcrossSpecialists(specs)
	if got := countFindings(out); got != 2 {
		t.Fatalf("PR-wide findings must never be collapsed, got %d", got)
	}
}

func TestDedupePrefersSuggestionOnPriorityTie(t *testing.T) {
	comment := "The memory unit should be Mi rather than M for binary quantities here."
	// Same specialist (priority tie) files two near-duplicate findings on the
	// same line; the one carrying a suggestion should win.
	specs := []SpecialistResult{
		{Specialist: SpecFormatting, Findings: []Finding{
			dedupeFinding(comment, ""),
			dedupeFinding(comment, "        memory: 717Mi"),
		}},
	}
	out := dedupeInlineFindingsAcrossSpecialists(specs)
	kept := findingsForSpecialist(out, SpecFormatting)
	if len(kept) != 1 {
		t.Fatalf("expected 1 finding after dedupe, got %d", len(kept))
	}
	if kept[0].Suggestion == "" {
		t.Fatalf("the finding carrying a suggestion should be the keeper")
	}
}
