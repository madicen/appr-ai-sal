package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review/conventionwitness"
)

func TestReviewCommentBodyAIAndAgent(t *testing.T) {
	body := ReviewCommentBody("formatting", Finding{
		Comment: "Use consistent naming.",
	})
	if !strings.Contains(body, "appr-ai-sal") || !strings.Contains(body, "formatting") || !strings.Contains(body, "AI-generated") {
		t.Fatalf("expected disclosure: %q", body)
	}
}

func TestReviewCommentBodyOmitsBadSuggestion(t *testing.T) {
	body := ReviewCommentBody("docs", Finding{
		Comment:    "Improve the doc.",
		Suggestion: "Improve the doc.",
	})
	if strings.Contains(body, "```suggestion") {
		t.Fatalf("should not attach suggestion when same as comment: %q", body)
	}
}

func TestRenderBodyLeadsWithMergeRecommendation(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "formatting",
			Summary:    "Looks fine.",
			Findings: []Finding{
				{Path: "a.go", Line: 1, Comment: "nit", Severity: SeverityInfo},
			},
		}},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Fix the handler validation before merge.",
			Prompts: []AuthorPrompt{
				{Title: "T", AgentPrompt: "do thing"},
			},
		},
	}
	body := d.RenderBody()
	iMerge := strings.Index(body, "### Merge recommendation")
	iPrompts := strings.Index(body, "### Suggested prompt for your AI assistant")
	if iMerge < 0 {
		t.Fatal("missing merge recommendation section")
	}
	if iPrompts < 0 {
		t.Fatal("missing suggested prompt section")
	}
	if !(iMerge < iPrompts) {
		t.Fatal("expected merge recommendation before suggested prompt")
	}
	if strings.Contains(body, "### formatting") {
		t.Fatal("inline-only specialists must not get per-agent headings in the body")
	}
	if !strings.Contains(body, "## Verdict: Request changes") {
		t.Fatalf("body should contain verdict heading: %s", body)
	}
	if strings.Count(body, "```text") != 1 {
		t.Fatalf("expected exactly one fenced prompt block, body: %s", body)
	}
}

func TestRenderBodyOmitsDuplicativeSummaryWhenInlinePresent(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "security",
			Summary:    "Long prose that repeats what inline comments say.",
			Findings: []Finding{
				{Path: "x.go", Line: 10, Comment: "fix leak", Severity: SeverityError},
			},
		}},
	}
	body := d.RenderBody()
	if strings.Contains(body, "Long prose that repeats") {
		t.Fatalf("specialist summary should be omitted when inline findings exist: %s", body)
	}
	if strings.Contains(body, "### security") {
		t.Fatalf("inline-only specialist should not appear as a section heading: %s", body)
	}
}

func TestRenderBodyOmitsSpecialistWithNoFindings(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: "formatting", Summary: "All good.", Findings: nil},
			{Specialist: "design", Summary: "", Findings: []Finding{
				{Path: "b.go", Line: 2, Comment: "issue", Severity: SeverityWarning},
			}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
	}
	body := d.RenderBody()
	if strings.Contains(body, "### formatting") || strings.Contains(body, "### design") {
		t.Fatalf("per-specialist agent headings should not appear: %s", body)
	}
}

func TestRenderBodyConsolidatesPRWideFromSeveralAgents(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: "docs", Findings: []Finding{
				{Path: "", Line: 0, Comment: "missing readme section", Severity: SeverityInfo},
			}},
			{Specialist: "security", Findings: []Finding{
				{Path: "", Line: 0, Comment: "audit vault paths", Severity: SeverityWarning},
			}},
		},
	}
	body := d.RenderBody()
	if strings.Count(body, "### PR-wide notes") != 1 {
		t.Fatalf("want one PR-wide section: %s", body)
	}
	if strings.Contains(body, "### docs") || strings.Contains(body, "### security") {
		t.Fatalf("no per-agent headings: %s", body)
	}
	if !strings.Contains(body, "info · docs:") || !strings.Contains(body, "warning · security:") {
		t.Fatalf("expected tagged bullets: %s", body)
	}
}

