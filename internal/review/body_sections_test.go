package review

import (
	"errors"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestComposeBodyNilOverridesMatchesLegacyRenderBody(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: "design", Findings: []Finding{
				{Path: "a.go", Line: 1, Comment: "nit", Severity: SeverityInfo},
			}},
			{Specialist: "security", Err: errors.New("boom"), Outcome: OutcomeFailed},
			{Specialist: "docs", Findings: []Finding{
				{Path: "", Line: 0, Comment: "readme gap", Severity: SeverityWarning},
			}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "Mostly fine."},
	}
	got := ComposeBody(d.BodySections(), nil)
	if strings.Count(got, "> [!CAUTION]") != 1 {
		t.Fatalf("want exactly one CAUTION:\n%s", got)
	}
	if !strings.HasPrefix(got, "## appr-ai-sal summary — 💬 Comment only") {
		t.Fatalf("headline:\n%s", got)
	}
	if !strings.Contains(got, "**1 inline comment** on the diff · **1 PR-wide note** below") {
		t.Fatalf("stats:\n%s", got)
	}
	if !strings.Contains(got, "### PR-wide notes") || !strings.Contains(got, "readme gap") {
		t.Fatalf("PR-wide note missing:\n%s", got)
	}
	if got != d.RenderBody() {
		t.Fatalf("RenderBody differs from ComposeBody(nil):\n%s\n---\n%s", got, d.RenderBody())
	}
}

func TestComposeBodyOverrides(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: "docs", Findings: []Finding{
				{Comment: "first gap", Severity: SeverityWarning},
				{Comment: "second gap", Severity: SeverityWarning},
			}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "Mostly fine."},
	}
	secs := d.BodySections()
	ids := map[string]bool{}
	for _, s := range secs {
		ids[s.ID] = true
	}
	for _, id := range []string{"headline", "caution", "stats", "prwide:docs:0", "prwide:docs:1"} {
		if !ids[id] {
			t.Fatalf("missing section %q in %v", id, ids)
		}
	}

	// Fixed caution cannot be excluded or edited.
	got := ComposeBody(secs, map[string]BodySectionOverride{
		"caution": {Included: false, Edited: "gone"},
	})
	if !strings.Contains(got, "> [!CAUTION]") || strings.Contains(got, "gone") {
		t.Fatalf("caution must be fixed:\n%s", got)
	}

	// Excluding one note keeps the heading; excluding both drops it.
	got = ComposeBody(secs, map[string]BodySectionOverride{
		"prwide:docs:0": {Included: false},
	})
	if !strings.Contains(got, "### PR-wide notes") || strings.Contains(got, "first gap") || !strings.Contains(got, "second gap") {
		t.Fatalf("partial exclude:\n%s", got)
	}
	got = ComposeBody(secs, map[string]BodySectionOverride{
		"prwide:docs:0": {Included: false},
		"prwide:docs:1": {Included: false},
	})
	if strings.Contains(got, "### PR-wide notes") {
		t.Fatalf("heading should be dropped:\n%s", got)
	}

	// Edited replaces the fragment.
	got = ComposeBody(secs, map[string]BodySectionOverride{
		"prwide:docs:1": {Included: true, Edited: "- custom note\n"},
	})
	if !strings.Contains(got, "- custom note") || strings.Contains(got, "second gap") {
		t.Fatalf("edit:\n%s", got)
	}
}

func TestComposeBodyPromptTopics(t *testing.T) {
	d := &Draft{
		PR:          &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{Specialist: "design", Findings: []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityWarning}}}},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictComment,
			Prompts: []AuthorPrompt{
				{Title: "One", AgentPrompt: "do one"},
				{Title: "Two", AgentPrompt: "do two"},
			},
		},
	}
	secs := d.BodySections()
	got := ComposeBody(secs, nil)
	if !strings.Contains(got, "**2 distinct topics**") || !strings.Contains(got, "## One\n\ndo one\n\n---\n\n## Two\n\ndo two") {
		t.Fatalf("both topics:\n%s", got)
	}
	got = ComposeBody(secs, map[string]BodySectionOverride{"prompt:0": {Included: false}})
	if strings.Contains(got, "distinct topics") || strings.Contains(got, "do one") || !strings.Contains(got, "## Two") {
		t.Fatalf("one topic:\n%s", got)
	}
	got = ComposeBody(secs, map[string]BodySectionOverride{
		"prompt:0": {Included: false},
		"prompt:1": {Included: false},
	})
	if strings.Contains(got, "Prompt for your AI assistant") || strings.Contains(got, "```text") {
		t.Fatalf("no topics should omit heading/fence:\n%s", got)
	}
}

