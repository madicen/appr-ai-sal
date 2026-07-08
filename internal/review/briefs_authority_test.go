package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/techagents"
)

// TestSpecialistPromptsCalibrateAgainstBriefs locks in the "do not file
// findings that contradict the briefs" framing across all five
// code-reviewing specialist prompts. The framing is the only thing that
// turns the injected language / technology / repo briefs from background
// context into ground truth — losing it sends the specialists back to
// treating their generic priors as authoritative.
//
// The expected markers are intentionally specific phrases (not generic
// vibes) so a future edit that softens the language back into
// "consider" / "you may want to" is caught.
func TestSpecialistPromptsCalibrateAgainstBriefs(t *testing.T) {
	cases := []struct {
		specialist string
		// Every specialist must name the three brief section headers so
		// the model knows what to look for in the user message. Testing
		// and docs additionally cite "Repo evidence for this PR".
		extraSections []string
	}{
		{specialist: SpecDesign},
		{specialist: SpecSecurity},
		{specialist: SpecFormatting},
		{specialist: SpecTesting, extraSections: []string{"`## Repo evidence for this PR`"}},
		{specialist: SpecDocs, extraSections: []string{"`## Repo evidence for this PR`"}},
	}

	mustContain := []string{
		// Section heading naming the calibration block so a reader of
		// the prompt can find it. The exact wording is shared across
		// all five specialists.
		"## Calibrating against the repo briefs",
		// The three brief section headers the prompt asks the model to
		// re-read before emitting findings.
		"`## Language conventions`",
		"`## Technology conventions`",
		"`## Repository context`",
		// The hard rule: this is the phrase that turns the briefs from
		// "context" into "ground truth". If this line goes away the
		// audit's "weakest link" comes back.
		"Do not file findings that contradict the briefs",
		// Severity calibration is the second job of the briefs — the
		// prompts must say so explicitly.
		"calibrate",
		// The narrower-scope-wins precedence so the model doesn't get
		// stuck when two briefs disagree.
		"Narrower scope wins",
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.specialist, func(t *testing.T) {
			p, err := SpecialistPrompt(tc.specialist)
			if err != nil {
				t.Fatalf("load specialist %q: %v", tc.specialist, err)
			}
			for _, marker := range mustContain {
				if !strings.Contains(p, marker) {
					t.Errorf("specialist %q prompt is missing required marker %q (the brief-authority framing may have regressed)", tc.specialist, marker)
				}
			}
			for _, marker := range tc.extraSections {
				if !strings.Contains(p, marker) {
					t.Errorf("specialist %q prompt should also cite %q in its calibration block", tc.specialist, marker)
				}
			}
		})
	}
}

// TestFormatRepoContextSectionCarriesAuthorityPreamble guards the
// "do not file findings that contradict" preamble that rides with every
// non-empty `## Repository context` block. Without this the per-(repo,
// specialist) brief is just labelled markdown the model is free to
// ignore.
func TestFormatRepoContextSectionCarriesAuthorityPreamble(t *testing.T) {
	out := FormatRepoContextSection("body lives here")
	mustContain := []string{
		"## Repository context",
		// The repo-specific authority claim — mirrors the language
		// brief's framing in langagents/brief.go.
		"do not file findings that contradict the conventions stated here",
		// Severity calibration — paired with the authority claim so the
		// model knows the briefs do two jobs.
		"calibrate the severity",
		// The body must still be present after the preamble.
		"body lives here",
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("FormatRepoContextSection output missing marker %q in:\n%s", m, out)
		}
	}

	if got := FormatRepoContextSection(""); got != "" {
		t.Errorf("empty body should produce empty section, got %q", got)
	}
	if got := FormatRepoContextSection("   \n\t\n"); got != "" {
		t.Errorf("whitespace-only body should produce empty section, got %q", got)
	}
}