func TestRenderBodyCombinesMultiplePromptsIntoOneFence(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictApprove,
			Prompts: []AuthorPrompt{
				{Title: "First", AgentPrompt: "do A"},
				{Title: "Second", AgentPrompt: "do B"},
			},
		},
	}
	body := d.RenderBody()
	if strings.Count(body, "```text") != 1 {
		t.Fatalf("want one ```text fence: %s", body)
	}
	if !strings.Contains(body, "do A") || !strings.Contains(body, "do B") || !strings.Contains(body, "---") {
		t.Fatalf("expected combined body with separator: %s", body)
	}
	// Titles must appear with the "## " visual prefix inside the fenced
	// text block — markdown isn't rendered there, but the prefix gives a
	// clear topic boundary in the pasted prompt. Without it the title
	// becomes a bare line that's easy to miss.
	if !strings.Contains(body, "## First") || !strings.Contains(body, "## Second") {
		t.Fatalf("expected ## title prefixes inside the prompt block: %s", body)
	}
	// When 2+ topics are bundled, the disclosure above the fence must say
	// so explicitly with the count — the previous "If multiple topics
	// were bundled, sections are separated by ---" wording was vague and
	// only conditionally true.
	if !strings.Contains(body, "**2 distinct topics**") {
		t.Fatalf("expected 'distinct topics' count disclosure with N=2: %s", body)
	}
}

func TestRenderBodySingleTopicDisclosureOmitsTopicCount(t *testing.T) {
	// With exactly one prompt there are no `---` separators, so the
	// disclosure must NOT promise multiple topics — that would be false
	// to the reader.
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictApprove,
			Prompts: []AuthorPrompt{
				{Title: "Refactor handler", AgentPrompt: "do the refactor"},
			},
		},
	}
	body := d.RenderBody()
	if strings.Contains(body, "distinct topics") {
		t.Fatalf("single-prompt body should not mention 'distinct topics': %s", body)
	}
	if !strings.Contains(body, "Paste the fenced block below") {
		t.Fatalf("single-prompt body still needs the basic paste disclosure: %s", body)
	}
}

func TestRenderBodyTopicCountReflectsThreeSeparatePrompts(t *testing.T) {
	// This is the exact "refactor + README + CHANGELOG" pattern that
	// motivated this change. The author should see three distinct
	// sections in the fenced block, each separated by `---`, and the
	// disclosure should say "3 distinct topics".
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Three pieces of work.",
			Prompts: []AuthorPrompt{
				{Title: "Refactor discovery runner", AgentPrompt: "Refactor expandSeedsWithDiscovery in discovery_runner.go to return a channel."},
				{Title: "Update README discovery section", AgentPrompt: "Update the APP-790 section of README.md to describe the new behaviour."},
				{Title: "Add CHANGELOG entry", AgentPrompt: "Add a CHANGELOG.md entry describing the throughput improvement."},
			},
		},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "**3 distinct topics**") {
		t.Fatalf("three prompts should produce a '3 distinct topics' disclosure: %s", body)
	}
	if strings.Count(body, "\n---\n") < 2 {
		t.Fatalf("three prompts should produce two `---` separators: %s", body)
	}
	if !strings.Contains(body, "## Refactor discovery runner") ||
		!strings.Contains(body, "## Update README discovery section") ||
		!strings.Contains(body, "## Add CHANGELOG entry") {
		t.Fatalf("expected each topic title to appear with `## ` prefix: %s", body)
	}
}

