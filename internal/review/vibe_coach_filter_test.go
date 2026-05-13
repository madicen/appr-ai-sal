package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// TestRenderBodyDropsPromptWhenAllRefsSuppressed verifies that a vibe-coach
// prompt whose every finding_ref was suppressed by the repo arbiter is
// dropped from the rendered body, and that the human reader is told why.
func TestRenderBodyDropsPromptWhenAllRefsSuppressed(t *testing.T) {
	suppressed := Finding{Path: "a.go", Line: 10, Side: "RIGHT", Severity: SeverityWarning, Comment: "drop me"}
	live := Finding{Path: "b.go", Line: 20, Side: "RIGHT", Severity: SeverityWarning, Comment: "stays"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{suppressed, live}},
		},
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "a.go", 10, "RIGHT"): {},
			},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Address the docs notes.",
			Prompts: []AuthorPrompt{
				{Title: "Fix suppressed thing", AgentPrompt: "do A",
					FindingRefs: []FindingRef{{Specialist: SpecDocs, Path: "a.go", Line: 10}}},
				{Title: "Fix surviving thing", AgentPrompt: "do B",
					FindingRefs: []FindingRef{{Specialist: SpecDocs, Path: "b.go", Line: 20}}},
			},
		},
	}
	body := d.RenderBody()
	if strings.Contains(body, "do A") {
		t.Errorf("dropped prompt should not appear in body: %s", body)
	}
	if !strings.Contains(body, "do B") {
		t.Errorf("surviving prompt should appear in body: %s", body)
	}
	if !strings.Contains(body, "Fix suppressed thing") {
		t.Errorf("disclosure should name the dropped prompt by title, got: %s", body)
	}
	if !strings.Contains(body, "1 paste-ready follow-up prompt was dropped") {
		t.Errorf("disclosure should say '1 paste-ready follow-up prompt was dropped' (singular noun + singular verb), got: %s", body)
	}
	if strings.Contains(body, "1 paste-ready follow-up prompt were dropped") {
		t.Errorf("singular-noun + plural-verb regression: 'were' should be 'was', got: %s", body)
	}
	if !strings.Contains(body, "the inline findings they pointed to were all suppressed or skipped") {
		t.Errorf("disclosure should make the inline-only scope explicit, got: %s", body)
	}
}

// TestRenderBodyDropsPromptWhenAllRefsUserSkipped covers the same path for
// user-skipped findings (TUI approval flow) so the published summary doesn't
// recommend AI work for things the reviewer chose not to post.
func TestRenderBodyDropsPromptWhenAllRefsUserSkipped(t *testing.T) {
	skipped := Finding{Path: "a.go", Line: 10, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecSecurity, Findings: []Finding{skipped}},
		},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecSecurity, skipped): {},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictApprove,
			Summary: "ok",
			Prompts: []AuthorPrompt{
				{Title: "Address skipped finding", AgentPrompt: "do thing",
					FindingRefs: []FindingRef{{Specialist: SpecSecurity, Path: "a.go", Line: 10}}},
			},
		},
	}
	body := d.RenderBody()
	if strings.Contains(body, "do thing") {
		t.Errorf("user-skipped-only prompt should be dropped: %s", body)
	}
	if !strings.Contains(body, "The 1 paste-ready follow-up prompt was dropped") {
		t.Errorf("expected singular 'The 1 paste-ready follow-up prompt was dropped' disclosure, got: %s", body)
	}
	if strings.Contains(body, "follow-up prompt were dropped") {
		t.Errorf("singular-noun + plural-verb regression in the 'all dropped' branch, got: %s", body)
	}
	if !strings.Contains(body, "the inline findings they pointed to were all suppressed by the repo arbiter or skipped during review") {
		t.Errorf("disclosure should make the inline-only scope explicit, got: %s", body)
	}
	if !strings.Contains(body, "The verdict above is based on the broader review") {
		t.Errorf("disclosure should reassure the reader the verdict is based on the broader review, got: %s", body)
	}
}

// TestRenderBodyKeepsPromptWithMixedRefs ensures the renderer keeps a prompt
// when at least one finding_ref still survives — partial suppression must
// not silently drop a prompt that still has a live finding.
func TestRenderBodyKeepsPromptWithMixedRefs(t *testing.T) {
	dead := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "dead"}
	alive := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "alive"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDesign, Findings: []Finding{dead, alive}},
		},
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDesign, "a.go", 1, "RIGHT"): {},
			},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Prompts: []AuthorPrompt{
				{Title: "Both", AgentPrompt: "do both",
					FindingRefs: []FindingRef{
						{Specialist: SpecDesign, Path: "a.go", Line: 1},
						{Specialist: SpecDesign, Path: "b.go", Line: 2},
					}},
			},
		},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "do both") {
		t.Errorf("prompt with at least one live ref must be kept: %s", body)
	}
	if strings.Contains(body, "follow-up prompt") && strings.Contains(body, "dropped") {
		t.Errorf("no drop disclosure expected when nothing was dropped: %s", body)
	}
}