// TestComposeTechSectionCarriesAuthorityPreamble verifies that
// composeTechSection prepends a "Technology conventions" preamble with
// the same "do not contradict" framing as the language and repo briefs.
// Without this the per-tech blocks render as bare context blobs that the
// model treats as informational.
func TestComposeTechSectionCarriesAuthorityPreamble(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")

	owner, repo := "acme", "widget"
	if err := techagents.SaveAgent(owner, repo, techagents.Agent{
		Tech:    "kestra",
		Label:   "Kestra",
		Context: "kestra body",
	}); err != nil {
		t.Fatalf("seed kestra: %v", err)
	}

	pr := &gh.PR{Owner: owner, Repo: repo, Number: 1, Title: "x", Repository: owner + "/" + repo}
	rc := repoconfig.Default()

	out := make(chan Progress, 2)
	got := composeTechSection(pr, rc, out)
	close(out)

	mustContain := []string{
		// The new top-level heading that names this section as
		// conventions (authoritative), not just "context".
		"## Technology conventions",
		// The authority claim — same wording shape as the language and
		// repo briefs so all three feel uniform to the model.
		"do not file findings that contradict the conventions stated here",
		// The per-tech block must still appear below the preamble so
		// the model can read the actual body.
		"## Technology context: Kestra",
		"kestra body",
	}
	for _, m := range mustContain {
		if !strings.Contains(got, m) {
			t.Errorf("composeTechSection output missing marker %q in:\n%s", m, got)
		}
	}

	// Ordering sanity: preamble must come before the per-tech blocks,
	// otherwise the "do not contradict" framing reads as an afterthought.
	idxPreamble := strings.Index(got, "## Technology conventions")
	idxBody := strings.Index(got, "## Technology context: Kestra")
	if idxPreamble < 0 || idxBody < 0 || idxPreamble >= idxBody {
		t.Errorf("preamble must precede per-tech blocks: preamble=%d body=%d", idxPreamble, idxBody)
	}
}

// TestBuildReviewUserPromptEmitsBriefsReReadReminder verifies the
// post-diff "re-check the brief section(s) above" stanza is present
// (a) only when at least one brief section is present, (b) names exactly
// the sections that were injected, and (c) sits between the diff and the
// JSON output contract. Position matters: the diff dominates attention,
// so the reminder is the last thing the model reads before producing
// findings.
func TestBuildReviewUserPromptEmitsBriefsReReadReminder(t *testing.T) {
	pr := &gh.PR{Number: 1, Title: "x", Repository: "acme/widget", BaseRef: "main", HeadRef: "feat"}

	t.Run("no briefs, no reminder", func(t *testing.T) {
		got := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced, "", "", "", "", "")
		if strings.Contains(got, "Before emitting findings") {
			t.Errorf("reminder should be omitted when no briefs are present:\n%s", got)
		}
	})

	t.Run("all four briefs, all four named", func(t *testing.T) {
		got := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced,
			"REPO_BRIEF",
			"EVIDENCE_BRIEF",
			"",
			"## Language: Go\n\nLANG",
			"## Technology context: Kestra\n\nTECH",
		)
		mustContain := []string{
			"Before emitting findings",
			"`## Language conventions`",
			"`## Technology conventions`",
			"`## Repository context`",
			"`## Repo evidence for this PR`",
			// The "authoritative" framing must be in the reminder too,
			// not just in the section preambles, because the model's
			// last read before JSON output is this stanza.
			"authoritative",
		}
		for _, m := range mustContain {
			if !strings.Contains(got, m) {
				t.Errorf("reminder missing marker %q in:\n%s", m, got)
			}
		}

		// Position: reminder must come AFTER the closing diff fence and
		// BEFORE the output contract (the JSON instructions). If it
		// drifts above the diff the diff drowns it out; if it drifts
		// into the contract the model will treat it as part of the JSON
		// spec.
		idxDiffClose := strings.LastIndex(got, "\n```\n\n")
		idxReminder := strings.Index(got, "Before emitting findings")
		idxContract := strings.Index(got, "Return your review as a single JSON object")
		if idxDiffClose < 0 || idxReminder < 0 || idxContract < 0 {
			t.Fatalf("missing one of the anchors: diff=%d reminder=%d contract=%d", idxDiffClose, idxReminder, idxContract)
		}
		if !(idxDiffClose < idxReminder && idxReminder < idxContract) {
			t.Errorf("reminder must sit between diff and output contract: diff=%d reminder=%d contract=%d", idxDiffClose, idxReminder, idxContract)
		}
	})

	t.Run("only lang and tech are named when only those are present", func(t *testing.T) {
		got := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced,
			"",
			"",
			"",
			"## Language: Go\n\nLANG",
			"## Technology context: Kestra\n\nTECH",
		)
		if !strings.Contains(got, "Before emitting findings") {
			t.Fatalf("reminder should appear when lang+tech are present:\n%s", got)
		}
		if !strings.Contains(got, "`## Language conventions`") {
			t.Errorf("reminder should name language conventions")
		}
		if !strings.Contains(got, "`## Technology conventions`") {
			t.Errorf("reminder should name technology conventions")
		}
		if strings.Contains(got, "`## Repository context`") {
			t.Errorf("reminder must NOT name repository context when none was injected")
		}
		if strings.Contains(got, "`## Repo evidence for this PR`") {
			t.Errorf("reminder must NOT name repo evidence when none was injected")
		}
	})
}
