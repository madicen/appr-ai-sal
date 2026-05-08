package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests lock in the "default to providing a suggestion" framing across
// the review contract and each specialist prompt. They exist to prevent a
// future edit from accidentally weakening the contract back into the older
// "either a suggestion OR a comment, your choice" shape, which empirically
// caused the model to almost always pick the comment-only path.

func TestReviewOutputContractRequiresSuggestionByDefault(t *testing.T) {
	c := reviewOutputContract

	mustContain := []string{
		// Required JSON fields are still both documented.
		`"comment"`,
		`"suggestion"`,
		// The contract reframes suggestion as the default for local fixes,
		// not as an optional escape hatch.
		"SUGGESTION CONTRACT",
		"You MUST emit a non-empty",
		// And it spells out the worked example so the model has a template.
		"Worked example",
		// The actionability bar still requires a concrete comment on every
		// finding.
		"ACTIONABILITY BAR",
		// Anti-deletion / anti-duplication framing — the bug we just fixed
		// where the model anchored at an unrelated line and silently
		// deleted it while duplicating an adjacent declaration.
		"REPLACEMENT, NOT INSERTION",
		"REPLAYED verbatim",
		// Language-awareness — the model should not slap Go's `//` and
		// godoc framing onto every file.
		"LANGUAGE AWARENESS",
		"HCL is NOT Go",
		// At least one non-Go worked example so the contract isn't a
		// Go-only template.
		"Terraform/HCL",
	}
	for _, s := range mustContain {
		if !strings.Contains(c, s) {
			t.Errorf("reviewOutputContract is missing required marker %q (the suggestion-default framing may have regressed)", s)
		}
	}

	// Anti-regression: the old "satisfies ONE of A or B" framing made
	// suggestion feel optional. If it ever returns, the contract is back in
	// the failure mode the user asked us to fix.
	mustNotContain := []string{
		"satisfy ONE of",
		"ONE of these two bars",
	}
	for _, s := range mustNotContain {
		if strings.Contains(c, s) {
			t.Errorf("reviewOutputContract contains old A-or-B framing %q; suggestion is meant to be the default for local fixes, not an alternative to comment", s)
		}
	}
}

// TestVibeCoachContractRequiresOneTopicPerPrompt locks in the "one prompts
// entry per distinct topic" framing on the vibe-coach contract. The earlier
// "Prefer one top-level agent_prompt" wording produced run-on prompts that
// hid docs / changelog work inside what looked like a refactor instruction.
func TestVibeCoachContractRequiresOneTopicPerPrompt(t *testing.T) {
	c := vibeCoachOutputContract

	mustContain := []string{
		// New framing: one entry per distinct topic, not one entry that
		// bundles everything.
		"One prompts entry per distinct topic",
		// Cap is unchanged but must be re-stated in the new framing
		// (consolidate within a topic, never across topics).
		"never across topics",
		// Steps inside a single agent_prompt must be paragraph-broken
		// instead of run-on.
		"separate distinct steps with a blank line",
	}
	for _, s := range mustContain {
		if !strings.Contains(c, s) {
			t.Errorf("vibeCoachOutputContract is missing required marker %q (the one-topic-per-prompt framing may have regressed)", s)
		}
	}

	// Anti-regression: the older "Prefer one top-level agent_prompt that
	// covers everything" wording is what produced single-paragraph prompts
	// that bundled refactor + README + CHANGELOG into one wall of text.
	mustNotContain := []string{
		"Prefer one top-level agent_prompt",
		"bundles all required work",
	}
	for _, s := range mustNotContain {
		if strings.Contains(c, s) {
			t.Errorf("vibeCoachOutputContract contains old bundle-everything framing %q; that framing produced run-on prompts that hid docs work behind refactor work", s)
		}
	}
}

// TestVibeCoachPromptHasOneTopicPerPromptGuidance locks the same framing
// into the on-disk specialist prompt (which is what the model actually
// reads — the contract above is a JSON contract block).
func TestVibeCoachPromptHasOneTopicPerPromptGuidance(t *testing.T) {
	body := readPromptFile(t, "vibe-coach")

	mustContain := []string{
		"One topic per prompt",
		// The worked "three topics → three prompts" example is the
		// concrete pattern the model mimics; if it disappears the model
		// reverts to single-paragraph bundling.
		"three topics, three prompts",
		// Anti-pattern: chaining unrelated work in one prompt body.
		"Cramming unrelated work",
		// Step-separator guidance for the body of one agent_prompt.
		"blank line",
	}
	for _, s := range mustContain {
		if !strings.Contains(body, s) {
			t.Errorf("vibe-coach prompt is missing required marker %q (the one-topic-per-prompt framing may have regressed)", s)
		}
	}
}

func TestSpecialistPromptsDefaultToSuggestion(t *testing.T) {
	// Each specialist prompt must explicitly tell the model when it MUST
	// emit a suggestion. Without this nudge per-specialist, the model
	// reverts to comment-only because it's "safer".
	cases := []struct {
		name         string
		mustContain  []string // any of these substrings must appear
		caseExamples []string // at least one of these typical-case markers must appear
	}{
		{
			name: "formatting",
			mustContain: []string{
				"MUST",
				"suggestion",
			},
			caseExamples: []string{
				"Spelling and grammar",
				"single-line",
				"typical formatting",
			},
		},
		{
			name: "docs",
			mustContain: []string{
				"MUST",
				"suggestion",
				// Anti-regression: docs.md was Go-only and led the model to
				// produce godoc-style suggestions on Terraform / Python /
				// etc. files. Lock in non-Go coverage and the anchor-discipline
				// reminder that a wrong anchor deletes the anchored line.
				"Python",
				"Terraform",
				"reproduced verbatim",
			},
			caseExamples: []string{
				"declaration line",
				"file's own comment syntax",
				"Anchor at the declaration line itself",
			},
		},
		{
			name: "testing",
			mustContain: []string{
				"MUST",
				"suggestion",
			},
			caseExamples: []string{
				"table-driven",
				"sub-test",
				"drop-in",
			},
		},
		{
			name: "security",
			mustContain: []string{
				"MUST",
				"suggestion",
			},
			caseExamples: []string{
				"sha256",
				"parameterised",
				"safe equivalent",
			},
		},
		{
			name: "design",
			mustContain: []string{
				"suggestion",
			},
			caseExamples: []string{
				"contiguous rewrite",
				"early return",
				"function signature",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := readPromptFile(t, tc.name)

			for _, s := range tc.mustContain {
				if !strings.Contains(body, s) {
					t.Errorf("specialist prompt %q is missing required marker %q (the suggestion-default framing may have regressed)", tc.name, s)
				}
			}

			matched := false
			for _, s := range tc.caseExamples {
				if strings.Contains(body, s) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("specialist prompt %q is missing any of the typical-case examples %v that anchor when to suggest", tc.name, tc.caseExamples)
			}
		})
	}
}

// readPromptFile reads the on-disk prompt for a specialist. Tests run from
// the package directory, so this is a stable relative path; it deliberately
// bypasses SpecialistPrompt() to avoid being side-tracked by user overrides
// in the developer's $XDG_CONFIG_HOME.
func readPromptFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("prompts", name+".md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}
