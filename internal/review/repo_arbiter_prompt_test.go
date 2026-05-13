package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
)

func TestBuildRepoArbiterUserPromptIncludesPerAgentBriefs(t *testing.T) {
	pr := &gh.PR{Number: 42, Title: "tighten docs", Repository: "acme/widget"}
	digest := "--- testing ---\nSummary: ...\n"
	per := map[string]string{
		"testing":    "Testing brief: small helpers ship without tests in this repo.",
		"formatting": "Formatting brief: use go fmt + golangci-lint.",
		// design / docs / security intentionally absent.
	}

	got := buildRepoArbiterUserPrompt(pr, digest, per, "", nil)

	if !strings.Contains(got, "## Per-specialist repo-agent briefs") {
		t.Fatalf("expected briefs section header, got:\n%s", got)
	}
	for _, name := range AllSpecialists {
		if !strings.Contains(got, "### Repo agent: "+name) {
			t.Fatalf("expected subsection for %q, got:\n%s", name, got)
		}
	}
	if !strings.Contains(got, "small helpers ship without tests") {
		t.Fatalf("testing brief body missing")
	}
	if !strings.Contains(got, "use go fmt + golangci-lint") {
		t.Fatalf("formatting brief body missing")
	}
	// Specialists without a brief must explicitly say so.
	if !strings.Contains(got, "no brief on file") {
		t.Fatalf("expected fallback notice for missing briefs")
	}
	if !strings.Contains(got, "## Specialist + vibe digest") {
		t.Fatalf("expected specialist digest section, got:\n%s", got)
	}
}

func TestBuildRepoArbiterUserPromptIncludesWitnessSection(t *testing.T) {
	pr := &gh.PR{Number: 42, Title: "x", Repository: "acme/widget"}
	witnesses := []conventionwitness.Witness{
		{Specialist: "testing", Path: "a.go", Line: 5, Side: "RIGHT", Verdict: conventionwitness.VerdictCongruent, Citation: "no sib tests"},
	}
	got := buildRepoArbiterUserPrompt(pr, "digest", nil, "", witnesses)
	if !strings.Contains(got, "## Convention witness") {
		t.Fatalf("missing witness section header in:\n%s", got)
	}
	if !strings.Contains(got, "no sib tests") {
		t.Fatalf("witness citation missing")
	}
}

func TestBuildRepoArbiterUserPromptOmitsEmptyWitnessSection(t *testing.T) {
	pr := &gh.PR{Number: 42, Title: "x", Repository: "acme/widget"}
	got := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil)
	if strings.Contains(got, "## Convention witness") {
		t.Fatalf("witness section should be omitted when empty:\n%s", got)
	}
}

func TestFormatPerAgentBriefsIsStableAndMonotonic(t *testing.T) {
	per := map[string]string{
		"design":   "design brief A",
		"security": "security brief B",
	}
	out := formatPerAgentBriefs(per)
	// Headers exist for every known specialist, in a deterministic ordering.
	for _, name := range AllSpecialists {
		if !strings.Contains(out, "### Repo agent: "+name) {
			t.Fatalf("missing header for %q in:\n%s", name, out)
		}
	}
	// Empty agent block uses the parenthetical placeholder.
	if !strings.Contains(out, "_(no brief on file for this repo + specialist.)_") {
		t.Fatalf("expected fallback string for missing briefs, got:\n%s", out)
	}
}
