package review

import (
	"strings"
	"testing"
)

func TestValidateActionabilityDemotesBareDocsDeficiency(t *testing.T) {
	in := []Finding{{
		Path:     "main.tf",
		Line:     78,
		Side:     "RIGHT",
		Severity: SeverityError,
		Comment:  "The DNS entry `hold` in this file lacks a comment.",
	}}
	out := validateActionability(SpecDocs, in)
	if out[0].Severity != SeverityInfo {
		t.Fatalf("severity should be demoted to info, got %q", out[0].Severity)
	}
	if !strings.Contains(out[0].ActionabilityNote, "low actionability") {
		t.Fatalf("ActionabilityNote should be set: %q", out[0].ActionabilityNote)
	}
}

func TestValidateActionabilityKeepsActionableDocsFinding(t *testing.T) {
	in := []Finding{{
		Path:     "foo.go",
		Line:     12,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "ParseConfig is exported but lacks a godoc comment. Add: \"ParseConfig reads the file at p and returns the parsed Config.\"",
	}}
	out := validateActionability(SpecDocs, in)
	if out[0].Severity != SeverityWarning {
		t.Fatalf("actionable comment with quoted proposed wording should not be demoted, got %q", out[0].Severity)
	}
	if out[0].ActionabilityNote != "" {
		t.Fatalf("ActionabilityNote should be empty for actionable findings, got %q", out[0].ActionabilityNote)
	}
}

func TestValidateActionabilityKeepsFindingWithSuggestion(t *testing.T) {
	in := []Finding{{
		Path:       "foo.go",
		Line:       12,
		Side:       "RIGHT",
		Severity:   SeverityWarning,
		Comment:    "ParseConfig lacks a godoc comment.",
		Suggestion: "// ParseConfig reads the file at p.\nfunc ParseConfig(p string) {",
	}}
	out := validateActionability(SpecDocs, in)
	if out[0].Severity != SeverityWarning {
		t.Fatalf("non-empty suggestion satisfies the actionability bar; got %q", out[0].Severity)
	}
	if out[0].ActionabilityNote != "" {
		t.Fatalf("ActionabilityNote should be empty when a suggestion is present, got %q", out[0].ActionabilityNote)
	}
}

func TestValidateActionabilityDemotesBareTestingDeficiency(t *testing.T) {
	in := []Finding{{
		Path:     "service.go",
		Line:     42,
		Side:     "RIGHT",
		Severity: SeverityError,
		Comment:  "This handler lacks a unit test.",
	}}
	out := validateActionability(SpecTesting, in)
	if out[0].Severity != SeverityInfo {
		t.Fatalf("bare 'lacks a unit test' should be demoted to info, got %q", out[0].Severity)
	}
}

func TestValidateActionabilityKeepsActionableTestingFinding(t *testing.T) {
	in := []Finding{{
		Path:     "service.go",
		Line:     42,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "Missing test for the timeout=0 branch — add a row asserting that ParseTimeout(0) returns ErrZeroTimeout.",
	}}
	out := validateActionability(SpecTesting, in)
	if out[0].Severity != SeverityWarning {
		t.Fatalf("comment with concrete wording should not be demoted, got %q", out[0].Severity)
	}
}

func TestValidateActionabilityIgnoresOtherSpecialists(t *testing.T) {
	in := []Finding{{
		Path:     "main.go",
		Line:     1,
		Side:     "RIGHT",
		Severity: SeverityError,
		Comment:  "This entire file lacks a comment header.",
	}}
	out := validateActionability(SpecDesign, in)
	if out[0].Severity != SeverityError {
		t.Fatalf("non-docs/testing specialist should not be touched, got %q", out[0].Severity)
	}
}

func TestHasProposedWordingBacktickThreshold(t *testing.T) {
	if hasProposedWording("missing doc on `x`") {
		t.Fatal("single-letter backtick token should not satisfy the actionability bar")
	}
	if !hasProposedWording("rename `OldName` to `NewName`") {
		t.Fatal("non-trivial backtick identifier with 'rename to' marker should satisfy")
	}
	if !hasProposedWording("Should be \"a more descriptive sentence here\"") {
		t.Fatal("substantive double-quoted span should satisfy")
	}
	if !hasProposedWording("Header: here is the long-form proposed replacement text.") {
		t.Fatal("substantive post-colon should satisfy")
	}
}