func TestRenderBodyRequestChangesMissingPromptsWarning(t *testing.T) {
	// Use a general (PR-wide) error finding so the reconciliation pass
	// keeps the request_changes verdict — without an actual blocker the
	// verdict gets downgraded to comment and the warning becomes moot.
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecTesting, Findings: []Finding{
				{Path: "", Line: 0, Severity: SeverityError, Comment: "no tests added"},
			}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict:                      VibeVerdictRequestChanges,
			Summary:                      "Needs work.",
			RequestChangesWithoutPrompts: true,
		},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "**Warning:** Verdict is **request changes**, but no paste-ready AI prompts") {
		t.Fatalf("expected missing-prompts warning: %s", body)
	}
}

func TestRenderBodyReviewerChoicesDisclosure(t *testing.T) {
	d := &Draft{
		PR:               &gh.PR{HeadSHA: "abc"},
		UserSkipPostKeys: map[string]struct{}{"k1": {}, "k2": {}},
		VibeCoach:        &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "### Reviewer choices") {
		t.Fatalf("missing section: %s", body)
	}
	if !strings.Contains(body, "2 inline suggestions skipped") {
		t.Fatalf("expected plural disclosure: %s", body)
	}
}

func TestRenderBodyReviewerChoicesSingular(t *testing.T) {
	d := &Draft{
		PR:               &gh.PR{HeadSHA: "abc"},
		UserSkipPostKeys: map[string]struct{}{"only": {}},
		VibeCoach:        &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "1 inline suggestion skipped") {
		t.Fatalf("expected singular: %s", body)
	}
}

func TestRenderBodyNoFindingsTailoredBody(t *testing.T) {
	d := &Draft{PR: &gh.PR{HeadSHA: "abc"}}
	body := d.RenderBody()
	if !strings.Contains(body, "No issues found by any agent") {
		t.Fatalf("expected no-findings notice: %s", body)
	}
	if strings.Contains(body, "executive summary") {
		t.Fatalf("standard disclosure should be replaced when no findings: %s", body)
	}
	// The no-findings body is the most likely place for a future drift
	// to slip in wording that implies the AI itself "reviewed and
	// approved" the PR. Lock in the assist-the-human framing and the
	// "It recommends Approving" phrasing so a regression here trips a
	// test instead of shipping to GitHub.
	if !strings.Contains(body, "It recommends Approving this pull request.") {
		t.Fatalf("no-findings body should phrase the recommendation as the tool's, not as the verdict itself: %s", body)
	}
	// Disclaimer is now wrapped in a GitHub CAUTION alert so it renders
	// red on PR pages, with "not" bolded to draw the eye.
	if !strings.Contains(body, "replacement for manual review") {
		t.Fatalf("no-findings body should keep the human-reviewer disclaimer: %s", body)
	}
	if !strings.Contains(body, "> [!CAUTION]") {
		t.Fatalf("no-findings disclaimer should be wrapped in a CAUTION alert so it renders red: %s", body)
	}
	if !strings.Contains(body, "is **not** a replacement") {
		t.Fatalf("no-findings disclaimer should bold 'not' for emphasis: %s", body)
	}
	if !strings.Contains(body, "## appr-ai-sal summary") {
		t.Fatalf("body heading should say summary, not review: %s", body)
	}
	// Detection marker must remain so re-runs still recognise this
	// body as one previously posted by the tool.
	if !strings.Contains(body, "produced by **appr-ai-sal**") {
		t.Fatalf("AprrAISalReviewBodyMarker substring must remain in disclosure: %s", body)
	}
}

