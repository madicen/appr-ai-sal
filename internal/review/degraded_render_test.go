package review

import (
	"errors"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestDegradedStagesDistinguishesFailedFromSkipped(t *testing.T) {
	t.Parallel()
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Findings: []Finding{{Path: "a.go", Line: 1, Comment: "nit", Severity: SeverityInfo}}},
			{Specialist: SpecSecurity, Err: errors.New("boom after retries"), Outcome: OutcomeFailed},
			{Specialist: SpecDocs, Outcome: OutcomeSkipped, OutcomeReason: "circuit breaker: 4 consecutive stage failures"},
			// Pre-R4 convention: Err set, Outcome left zero — must still count as failed.
			{Specialist: SpecTesting, Err: errors.New("legacy failure")},
		},
	}
	failed, skipped := d.DegradedStages()
	if strings.Join(failed, ",") != SpecSecurity+","+SpecTesting {
		t.Fatalf("failed = %v, want [security testing]", failed)
	}
	if strings.Join(skipped, ",") != SpecDocs {
		t.Fatalf("skipped = %v, want [docs]", skipped)
	}
}

func TestRenderBodyListsDegradedStages(t *testing.T) {
	t.Parallel()
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Findings: []Finding{{Path: "a.go", Line: 1, Comment: "nit", Severity: SeverityInfo}}},
			{Specialist: SpecSecurity, Err: errors.New("transport gave up"), Outcome: OutcomeFailed},
			{Specialist: SpecDocs, Outcome: OutcomeSkipped, OutcomeReason: "circuit breaker"},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "ok"},
	}
	body := d.RenderBody()

	if !strings.Contains(body, "### Agent failures _(failed after retries)_") {
		t.Fatalf("body should have a failed-after-retries section:\n%s", body)
	}
	if !strings.Contains(body, "**security:**") || !strings.Contains(body, "transport gave up") {
		t.Fatalf("failed section should name the security agent and its error:\n%s", body)
	}
	if !strings.Contains(body, "### Stages skipped _(run aborted early)_") {
		t.Fatalf("body should have a skipped-stages section:\n%s", body)
	}
	if !strings.Contains(body, "**docs**") {
		t.Fatalf("skipped section should list docs:\n%s", body)
	}
	// A skipped stage must NOT be listed under failures.
	failIdx := strings.Index(body, "### Agent failures")
	skipIdx := strings.Index(body, "### Stages skipped")
	if failIdx < 0 || skipIdx < 0 {
		t.Fatal("both sections expected")
	}
	failSection := body[failIdx:skipIdx]
	if strings.Contains(failSection, "docs") {
		t.Fatalf("docs (skipped) must not appear in the failures section:\n%s", failSection)
	}
}

// A run with skipped stages and no findings must NOT be treated as a clean
// "no issues found" auto-approve — the review is partial.
func TestHasNoFindingsFalseWhenStagesSkipped(t *testing.T) {
	t.Parallel()
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Outcome: OutcomeSkipped, OutcomeReason: "circuit breaker"},
		},
	}
	if d.HasNoFindings() {
		t.Fatal("a run with a skipped stage must not report HasNoFindings=true")
	}
	body := d.RenderBody()
	if strings.Contains(body, "No issues found by any agent") {
		t.Fatalf("degraded run must not render the clean no-issues body:\n%s", body)
	}
}

func TestDegradedDetailFormat(t *testing.T) {
	t.Parallel()
	if got := degradedDetail([]string{"security"}, []string{"docs", "tech"}); got != "failed after retries: security; skipped: docs, tech" {
		t.Fatalf("degradedDetail = %q", got)
	}
	if got := degradedDetail(nil, []string{"docs"}); got != "skipped: docs" {
		t.Fatalf("degradedDetail skipped-only = %q", got)
	}
	if got := degradedDetail([]string{"security"}, nil); got != "failed after retries: security" {
		t.Fatalf("degradedDetail failed-only = %q", got)
	}
}
