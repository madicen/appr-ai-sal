package conventionwitness

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

// Q3.5: witness user prompt threads review intensity; balanced stays byte-identical.
func TestBuildUserPromptStrictnessBlock(t *testing.T) {
	pr := PrWideRef{Repository: "o/r", Number: 1, Title: "t"}
	findings := []FindingInput{{Specialist: "testing", Path: "a.go", Line: 1, Severity: "warning", Comment: "x"}}
	evidence := "evidence"

	balanced := buildUserPrompt(pr, findings, evidence, aiconfig.ReviewBalanced)
	if strings.Contains(balanced, "## Review intensity") {
		t.Fatalf("balanced witness prompt must not add strictness section:\n%s", balanced)
	}

	for _, tc := range []struct {
		level  aiconfig.ReviewStrictness
		needle string
	}{
		{aiconfig.ReviewStrict, "strict"},
		{aiconfig.ReviewLenient, "lenient"},
		{aiconfig.ReviewCriticalOnly, "lenient"},
	} {
		got := buildUserPrompt(pr, findings, evidence, tc.level)
		if !strings.Contains(got, "## Review intensity:") {
			t.Fatalf("%s: expected review-intensity section, got:\n%s", tc.level, got)
		}
		if !strings.Contains(got, tc.needle) {
			t.Fatalf("%s: intensity section must mention %q, got:\n%s", tc.level, tc.needle, got)
		}
		block := strictnessScrutinyBlock(tc.level)
		if block == "" {
			t.Fatalf("%s: expected non-empty strictness block", tc.level)
		}
		if recovered := strings.Replace(got, "\n"+block, "", 1); recovered != balanced {
			t.Fatalf("%s: strictness block must insert cleanly\n--- recovered ---\n%s\n--- balanced ---\n%s", tc.level, recovered, balanced)
		}
	}
}