func TestRenderBodyStandardDisclosureFramesAsAssist(t *testing.T) {
	// The standard (with-findings) body must also frame appr-ai-sal as
	// an assistive tool — the verdict on the PR represents the human
	// reviewer's judgement, not the AI's. This is the most-posted body
	// shape, so the framing matters more here than in the no-findings
	// fallback.
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "design",
			Findings: []Finding{
				{Path: "a.go", Line: 1, Comment: "nit", Severity: SeverityInfo},
			},
		}},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "## appr-ai-sal summary") {
		t.Fatalf("body heading should say summary: %s", body)
	}
	if !strings.Contains(body, "to assist the human reviewer") {
		t.Fatalf("disclosure should describe appr-ai-sal as assistive: %s", body)
	}
	// Disclaimer is now wrapped in a GitHub CAUTION alert so it renders
	// red on PR pages, with "not" bolded to draw the eye.
	if !strings.Contains(body, "replacement for manual review") {
		t.Fatalf("disclosure should include the human-reviewer disclaimer: %s", body)
	}
	if !strings.Contains(body, "> [!CAUTION]") {
		t.Fatalf("standard disclaimer should be wrapped in a CAUTION alert so it renders red: %s", body)
	}
	if !strings.Contains(body, "is **not** a replacement") {
		t.Fatalf("standard disclaimer should bold 'not' for emphasis: %s", body)
	}
	if !strings.Contains(body, "produced by **appr-ai-sal**") {
		t.Fatalf("AprrAISalReviewBodyMarker substring must remain: %s", body)
	}
}

func TestRenderBodyShowsReconciliationNoteAfterDowngrade(t *testing.T) {
	skipped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{skipped}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges, Summary: "Needs work."},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, skipped): {},
		},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "## Verdict: Comment only") {
		t.Fatalf("body should display the reconciled verdict (Comment only): %s", body)
	}
	if strings.Contains(body, "## Verdict: Request changes") {
		t.Fatalf("body should not still claim Request changes after downgrade: %s", body)
	}
	if !strings.Contains(body, "Verdict downgraded from Request changes to Comment only") {
		t.Fatalf("body should explain the downgrade: %s", body)
	}
}

// TestRenderBodyVerdictHeadlineMatchesArbiterPanel pins the fix for the
// "Approve at the top, Request changes at the bottom" contradiction. When the
// arbiter sets a relaxing verdict_override (approve) but a blocking prompt
// survives, the override is clamped: the body headline, the arbiter panel
// adjustment line, and ReconciledMergeVerdict must all agree on Request
// changes — the headline must never show the clamped Approve.
func TestRenderBodyVerdictHeadlineMatchesArbiterPanel(t *testing.T) {
	finding := Finding{Path: "values.yaml", Line: 207, Side: "RIGHT", Severity: SeverityWarning, Comment: "Use the binary unit suffix Mi instead of M."}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDesign, Findings: []Finding{finding}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Correct the memory unit, then it's ready for merge.",
			Prompts: []AuthorPrompt{{
				Title:       "Fix memory unit suffix",
				AgentPrompt: "change M to Mi in values.yaml",
				FindingRefs: []FindingRef{{Specialist: SpecDesign, Path: "values.yaml", Line: 207}},
			}},
		},
		RepoArbiter: &RepoArbiterResult{
			UserSummary:      "Reviewed the resource limit change.",
			VerdictOverride:  VibeVerdictApprove,
			EffectiveVerdict: VibeVerdictApprove,
		},
	}

	if got := NormalizeVibeVerdict(d.ReconciledMergeVerdict()); got != VibeVerdictRequestChanges {
		t.Fatalf("reconciled verdict = %q, want request_changes (blocking prompt survives)", got)
	}

	body := d.RenderBody()
	rcLabel := VibeVerdictShortLabel(VibeVerdictRequestChanges)
	apLabel := VibeVerdictShortLabel(VibeVerdictApprove)
	if !strings.Contains(body, "## Verdict: "+rcLabel) {
		t.Fatalf("headline should be %q:\n%s", rcLabel, body)
	}
	if strings.Contains(body, "## Verdict: "+apLabel) {
		t.Fatalf("headline must not show the clamped Approve override:\n%s", body)
	}
	if !strings.Contains(body, "stays **"+rcLabel+"**") {
		t.Fatalf("arbiter panel should explain the override was clamped to %q:\n%s", rcLabel, body)
	}
}

