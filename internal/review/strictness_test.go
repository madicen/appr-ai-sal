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