// TestRenderBodyKeepsLegacyPromptWithoutRefs guards against a regression: an
// AuthorPrompt without finding_refs (legacy output, or general advice) must
// keep rendering even when there are arbiter suppressions on the draft.
func TestRenderBodyKeepsLegacyPromptWithoutRefs(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "x.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "x"},
			}},
		},
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "x.go", 1, "RIGHT"): {},
			},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictApprove,
			Prompts: []AuthorPrompt{
				{Title: "General", AgentPrompt: "general advice"},
			},
		},
	}
	body := d.RenderBody()
	if !strings.Contains(body, "general advice") {
		t.Errorf("legacy prompt without finding_refs must still render: %s", body)
	}
}

// TestRenderBodyOmitsLegacyEditFooter is the regression test for the
// "Edit before posting. Each inline comment states its agent in the comment
// text." footer line, which the user asked us to remove.
func TestRenderBodyOmitsLegacyEditFooter(t *testing.T) {
	d := &Draft{
		PR:        &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
	}
	body := d.RenderBody()
	for _, banned := range []string{"Edit before posting", "Each inline comment states its agent"} {
		if strings.Contains(body, banned) {
			t.Errorf("body should no longer contain %q, got:\n%s", banned, body)
		}
	}
}

// TestRenderBodyOmitsDroppedSuppressionsDisclosure regresses the change that
// stopped rendering the arbiter's DroppedSuppressions list in the GitHub
// review body. The internal field stays populated (other code may surface
// it in debug logs / tests), but the public review summary should never
// expose internal suppression-key shapes like "specialist|path|line|side".
func TestRenderBodyOmitsDroppedSuppressionsDisclosure(t *testing.T) {
	d := &Draft{
		PR:        &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
		RepoArbiter: &RepoArbiterResult{
			UserSummary: "Repo arbiter ran.",
			DroppedSuppressions: []string{
				"no matching inline finding: testing|relative/path.go|42|RIGHT",
				"cannot suppress security finding: security|x.go|1|RIGHT",
			},
		},
	}
	body := d.RenderBody()
	for _, banned := range []string{
		"Some suppression requests were not applied",
		"guardrails",
		"no matching inline finding",
		"cannot suppress security finding",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("body should no longer contain %q, got:\n%s", banned, body)
		}
	}
	// Sanity: the field itself is still present on the in-memory draft so
	// debug/test paths can read it.
	if len(d.RepoArbiter.DroppedSuppressions) != 2 {
		t.Errorf("DroppedSuppressions field should remain populated for tests/logs; got %d", len(d.RepoArbiter.DroppedSuppressions))
	}
}

// TestIsAuthorPromptAliveEmptyRefs documents that a prompt with no refs is
// treated as alive (general advice / legacy output) regardless of draft
// state — the unit-level guarantee behind TestRenderBodyKeepsLegacyPromptWithoutRefs.
func TestIsAuthorPromptAliveEmptyRefs(t *testing.T) {
	if !isAuthorPromptAlive(nil, AuthorPrompt{Title: "x"}) {
		t.Fatal("nil draft + no refs: should be alive")
	}
	if !isAuthorPromptAlive(&Draft{}, AuthorPrompt{Title: "x"}) {
		t.Fatal("empty draft + no refs: should be alive")
	}
}

// TestSpecialistsForVibeCoachDropsArbiterSuppressed regresses the
// long-standing behaviour that arbiter-suppressed inline findings never
// make it into the vibe-coach prompt input.
func TestSpecialistsForVibeCoachDropsArbiterSuppressed(t *testing.T) {
	dropped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "drop"}
	kept := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "keep"}
	specialists := []SpecialistResult{
		{Specialist: SpecDocs, Findings: []Finding{dropped, kept}},
	}
	d := &Draft{
		Specialists: specialists,
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "a.go", 1, "RIGHT"): {},
			},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if len(out) != 1 || len(out[0].Findings) != 1 {
		t.Fatalf("expected 1 specialist with 1 kept finding, got %+v", out)
	}
	if out[0].Findings[0].Comment != "keep" {
		t.Errorf("kept finding lost: %+v", out[0].Findings)
	}
}