// TestRenderBodyOmitsDemotionListAndWitnessTally locks in that the posted
// body never carries the non-actionable repo-arbiter process exhaust: the
// per-finding demotion list ("warning → info") and the convention-witness
// tally ("N congruent / divergent / unknown"). A demotion only re-grades a
// finding — it either still shows at its new severity or was dropped under
// the floor — so listing it gives the PR author nothing to act on, and the
// witness counts are an internal QA signal. The arbiter's plain-English
// summary and the suppressed-comment disclosure (location + reason) stay,
// because those are reader-facing.
func TestRenderBodyOmitsDemotionListAndWitnessTally(t *testing.T) {
	kept := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "keep"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{kept}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "Looks ok."},
		RepoArbiter: &RepoArbiterResult{
			UserSummary: "Repo prefers light docs; softened a batch of header asks.",
			Suppressed: []SuppressedFindingRef{
				{Specialist: SpecDocs, Path: "b.yml", Line: 1, Side: "RIGHT", Reason: "header pattern congruent with the codebase"},
			},
			Demoted: []DemotedFindingRef{
				{Specialist: SpecDocs, Path: "c.yml", Line: 1, Side: "RIGHT", From: SeverityWarning, To: SeverityInfo, Reason: "congruent"},
			},
		},
		ConventionWitness: []conventionwitness.Witness{
			{Specialist: SpecDocs, Path: "c.yml", Line: 1, Verdict: conventionwitness.VerdictCongruent},
		},
	}
	body := d.RenderBody()

	for _, banned := range []string{
		"Findings demoted by repo arbiter",
		"severity dropped one rank",
		"warning → info",
		"Convention witness",
		"classified",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("posted body should not contain non-actionable %q:\n%s", banned, body)
		}
	}

	// The reader-facing pieces of the panel survive.
	if !strings.Contains(body, "Repo prefers light docs") {
		t.Errorf("arbiter user summary should still be posted:\n%s", body)
	}
	if !strings.Contains(body, "Inline comments not posted") {
		t.Errorf("suppressed-comment disclosure should still be posted:\n%s", body)
	}
}

// A demoted PR-wide finding stays out of the posted body by default (the
// arbiter's no-block intent), but the reviewer can opt it in, after which it
// appears in a clearly-labelled "included despite demotion" section.
func TestRenderBodyIncludesOptedInDemotedPRWideFinding(t *testing.T) {
	kept := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "keep this one"}
	demoted := Finding{Path: "", Line: 0, Side: "RIGHT", Severity: SeverityInfo, Comment: "The description is empty."}
	newDraft := func() *Draft {
		return &Draft{
			PR: &gh.PR{HeadSHA: "abc"},
			Specialists: []SpecialistResult{
				{Specialist: SpecDocs, Findings: []Finding{kept}},
			},
			VibeCoach:   &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "ok"},
			RepoArbiter: &RepoArbiterResult{},
			DemotedHidden: []FlatFinding{
				{Specialist: SpecDescription, Finding: demoted},
			},
		}
	}

	// Default: not opted in → comment absent from the body.
	d := newDraft()
	if body := d.RenderBody(); strings.Contains(body, "The description is empty.") {
		t.Fatalf("demoted PR-wide finding should not post unless opted in:\n%s", body)
	}

	// Opt in → it appears under the demotion-inclusion section.
	d = newDraft()
	if !d.ToggleDemotedPosting(SpecDescription, demoted) {
		t.Fatalf("toggling a not-yet-included demoted finding should return true (now included)")
	}
	body := d.RenderBody()
	if !strings.Contains(body, "The description is empty.") {
		t.Fatalf("opted-in demoted PR-wide finding should be posted:\n%s", body)
	}
	if !strings.Contains(body, "included despite demotion") {
		t.Fatalf("opted-in demoted finding should sit under the demotion-inclusion section:\n%s", body)
	}

	// Toggling again removes it.
	if d.ToggleDemotedPosting(SpecDescription, demoted) {
		t.Fatalf("toggling an included demoted finding should return false (now excluded)")
	}
	if body := d.RenderBody(); strings.Contains(body, "The description is empty.") {
		t.Fatalf("excluded demoted PR-wide finding should not post:\n%s", body)
	}
}
