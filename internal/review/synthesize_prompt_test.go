package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// TestRenderBodySynthesizesPromptForUncoveredPRWideError reproduces the
// screenshot scenario: vibe-coach emitted one prompt referencing only an
// inline finding; the repo arbiter suppressed that inline; the rendered
// review now has a request_changes verdict and an uncovered PR-wide
// testing error in the notes section, but no AI prompt to address it.
//
// The fallback synthesizer must produce a paste-ready prompt for the
// PR-wide finding so the author always has actionable AI guidance.
func TestRenderBodySynthesizesPromptForUncoveredPRWideError(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecTesting, Findings: []Finding{
				{
					Path:     "",
					Line:     0,
					Severity: SeverityError,
					Comment:  "New Terraform configurations lack any unit tests. Please ensure that each resource and policy has corresponding tests with edge cases, integration points, and validation.",
				},
				{
					Path:     "main.tf",
					Line:     12,
					Side:     "RIGHT",
					Severity: SeverityWarning,
					Comment:  "naming nit on this resource",
				},
			}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Tests are missing.",
			Prompts: []AuthorPrompt{
				{
					Title:       "Rename the resource",
					AgentPrompt: "Rename the resource in main.tf:12 to follow the naming convention.",
					FindingRefs: []FindingRef{
						{Specialist: SpecTesting, Path: "main.tf", Line: 12, Side: "RIGHT"},
					},
				},
			},
		},
		RepoArbiter: &RepoArbiterResult{
			EffectiveVerdict: VibeVerdictRequestChanges,
			Suppressed: []SuppressedFindingRef{
				{Specialist: SpecTesting, Path: "main.tf", Line: 12, Side: "RIGHT", Reason: "naming convention is conventional in this repo"},
			},
		},
	}
	d.RepoArbiter.suppressKeySet = map[string]struct{}{
		suppressionKey(SpecTesting, "main.tf", 12, "RIGHT"): {},
	}

	body := d.RenderBody()

	// Sanity: the model's only prompt should have been filtered out.
	if !strings.Contains(body, "dropped because") {
		t.Fatalf("expected the dropped-prompts disclosure since the model's only prompt referenced a suppressed inline finding:\n%s", body)
	}

	// The fallback prompt MUST appear in the fenced block, quoting the
	// specialist comment so the author has actionable text to paste.
	if !strings.Contains(body, "```text") {
		t.Fatalf("expected a fenced ```text block from the synthesizer:\n%s", body)
	}
	if !strings.Contains(body, "New Terraform configurations lack any unit tests") {
		t.Fatalf("synthesizer should quote the PR-wide finding's comment verbatim so the author's AI has the same context the human reviewer has:\n%s", body)
	}
	if !strings.Contains(body, "testing specialist") {
		t.Fatalf("synthesized prompt should name the specialist so the author knows where the instruction came from:\n%s", body)
	}

	// And the body must disclose that the prompt was auto-generated, so
	// the human reviewer doesn't mistake it for vibe-coach prose.
	if !strings.Contains(body, "auto-built from blocking findings") &&
		!strings.Contains(body, "built from blocking findings") {
		t.Fatalf("expected an explicit auto-generated disclosure above the fenced block:\n%s", body)
	}
}

// TestRenderBodyDoesNotSynthesizeWhenBlockerHasInlineSuggestion guards
// against double-actioning: a blocker that already has a one-click GitHub
// suggestion on the diff is self-actionable; synthesizing a paste-ready
// prompt for it would be noise.
func TestRenderBodyDoesNotSynthesizeWhenBlockerHasInlineSuggestion(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecSecurity, Findings: []Finding{
				{
					Path:       "x.go",
					Line:       7,
					Side:       "RIGHT",
					Severity:   SeverityError,
					Comment:    "MD5 is not collision-resistant.",
					Suggestion: "h := sha256.New()",
				},
			}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Security blocker addressed inline.",
		},
	}
	body := d.RenderBody()
	if strings.Contains(body, "auto-built from blocking findings") ||
		strings.Contains(body, "built from blocking findings") {
		t.Fatalf("inline blocker with a one-click suggestion should not be synthesized — it is already self-actionable:\n%s", body)
	}
}

// TestRenderBodyDoesNotSynthesizeWhenBlockerIsCoveredByVibeCoachPrompt
// guards against duplicating work: a blocker the vibe-coach already
// addressed in a surviving prompt should not get a fallback prompt too.
func TestRenderBodyDoesNotSynthesizeWhenBlockerIsCoveredByVibeCoachPrompt(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{
					Path:     "",
					Line:     0,
					Severity: SeverityError,
					Comment:  "README is missing the new flag's documentation.",
				},
			}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "README needs an update.",
			Prompts: []AuthorPrompt{
				{
					Title:       "Document the new flag in the README",
					AgentPrompt: "In README.md, add a Flags section entry for --foo describing the new behaviour.",
					FindingRefs: []FindingRef{
						{Specialist: SpecDocs, Path: "", Line: 0, Side: ""},
					},
				},
			},
		},
	}
	body := d.RenderBody()
	if strings.Contains(body, "built from blocking findings") {
		t.Fatalf("blocker already covered by a surviving vibe-coach prompt should not be synthesized:\n%s", body)
	}
	// And the model's PR-wide-ref prompt must survive isAuthorPromptAlive.
	if !strings.Contains(body, "Document the new flag in the README") {
		t.Fatalf("a vibe-coach prompt whose only finding_ref is a PR-wide finding should not be filtered out:\n%s", body)
	}
}