// TestSpecialistsForVibeCoachDropsUserSkipped is the new behaviour:
// user-skipped findings (TUI approval flow) must also be excluded from
// the vibe-coach input so the LLM-generated Summary, Prompts, and
// Verdict reflect only the findings the reviewer is actually going to
// ship. Mirrors the post-skip filtering in
// FlatPostableFindingsForPost / RenderBody's filterAuthorPrompts.
func TestSpecialistsForVibeCoachDropsUserSkipped(t *testing.T) {
	skipped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip-me"}
	kept := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "keep-me"}
	specialists := []SpecialistResult{
		{Specialist: SpecSecurity, Findings: []Finding{skipped, kept}},
	}
	d := &Draft{
		Specialists: specialists,
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecSecurity, skipped): {},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if len(out) != 1 || len(out[0].Findings) != 1 {
		t.Fatalf("expected 1 specialist with 1 kept finding (user skip should drop the other), got %+v", out)
	}
	if out[0].Findings[0].Comment != "keep-me" {
		t.Errorf("kept finding lost: %+v", out[0].Findings)
	}
}

// TestSpecialistsForVibeCoachDropsBoth covers the both-filters case:
// arbiter suppression and user skip apply independently, dropping
// every inline finding that hits either filter.
func TestSpecialistsForVibeCoachDropsBoth(t *testing.T) {
	suppressedByArbiter := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "arb"}
	skippedByUser := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "user"}
	kept := Finding{Path: "c.go", Line: 3, Side: "RIGHT", Severity: SeverityWarning, Comment: "live"}
	specialists := []SpecialistResult{
		{Specialist: SpecDesign, Findings: []Finding{suppressedByArbiter, skippedByUser, kept}},
	}
	d := &Draft{
		Specialists: specialists,
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDesign, "a.go", 1, "RIGHT"): {},
			},
		},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDesign, skippedByUser): {},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if len(out) != 1 || len(out[0].Findings) != 1 || out[0].Findings[0].Comment != "live" {
		t.Errorf("expected only 'live' to survive, got %+v", out)
	}
}

// TestSpecialistsForVibeCoachPreservesPRWideFindings ensures that
// findings with empty Path / Line 0 (PR-wide notes) are never filtered:
// they have no suppression key and the skip flow only targets inline
// cards.
func TestSpecialistsForVibeCoachPreservesPRWideFindings(t *testing.T) {
	prWide := Finding{Path: "", Line: 0, Severity: SeverityWarning, Comment: "global"}
	inlineSkipped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "drop"}
	specialists := []SpecialistResult{
		{Specialist: SpecDocs, Findings: []Finding{prWide, inlineSkipped}},
	}
	d := &Draft{
		Specialists: specialists,
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, inlineSkipped): {},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if len(out) != 1 || len(out[0].Findings) != 1 || out[0].Findings[0].Comment != "global" {
		t.Errorf("PR-wide finding should always survive; got %+v", out)
	}
}

// TestSpecialistsForVibeCoachShortCircuitsWhenNoFilters confirms the
// fast path: when neither arbiter suppressions nor user skips exist,
// the original slice is returned untouched (cheap pointer-identity
// check).
func TestSpecialistsForVibeCoachShortCircuitsWhenNoFilters(t *testing.T) {
	specialists := []SpecialistResult{
		{Specialist: SpecDocs, Findings: []Finding{
			{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "x"},
		}},
	}
	d := &Draft{Specialists: specialists}
	out := SpecialistsForVibeCoach(d, specialists)
	if &out[0] != &specialists[0] {
		t.Errorf("expected the fast path to return the same slice header, got a copy")
	}
}

// TestFilterAuthorPromptsTitlesEmpty covers the disclosure formatting when
// the LLM returned a prompt with no Title.
func TestFilterAuthorPromptsTitlesEmpty(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{}},
		},
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "a.go", 1, "RIGHT"): {},
			},
		},
	}
	prompts := []AuthorPrompt{{
		Title: "", AgentPrompt: "do",
		FindingRefs: []FindingRef{{Specialist: SpecDocs, Path: "a.go", Line: 1}},
	}}
	kept, dropped := filterAuthorPrompts(d, prompts)
	if len(kept) != 0 {
		t.Errorf("expected prompt to be dropped, kept=%d", len(kept))
	}
	if len(dropped) != 1 || dropped[0] != "untitled" {
		t.Errorf("expected dropped titles=[untitled], got %v", dropped)
	}
}

