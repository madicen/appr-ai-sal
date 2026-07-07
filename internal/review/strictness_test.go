package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

func TestMinSeverityFourLevels(t *testing.T) {
	cases := []struct {
		rs   aiconfig.ReviewStrictness
		want Severity
	}{
		{aiconfig.ReviewCriticalOnly, SeverityCritical},
		{aiconfig.ReviewLenient, SeverityError},
		{aiconfig.ReviewBalanced, SeverityWarning},
		{aiconfig.ReviewStrict, SeverityInfo},
	}
	for _, tc := range cases {
		if g := MinSeverityForStrictness(tc.rs); g != tc.want {
			t.Errorf("%q: got %q want %q", tc.rs, g, tc.want)
		}
	}
}

func TestFilterFindingsBySeverityCriticalFloor(t *testing.T) {
	findings := []Finding{
		{Path: "a.go", Line: 1, Severity: SeverityInfo, Comment: "i"},
		{Path: "a.go", Line: 2, Severity: SeverityWarning, Comment: "w"},
		{Path: "a.go", Line: 3, Severity: SeverityError, Comment: "e"},
		{Path: "a.go", Line: 4, Severity: SeverityCritical, Comment: "c"},
	}
	out := FilterFindingsBySeverity(findings, SeverityCritical)
	if len(out) != 1 || out[0].Severity != SeverityCritical {
		t.Fatalf("critical floor: got %+v", out)
	}
}

func TestFilterFindingsBySeverityLenientKeepsErrorAndCritical(t *testing.T) {
	findings := []Finding{
		{Path: "a.go", Line: 1, Severity: SeverityInfo, Comment: "i"},
		{Path: "a.go", Line: 2, Severity: SeverityError, Comment: "e"},
		{Path: "a.go", Line: 3, Severity: SeverityCritical, Comment: "c"},
	}
	out := FilterFindingsBySeverity(findings, MinSeverityForStrictness(aiconfig.ReviewLenient))
	if len(out) != 2 {
		t.Fatalf("lenient: want 2 kept, got %d %+v", len(out), out)
	}
}

// normalizeSeverity folds synonyms/unknowns to canonical severities so an
// unknown model string (e.g. "high", "blocker", "nit") never renders verbatim
// in the review body (0.4 fix #6).
func TestNormalizeSeverityFoldsSynonyms(t *testing.T) {
	cases := map[Severity]Severity{
		// canonical passthrough
		SeverityInfo:     SeverityInfo,
		SeverityWarning:  SeverityWarning,
		SeverityError:    SeverityError,
		SeverityCritical: SeverityCritical,
		// synonyms
		"high":    SeverityError,
		"major":   SeverityError,
		"blocker": SeverityCritical,
		"crit":    SeverityCritical,
		"nit":     SeverityInfo,
		"low":     SeverityInfo,
		"medium":  SeverityWarning,
		// case + whitespace insensitivity
		"  HIGH ": SeverityError,
		// unknown / empty → warning (matches the filter's rank-0 coercion)
		"bogus": SeverityWarning,
		"":      SeverityWarning,
	}
	for in, want := range cases {
		if got := normalizeSeverity(in); got != want {
			t.Errorf("normalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

// normalizeSpecialistSeverities must canonicalise every finding's severity at
// parse time (0.4 fix #6).
func TestNormalizeSpecialistSeveritiesCanonicalises(t *testing.T) {
	o := &specialistJSON{Findings: []Finding{
		{Severity: "high"},
		{Severity: "blocker"},
		{Severity: "totally-unknown"},
	}}
	normalizeSpecialistSeverities(o)
	want := []Severity{SeverityError, SeverityCritical, SeverityWarning}
	for i, w := range want {
		if o.Findings[i].Severity != w {
			t.Errorf("finding %d severity = %q, want %q", i, o.Findings[i].Severity, w)
		}
	}
}
