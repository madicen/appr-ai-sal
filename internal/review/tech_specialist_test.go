package review

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/techagents"
)

// SpecTech must be a registered specialist so it auto-wires into the runner
// dispatch, the arbiter digest/briefs, the vibe-coach input, and the TUI tabs.
func TestSpecTechIsRegisteredSpecialist(t *testing.T) {
	found := false
	for _, s := range AllSpecialists {
		if s == SpecTech {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SpecTech (%q) must be a member of AllSpecialists; got %v", SpecTech, AllSpecialists)
	}
	if IsPRAgent(SpecTech) {
		t.Fatalf("SpecTech is a code specialist, not a PR agent")
	}
}

// The tech specialist's prompt must (a) define its lane as enforcing the
// configured technology briefs, and (b) tell the model to no-op (empty
// findings) when no briefs are present or none of the configured tech changed.
func TestTechSpecialistPromptScope(t *testing.T) {
	body := readPromptFile(t, SpecTech)
	mustContain := []string{
		// Lane: it enforces the technology-conventions block.
		"## Technology conventions",
		// The unit-suffix example this specialist now owns.
		"717Mi",
		// No-op posture when there is nothing to enforce.
		"empty",
		"no technology conventions to enforce",
	}
	for _, marker := range mustContain {
		if !strings.Contains(body, marker) {
			t.Errorf("tech specialist prompt missing required marker %q", marker)
		}
	}
}

// The tech specialist is only an active specialist when technology experts are
// configured; otherwise it is excluded from the run set (no API call) and from
// the overlay (no empty tab). Every other specialist is always active.
func TestActiveSpecialistsGatesTechOnConfig(t *testing.T) {
	withTech := ActiveSpecialists(true)
	if len(withTech) != len(AllSpecialists) {
		t.Fatalf("with tech configured the active set should equal AllSpecialists; got %v", withTech)
	}
	if !containsSpecialist(withTech, SpecTech) {
		t.Fatalf("tech specialist should be active when configured")
	}

	withoutTech := ActiveSpecialists(false)
	if containsSpecialist(withoutTech, SpecTech) {
		t.Fatalf("tech specialist must be excluded when no tech experts are configured; got %v", withoutTech)
	}
	if len(withoutTech) != len(AllSpecialists)-1 {
		t.Fatalf("only the tech specialist should be dropped; got %v", withoutTech)
	}
	// Every universal specialist must survive the gating.
	for _, s := range []string{SpecFormatting, SpecDesign, SpecTesting, SpecDocs, SpecSecurity} {
		if !containsSpecialist(withoutTech, s) {
			t.Fatalf("universal specialist %q must always be active", s)
		}
	}

	// The returned slice must be a copy, not the shared AllSpecialists backing
	// array (mutating it must not corrupt the global).
	withTech[0] = "MUTATED"
	if AllSpecialists[0] == "MUTATED" {
		t.Fatalf("ActiveSpecialists must not alias AllSpecialists")
	}
}

func containsSpecialist(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

// HasUsableTechExperts is the shared predicate the TUI uses (and that mirrors
// the runner's gating) to decide whether the tech specialist is active.
func TestHasUsableTechExperts(t *testing.T) {
	// Redirect the tech-agents store to a throwaway location for this test.
	t.Setenv("APPR_AI_SAL_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	pr := &gh.PR{Owner: "o", Repo: "r", Number: 1}

	if HasUsableTechExperts(nil, nil) {
		t.Fatalf("nil PR must report no tech experts")
	}
	if HasUsableTechExperts(pr, &repoconfig.Config{TechAgents: false}) {
		t.Fatalf("tech experts toggled off must report none")
	}
	if HasUsableTechExperts(pr, &repoconfig.Config{TechAgents: true}) {
		t.Fatalf("no tech-agents file must report none")
	}
	if HasUsableTechExperts(pr, nil) {
		t.Fatalf("no tech-agents file must report none (nil rc)")
	}

	// A brief with no real content does not count as usable.
	if err := techagents.SaveAgent("o", "r", techagents.Agent{Tech: "kubernetes", Context: "   "}); err != nil {
		t.Fatalf("save empty brief: %v", err)
	}
	if HasUsableTechExperts(pr, &repoconfig.Config{TechAgents: true}) {
		t.Fatalf("an empty-content brief must not count as usable")
	}

	// A populated brief makes the tech specialist active.
	if err := techagents.SaveAgent("o", "r", techagents.Agent{Tech: "kubernetes", Context: "Use Mi suffixes for memory quantities."}); err != nil {
		t.Fatalf("save brief: %v", err)
	}
	if !HasUsableTechExperts(pr, &repoconfig.Config{TechAgents: true}) {
		t.Fatalf("a populated brief should report usable tech experts")
	}
	// The toggle still wins even with a populated brief.
	if HasUsableTechExperts(pr, &repoconfig.Config{TechAgents: false}) {
		t.Fatalf("the TechAgents toggle off must override a populated brief")
	}
}

// On a config-correctness line the tech specialist owns the keeper when it
// collides with a generalist lane (formatting/design), so its finding wins the
// cross-specialist dedupe.
func TestDedupeTechSpecialistWinsConfigLine(t *testing.T) {
	comment := "Kubernetes memory quantities use the binary Mi suffix, not M."
	specs := []SpecialistResult{
		{Specialist: SpecFormatting, Findings: []Finding{dedupeFinding(comment, "        memory: 717Mi")}},
		{Specialist: SpecDesign, Findings: []Finding{dedupeFinding(comment, "        memory: 717Mi")}},
		{Specialist: SpecTech, Findings: []Finding{dedupeFinding(comment, "        memory: 717Mi")}},
	}
	out := dedupeInlineFindingsAcrossSpecialists(specs)
	if got := countFindings(out); got != 1 {
		t.Fatalf("expected 1 finding after dedupe, got %d", got)
	}
	if len(findingsForSpecialist(out, SpecTech)) != 1 {
		t.Fatalf("tech specialist should keep the config-correctness finding over formatting/design")
	}
	for _, name := range []string{SpecFormatting, SpecDesign} {
		if len(findingsForSpecialist(out, name)) != 0 {
			t.Fatalf("%s duplicate should be dropped in favour of tech", name)
		}
	}
}