// TestIsAuthorPromptAliveKeepsPromptsBundlingPRWideFindings verifies the
// fix for the "PR-wide work disappears when its inline siblings are
// suppressed" failure mode: a prompt that bundles a suppressed inline
// finding AND a PR-wide finding survives because the PR-wide one is still
// rendered in the body.
func TestIsAuthorPromptAliveKeepsPromptsBundlingPRWideFindings(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "x.go", Line: 1, Side: "RIGHT", Severity: SeverityError, Comment: "inline doc gap"},
				{Path: "", Line: 0, Severity: SeverityError, Comment: "stale README section"},
			}},
		},
		RepoArbiter: &RepoArbiterResult{
			EffectiveVerdict: VibeVerdictRequestChanges,
			Suppressed: []SuppressedFindingRef{
				{Specialist: SpecDocs, Path: "x.go", Line: 1, Side: "RIGHT"},
			},
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "x.go", 1, "RIGHT"): {},
			},
		},
	}
	p := AuthorPrompt{
		Title:       "Fix docs",
		AgentPrompt: "Update the README and the inline doc.",
		FindingRefs: []FindingRef{
			{Specialist: SpecDocs, Path: "x.go", Line: 1, Side: "RIGHT"},
			{Specialist: SpecDocs, Path: "", Line: 0, Side: ""},
		},
	}
	if !isAuthorPromptAlive(d, p) {
		t.Fatalf("prompt bundling a suppressed inline AND a live PR-wide finding should survive — the PR-wide work is still rendered")
	}
}

// TestVibeCoachContractRequiresPRWideDedicatedAndCoverage locks in the
// two new contract clauses: PR-wide findings get their own prompt entry,
// and every blocking finding without an inline suggestion must appear in
// some prompt's finding_refs.
func TestVibeCoachContractRequiresPRWideDedicatedAndCoverage(t *testing.T) {
	c := vibeCoachOutputContract
	mustContain := []string{
		"PR-wide findings get their own dedicated prompts",
		"Coverage requirement",
		"appear in some prompt's finding_refs",
	}
	for _, s := range mustContain {
		if !strings.Contains(c, s) {
			t.Errorf("vibeCoachOutputContract is missing required marker %q (PR-wide / coverage framing may have regressed)", s)
		}
	}
}

// TestVibeCoachPromptHasPRWideAndCoverageGuidance is the on-disk-prompt
// counterpart to the contract test.
func TestVibeCoachPromptHasPRWideAndCoverageGuidance(t *testing.T) {
	body := readPromptFile(t, "vibe-coach")
	mustContain := []string{
		"PR-wide findings get their own dedicated prompts",
		"Cover every blocking finding",
		"safety net",
	}
	for _, s := range mustContain {
		if !strings.Contains(body, s) {
			t.Errorf("vibe-coach prompt is missing required marker %q (PR-wide / coverage framing may have regressed)", s)
		}
	}
}

// TestDroppedPromptsDisclosureIsGrammaticalForSinglePrompt covers the
// "_All 1 paste-ready follow-up prompt were dropped_" grammar bug — the
// previous wording produced a singular noun with a plural verb.
func TestDroppedPromptsDisclosureIsGrammaticalForSinglePrompt(t *testing.T) {
	// One vibe-coach prompt that references an inline finding the arbiter
	// suppressed; no PR-wide blockers, so the synthesizer adds nothing
	// and the disclosure is the only thing rendered besides the verdict.
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Findings: []Finding{
				{Path: "x.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "nit"},
			}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictComment,
			Summary: "One nit; nothing blocking.",
			Prompts: []AuthorPrompt{
				{
					Title:       "Fix nit",
					AgentPrompt: "Fix the nit at x.go:1.",
					FindingRefs: []FindingRef{
						{Specialist: SpecFormatting, Path: "x.go", Line: 1, Side: "RIGHT"},
					},
				},
			},
		},
		RepoArbiter: &RepoArbiterResult{
			EffectiveVerdict: VibeVerdictComment,
			Suppressed: []SuppressedFindingRef{
				{Specialist: SpecFormatting, Path: "x.go", Line: 1, Side: "RIGHT"},
			},
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecFormatting, "x.go", 1, "RIGHT"): {},
			},
		},
	}
	body := d.RenderBody()
	if strings.Contains(body, "1 paste-ready follow-up prompt were dropped") {
		t.Fatalf("singular-noun + plural-verb regression: 'were' should be 'was' when only 1 prompt was dropped:\n%s", body)
	}
	if !strings.Contains(body, "1 paste-ready follow-up prompt was") {
		t.Fatalf("expected singular phrasing 'was' when only 1 prompt was dropped:\n%s", body)
	}
}