func TestComposeBodyExcludesPromptTopic(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "docs",
			Findings:   []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityError}},
		}},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Needs work.",
			Prompts: []AuthorPrompt{
				{Title: "One", AgentPrompt: "do one"},
				{Title: "Two", AgentPrompt: "do two"},
			},
		},
	}
	sections := d.BodySections()
	var promptIDs []string
	for _, s := range sections {
		if s.Kind == BodySectionPromptTopic {
			promptIDs = append(promptIDs, s.ID)
		}
	}
	if len(promptIDs) < 2 {
		t.Fatalf("need 2 prompt topics, got %v", promptIDs)
	}
	ov := map[string]BodySectionOverride{
		promptIDs[0]: {Included: false},
	}
	body := ComposeBody(sections, ov)
	if strings.Contains(body, "do one") {
		t.Fatalf("excluded topic still present:\n%s", body)
	}
	if !strings.Contains(body, "do two") {
		t.Fatalf("kept topic missing:\n%s", body)
	}
	if !strings.Contains(body, "### 🤖 Prompt for your AI assistant") {
		t.Fatalf("prompt wrapper missing:\n%s", body)
	}
}

func TestComposeBodyExcludesAllPromptTopicsOmitsWrapper(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "docs",
			Findings:   []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityError}},
		}},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Needs work.",
			Prompts: []AuthorPrompt{
				{Title: "One", AgentPrompt: "do one"},
				{Title: "Two", AgentPrompt: "do two"},
			},
		},
	}
	sections := d.BodySections()
	ov := map[string]BodySectionOverride{}
	for _, s := range sections {
		if s.Kind == BodySectionPromptTopic {
			ov[s.ID] = BodySectionOverride{Included: false}
		}
	}
	body := ComposeBody(sections, ov)
	if strings.Contains(body, "### 🤖 Prompt for your AI assistant") {
		t.Fatalf("prompt heading should be omitted:\n%s", body)
	}
	if strings.Contains(body, "```text") {
		t.Fatalf("prompt fence should be omitted:\n%s", body)
	}
	if strings.Contains(body, "do one") || strings.Contains(body, "do two") {
		t.Fatalf("excluded topics still present:\n%s", body)
	}
}

func TestComposeBodyExcludesPRWideNote(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "docs",
			Findings: []Finding{
				{Comment: "first gap", Severity: SeverityWarning},
				{Comment: "second gap", Severity: SeverityWarning},
			},
		}},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "Mostly fine."},
	}
	sections := d.BodySections()
	body := ComposeBody(sections, map[string]BodySectionOverride{
		"prwide:docs:0": {Included: false},
	})
	if !strings.Contains(body, "### PR-wide notes") {
		t.Fatalf("heading should remain:\n%s", body)
	}
	if strings.Contains(body, "first gap") {
		t.Fatalf("excluded bullet still present:\n%s", body)
	}
	if !strings.Contains(body, "second gap") {
		t.Fatalf("kept bullet missing:\n%s", body)
	}
}

func TestComposeBodyEditOverride(t *testing.T) {
	d := &Draft{
		PR:          &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{Specialist: "docs", Findings: []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo}}}},
		VibeCoach:   &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "Original summary."},
	}
	sections := d.BodySections()
	ov := map[string]BodySectionOverride{
		"headline": {Included: true, Edited: "## appr-ai-sal summary — 💬 Comment only\n\nEdited TL;DR.\n\n"},
	}
	body := ComposeBody(sections, ov)
	if !strings.Contains(body, "Edited TL;DR.") || strings.Contains(body, "Original summary.") {
		t.Fatalf("edit not applied:\n%s", body)
	}
}

func TestComposeBodyCautionAlwaysIncluded(t *testing.T) {
	d := &Draft{
		PR:          &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{Specialist: "docs", Findings: []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo}}}},
		VibeCoach:   &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "S"},
	}
	ov := map[string]BodySectionOverride{
		"caution": {Included: false, Edited: "nope"},
	}
	body := ComposeBody(d.BodySections(), ov)
	if !strings.Contains(body, "> [!CAUTION]") || !strings.Contains(body, "produced by **appr-ai-sal**") {
		t.Fatalf("caution must remain:\n%s", body)
	}
	if strings.Contains(body, "nope") {
		t.Fatalf("caution edit must be ignored:\n%s", body)
	}
}

func TestBodySectionsPRWideIDsArePerSpecialist(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: "docs", Findings: []Finding{{Comment: "d0", Severity: SeverityWarning}}},
			{Specialist: "design", Findings: []Finding{{Comment: "g0", Severity: SeverityWarning}, {Comment: "g1", Severity: SeverityWarning}}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "ok"},
	}
	ids := map[string]bool{}
	for _, s := range d.BodySections() {
		ids[s.ID] = true
	}
	for _, id := range []string{"prwide:docs:0", "prwide:design:0", "prwide:design:1"} {
		if !ids[id] {
			t.Errorf("missing per-specialist id %q in %v", id, ids)
		}
	}
}
