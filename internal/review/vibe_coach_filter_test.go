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
