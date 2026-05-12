package review

import (
	"strings"
	"testing"
)

func TestValidateNamingConventionDemotesSnakeCaseOnGo(t *testing.T) {
	f := Finding{
		Path:       "internal/ingress/ingress_pipeline.go",
		Line:       76,
		Side:       "RIGHT",
		Severity:   SeverityWarning,
		Comment:    "The function name should be in snake_case according to naming conventions.",
		Suggestion: `ingestValidatorFunc{N: "strip_html", F: ingressStripHTML}`,
	}
	out := validateNamingConvention([]Finding{f})
	if out[0].Severity != SeverityInfo {
		t.Fatalf("severity should have been demoted to info, got %s", out[0].Severity)
	}
	if out[0].Suggestion != "" {
		t.Fatalf("suggestion should be cleared, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].ActionabilityNote, "Go uses MixedCaps") {
		t.Fatalf("actionability note should mention Go vs MixedCaps, got: %q", out[0].ActionabilityNote)
	}
	if !strings.Contains(out[0].ActionabilityNote, "recommends snake_case") {
		t.Fatalf("note should record the bad recommendation, got: %q", out[0].ActionabilityNote)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "convention mismatch") {
		t.Fatalf("stripped reason should mirror the note, got: %q", out[0].SuggestionStrippedReason)
	}
	if !strings.Contains(out[0].Comment, "snake_case") {
		t.Fatalf("comment must be preserved, got: %q", out[0].Comment)
	}
}

func TestValidateNamingConventionLeavesSnakeCaseOnPython(t *testing.T) {
	f := Finding{
		Path:       "scripts/foo.py",
		Line:       10,
		Side:       "RIGHT",
		Severity:   SeverityWarning,
		Comment:    "Rename this function to snake_case per Python conventions.",
		Suggestion: "def my_function():",
	}
	out := validateNamingConvention([]Finding{f})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("snake_case is correct for Python; severity must be untouched, got %s", out[0].Severity)
	}
	if out[0].Suggestion == "" {
		t.Fatal("suggestion must not be stripped when recommendation matches the language")
	}
}

func TestValidateNamingConventionDemotesCamelCaseOnPython(t *testing.T) {
	f := Finding{
		Path:     "scripts/foo.py",
		Line:     10,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "This function should be in camelCase per the naming convention.",
	}
	out := validateNamingConvention([]Finding{f})
	if out[0].Severity != SeverityInfo {
		t.Fatalf("camelCase is wrong for a Python function; should be demoted, got %s", out[0].Severity)
	}
}

func TestValidateNamingConventionPascalCaseForGoType(t *testing.T) {
	// Go uses MixedCaps (which is just PascalCase for exported names). The
	// gate treats "MixedCaps" and "PascalCase" as DIFFERENT canonical
	// strings on purpose — the table for Go records "MixedCaps" because
	// that's what the language calls it. A model recommendation of
	// "PascalCase for a Go type" is technically the same shape but our
	// table marks it as a mismatch. We accept that minor cost (the
	// suggestion is still kept; just severity is demoted) in exchange
	// for not papering over actual model confusion. The tests assert
	// this documented behaviour.
	f := Finding{
		Path:     "foo.go",
		Line:     5,
		Severity: SeverityWarning,
		Comment:  "Rename this type to PascalCase per naming conventions.",
	}
	out := validateNamingConvention([]Finding{f})
	if out[0].Severity != SeverityInfo {
		t.Fatalf("PascalCase vs MixedCaps mismatch should demote, got %s", out[0].Severity)
	}
}

func TestValidateNamingConventionIgnoresUnknownExtension(t *testing.T) {
	f := Finding{
		Path:     "config.yaml",
		Line:     2,
		Severity: SeverityWarning,
		Comment:  "Should be in snake_case per naming conventions.",
	}
	out := validateNamingConvention([]Finding{f})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("unknown extension must pass through; severity changed to %s", out[0].Severity)
	}
}

func TestValidateNamingConventionIgnoresVagueComments(t *testing.T) {
	f := Finding{
		Path:     "foo.go",
		Line:     5,
		Severity: SeverityWarning,
		Comment:  "This function name is unclear; consider something more descriptive.",
	}
	out := validateNamingConvention([]Finding{f})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("comment with no named convention must not trip the gate, got %s", out[0].Severity)
	}
}

func TestValidateNamingConventionLeavesEmptyPath(t *testing.T) {
	f := Finding{
		Path:     "",
		Line:     0,
		Severity: SeverityWarning,
		Comment:  "PR-wide: prefer snake_case.",
	}
	out := validateNamingConvention([]Finding{f})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("general findings have no path/extension to anchor on; must pass through, got %s", out[0].Severity)
	}
}

func TestExtractRecommendedConvention(t *testing.T) {
	cases := []struct {
		in           string
		wantConv     string
		wantKind     string
		wantSomeConv bool
	}{
		{"The function name should be in snake_case.", "snake_case", "function", true},
		{"Rename this type to PascalCase per naming conventions.", "PascalCase", "type", true},
		{"Use camelCase for this variable.", "camelCase", "variable", true},
		{"Should be snake_case per naming conventions.", "snake_case", "", true},
		{"PascalCase is the naming convention here.", "PascalCase", "", true},
		{"This function is unclear, rename it.", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		conv, kind := extractRecommendedConvention(tc.in)
		if tc.wantSomeConv && conv != tc.wantConv {
			t.Errorf("extractRecommendedConvention(%q) conv = %q, want %q", tc.in, conv, tc.wantConv)
		}
		if !tc.wantSomeConv && conv != "" {
			t.Errorf("extractRecommendedConvention(%q) conv = %q, want empty", tc.in, conv)
		}
		if tc.wantSomeConv && kind != tc.wantKind {
			t.Errorf("extractRecommendedConvention(%q) kind = %q, want %q", tc.in, kind, tc.wantKind)
		}
	}
}
