package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
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

	got := buildRepoArbiterUserPrompt(pr, digest, per, "", nil, "", aiconfig.ReviewBalanced)

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
	// The vibe-coach runs AFTER the arbiter, so the digest heading must not
	// promise vibe content that is never present (0.4 fix #3).
	if !strings.Contains(got, "## Specialist findings digest") {
		t.Fatalf("expected specialist findings digest section, got:\n%s", got)
	}
	if strings.Contains(got, "vibe digest") {
		t.Fatalf("stale 'vibe digest' heading must be gone, got:\n%s", got)
	}
}

func TestBuildRepoArbiterUserPromptIncludesWitnessSection(t *testing.T) {
	pr := &gh.PR{Number: 42, Title: "x", Repository: "acme/widget"}
	witnesses := []conventionwitness.Witness{
		{Specialist: "testing", Path: "a.go", Line: 5, Side: "RIGHT", Verdict: conventionwitness.VerdictContradictsFinding, Citation: "no sib tests"},
	}
	got := buildRepoArbiterUserPrompt(pr, "digest", nil, "", witnesses, "", aiconfig.ReviewBalanced)
	if !strings.Contains(got, "## Convention witness") {
		t.Fatalf("missing witness section header in:\n%s", got)
	}
	if !strings.Contains(got, "no sib tests") {
		t.Fatalf("witness citation missing")
	}
}

func TestBuildRepoArbiterUserPromptOmitsEmptyWitnessSection(t *testing.T) {
	pr := &gh.PR{Number: 42, Title: "x", Repository: "acme/widget"}
	got := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil, "", aiconfig.ReviewBalanced)
	if strings.Contains(got, "## Convention witness") {
		t.Fatalf("witness section should be omitted when empty:\n%s", got)
	}
}

// The arbiter prompt must instruct the model to default-keep objective
// PR-agent findings (empty description, failing checks) rather than silently
// demoting them below the floor to quiet the review.
func TestRepoArbiterPromptDefaultKeepsPRAgentFindings(t *testing.T) {
	body, err := SpecialistPrompt(specRepoArbiter)
	if err != nil {
		t.Fatalf("load repo-arbiter prompt: %v", err)
	}
	mustContain := []string{
		// Section framing PR-agent findings as objective.
		"PR-agent findings are objective",
		"Default-keep",
		// The two canonical objective signals called out by name.
		"empty",
		"failing required check",
		// The anti-pattern this calibration prevents.
		"Never demote a PR-agent finding to dodge the strictness floor",
	}
	for _, marker := range mustContain {
		if !strings.Contains(body, marker) {
			t.Errorf("repo-arbiter prompt missing PR-agent default-keep marker %q", marker)
		}
	}
}

// Q3.5: the arbiter prompt threads the review intensity through so the model
// can calibrate demotion aggressiveness. At the default (balanced) level the
// prompt must be byte-identical to a run that supplies no strictness signal
// (behavior preservation); at the off-default levels a calibration section
// appears and names the chosen intensity.
func TestBuildRepoArbiterUserPromptStrictnessBlock(t *testing.T) {
	pr := &gh.PR{Number: 42, Title: "x", Repository: "acme/widget"}

	balanced := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil, "", aiconfig.ReviewBalanced)
	if strings.Contains(balanced, "## Review intensity") {
		t.Fatalf("balanced (default) arbiter prompt must not add a strictness section:\n%s", balanced)
	}

	for _, tc := range []struct {
		level aiconfig.ReviewStrictness
		label string
	}{
		{aiconfig.ReviewCriticalOnly, "critical-only"},
		{aiconfig.ReviewLenient, "lenient"},
		{aiconfig.ReviewStrict, "strict"},
	} {
		got := buildRepoArbiterUserPrompt(pr, "digest", nil, "", nil, "", tc.level)
		if !strings.Contains(got, "## Review intensity:") {
			t.Fatalf("%s: expected a review-intensity section, got:\n%s", tc.level, got)
		}
		if !strings.Contains(got, tc.label) {
			t.Fatalf("%s: intensity section must name the chosen level %q, got:\n%s", tc.level, tc.label, got)
		}
		// Removing the injected block must recover the balanced prompt exactly —
		// the section is additive and disturbs nothing else.
		block := strictnessBlockForArbiter(tc.level)
		if block == "" {
			t.Fatalf("%s: expected a non-empty strictness block", tc.level)
		}
		if recovered := strings.Replace(got, block, "", 1); recovered != balanced {
			t.Fatalf("%s: strictness block must be inserted cleanly\n--- recovered ---\n%s\n--- balanced ---\n%s", tc.level, recovered, balanced)
		}
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