// TestSpecialistsForVibeCoachClearsSummaryWhenAllFindingsSuppressed is the
// regression test for the bug where the vibe-coach re-surfaced an
// arbiter-suppressed finding via the specialist's aggregate Summary text.
// The specialist's Summary typically describes the very findings the
// arbiter dropped ("Found inconsistent label naming throughout"), so
// leaving it intact lets the vibe-coach LLM generate paste-ready prompts
// for findings the reviewer already decided not to ship.
func TestSpecialistsForVibeCoachClearsSummaryWhenAllFindingsSuppressed(t *testing.T) {
	suppressed := Finding{Path: "a.yaml", Line: 10, Side: "RIGHT", Severity: SeverityWarning, Comment: "snake_case label"}
	specialists := []SpecialistResult{
		{
			Specialist: SpecFormatting,
			Summary:    "Found inconsistent label naming throughout the metadata.",
			Findings:   []Finding{suppressed},
		},
	}
	d := &Draft{
		Specialists: specialists,
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecFormatting, "a.yaml", 10, "RIGHT"): {},
			},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if len(out) != 1 {
		t.Fatalf("want 1 specialist entry, got %d", len(out))
	}
	if len(out[0].Findings) != 0 {
		t.Errorf("want 0 surviving findings, got %d: %#v", len(out[0].Findings), out[0].Findings)
	}
	if out[0].Summary != "" {
		t.Errorf("want Summary cleared (because every finding was filtered), got %q", out[0].Summary)
	}
	if specialists[0].Summary == "" {
		t.Error("must not mutate input slice's Summary")
	}
}

// TestSpecialistsForVibeCoachClearsSummaryOnPartialSuppression locks in the
// stricter posture: even when a single inline finding is filtered out of
// a multi-finding specialist, we clear the Summary. The aggregate
// Summary may name dropped findings ("issues with X and Y", where Y was
// suppressed); the surviving inline findings already speak for
// themselves, so dropping the Summary is the safer default than risking
// the leak the user reported.
func TestSpecialistsForVibeCoachClearsSummaryOnPartialSuppression(t *testing.T) {
	drop := Finding{Path: "a.yaml", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "drop"}
	keep := Finding{Path: "a.yaml", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "keep"}
	specialists := []SpecialistResult{
		{
			Specialist: SpecFormatting,
			Summary:    "Found 2 formatting issues: bad label naming and indentation drift.",
			Findings:   []Finding{drop, keep},
		},
	}
	d := &Draft{
		Specialists: specialists,
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecFormatting, "a.yaml", 1, "RIGHT"): {},
			},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if len(out[0].Findings) != 1 || out[0].Findings[0].Comment != "keep" {
		t.Fatalf("expected only 'keep' to survive, got %+v", out[0].Findings)
	}
	if out[0].Summary != "" {
		t.Errorf("want Summary cleared even on partial suppression, got %q", out[0].Summary)
	}
}

// TestSpecialistsForVibeCoachKeepsSummaryWhenNothingFiltered confirms the
// no-op case: when nothing is dropped (e.g. all findings are PR-wide and
// therefore not filterable), the specialist's Summary survives so the
// vibe-coach still has its aggregate context.
func TestSpecialistsForVibeCoachKeepsSummaryWhenNothingFiltered(t *testing.T) {
	prWide := Finding{Path: "", Line: 0, Severity: SeverityWarning, Comment: "global"}
	specialists := []SpecialistResult{
		{
			Specialist: SpecDesign,
			Summary:    "One PR-wide design note.",
			Findings:   []Finding{prWide},
		},
	}
	d := &Draft{
		Specialists: specialists,
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				// Key targets a different specialist; this specialist
				// has nothing inline to suppress.
				suppressionKey(SpecFormatting, "x.go", 1, "RIGHT"): {},
			},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if out[0].Summary != "One PR-wide design note." {
		t.Errorf("Summary should survive when no findings were dropped, got %q", out[0].Summary)
	}
}

// TestSpecialistsForVibeCoachKeepsSummaryForFailedSpecialist guards the
// s.Err early-continue: a failed specialist's Summary (typically empty,
// but possibly populated by partial output) should not be touched by the
// filter — its findings are not iterated at all.
func TestSpecialistsForVibeCoachKeepsSummaryForFailedSpecialist(t *testing.T) {
	specialists := []SpecialistResult{
		{
			Specialist: SpecDocs,
			Summary:    "partial output before failure",
			Err:        fmtErrorf("boom"),
		},
	}
	d := &Draft{
		Specialists: specialists,
		UserSkipPostKeys: map[string]struct{}{
			// Force the function to take the filtering branch.
			"docs|x.go|1|RIGHT": {},
		},
	}
	out := SpecialistsForVibeCoach(d, specialists)
	if out[0].Summary != "partial output before failure" {
		t.Errorf("failed-specialist Summary must be preserved, got %q", out[0].Summary)
	}
}
