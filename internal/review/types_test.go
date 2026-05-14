package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestFindingOriginalSeverityReturnsDemotedSeverity(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{{
			Specialist: SpecTesting,
			Findings: []Finding{
				{Path: "a.go", Line: 10, Side: "RIGHT", Severity: SeverityWarning, Comment: "missing test"},
			},
		}},
		RepoArbiter: &RepoArbiterResult{
			demoteKeySet: map[string]Severity{
				suppressionKey(SpecTesting, "a.go", 10, "RIGHT"): SeverityError,
			},
		},
	}
	if !d.HasRepoExpertDemotions() {
		t.Fatalf("HasRepoExpertDemotions should be true when demoteKeySet is non-empty")
	}
	got, ok := d.FindingOriginalSeverity(SpecTesting, d.Specialists[0].Findings[0])
	if !ok {
		t.Fatalf("FindingOriginalSeverity should report a hit for a demoted finding")
	}
	if got != SeverityError {
		t.Fatalf("FindingOriginalSeverity got %q want %q", got, SeverityError)
	}
	miss := Finding{Path: "z.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning}
	if _, ok := d.FindingOriginalSeverity(SpecTesting, miss); ok {
		t.Fatalf("FindingOriginalSeverity should miss for non-demoted finding")
	}
}

func TestHasRepoExpertDemotionsHandlesNilArbiter(t *testing.T) {
	if (&Draft{}).HasRepoExpertDemotions() {
		t.Fatalf("HasRepoExpertDemotions should be false without an arbiter")
	}
}

func TestSuggestionPostsToGitHub(t *testing.T) {
	cases := []struct {
		name string
		f    Finding
		want bool
	}{
		{"empty", Finding{Suggestion: ""}, false},
		{"code", Finding{Comment: "fix typo", Suggestion: "return nil"}, true},
		{"same as comment", Finding{Comment: "hello", Suggestion: "hello"}, false},
		{"fenced", Finding{Comment: "x", Suggestion: "```go\nx\n```"}, false},
		{"huge", Finding{Comment: "x", Suggestion: strings.Repeat("a", 9000)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SuggestionPostsToGitHub(tc.f); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

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

func TestToReviewOnlyInlineFindings(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "design",
			Findings: []Finding{
				{Path: "a.go", Line: 10, Comment: "inline", Severity: SeverityWarning},
				{Path: "", Line: 0, Comment: "general only", Severity: SeverityInfo},
			},
		}},
	}
	rev := d.ToReview()
	if len(rev.Comments) != 1 {
		t.Fatalf("comments: got %d want 1", len(rev.Comments))
	}
	if rev.Comments[0].Path != "a.go" || rev.Comments[0].Line != 10 {
		t.Fatalf("unexpected inline: %#v", rev.Comments[0])
	}
	if !strings.Contains(rev.Body, "### PR-wide notes") {
		t.Fatalf("body should have consolidated PR-wide section: %s", rev.Body)
	}
	if !strings.Contains(rev.Body, "general only") {
		t.Fatalf("body should include general finding: %s", rev.Body)
	}
	if !strings.Contains(rev.Body, "info · design:") {
		t.Fatalf("body should tag specialist on PR-wide bullet: %s", rev.Body)
	}
}

func TestNormalizeVibeVerdict(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"approve", VibeVerdictApprove},
		{"REQUEST_CHANGES", VibeVerdictRequestChanges},
		{"comment_only", VibeVerdictComment},
		{"", ""},
		{"maybe", ""},
	}
	for _, tc := range cases {
		if got := NormalizeVibeVerdict(tc.in); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.in, got, tc.want)
		}
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

func TestSpecialistsHaveAnyFindings(t *testing.T) {
	if SpecialistsHaveAnyFindings(nil) {
		t.Fatal("nil slice should be false")
	}
	if SpecialistsHaveAnyFindings([]SpecialistResult{}) {
		t.Fatal("empty slice should be false")
	}
	if SpecialistsHaveAnyFindings([]SpecialistResult{{Specialist: "x", Findings: []Finding{}}}) {
		t.Fatal("no findings should be false")
	}
	if !SpecialistsHaveAnyFindings([]SpecialistResult{{Specialist: "x", Findings: []Finding{{Path: "a.go", Line: 1, Comment: "x"}}}}) {
		t.Fatal("one finding should be true")
	}
}

func TestFindingIsInlinePostable(t *testing.T) {
	if !findingIsInlinePostable(Finding{Path: "x.go", Line: 1, Comment: "ok"}) {
		t.Fatal("expected postable")
	}
	if findingIsInlinePostable(Finding{Path: "", Line: 0, Comment: "g"}) {
		t.Fatal("general should not be postable")
	}
	if findingIsInlinePostable(Finding{Path: "x.go", Line: 1, Comment: "  "}) {
		t.Fatal("empty comment not postable")
	}
}

func TestEffectiveReviewEventAndBodySelfAuthorDowngradesVerdict(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "octocat", HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Fix validation.",
		},
	}
	ev, body, intent := EffectiveReviewEventAndBody(d, "REQUEST_CHANGES", "@OctoCat")
	if ev != "COMMENT" || intent != "REQUEST_CHANGES" {
		t.Fatalf("event/intent: got %q / %q want COMMENT / REQUEST_CHANGES", ev, intent)
	}
	if !strings.Contains(body, "does not allow") || !strings.Contains(body, "Fix validation.") {
		t.Fatalf("expected preamble + summary: %s", body)
	}

	ev2, body2, intent2 := EffectiveReviewEventAndBody(d, "APPROVE", "octocat")
	if ev2 != "COMMENT" || intent2 != "APPROVE" {
		t.Fatalf("approve event/intent: got %q / %q want COMMENT / APPROVE", ev2, intent2)
	}
	if !strings.Contains(body2, "does not allow") || !strings.Contains(body2, "## appr-ai-sal summary") {
		t.Fatalf("expected preamble + full summary for self-approve: %s", body2)
	}
}

func TestEffectiveReviewEventAndBodyOtherReviewerUnchanged(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "alice", HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Fix validation.",
		},
	}
	ev, body, intent := EffectiveReviewEventAndBody(d, "REQUEST_CHANGES", "bob")
	if ev != "REQUEST_CHANGES" || intent != "REQUEST_CHANGES" {
		t.Fatalf("event/intent: got %q / %q want REQUEST_CHANGES / REQUEST_CHANGES", ev, intent)
	}
	if strings.Contains(body, "does not allow") {
		t.Fatalf("should not add self-PR preamble: %s", body)
	}
	if !strings.Contains(body, "Fix validation.") {
		t.Fatalf("expected summary in body: %s", body)
	}
}

func TestEffectiveReviewEventAndBodySelfCommentUnchanged(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "alice", HeadSHA: "abc"},
	}
	ev, body, intent := EffectiveReviewEventAndBody(d, "COMMENT", "alice")
	if ev != "COMMENT" || intent != "COMMENT" {
		t.Fatalf("event/intent: got %q / %q want COMMENT / COMMENT", ev, intent)
	}
	if strings.Contains(body, "does not allow") {
		t.Fatalf("COMMENT should not get self-PR preamble: %s", body)
	}
}

func TestSpecialistsForVibeCoachRemovesSuppressedInlines(t *testing.T) {
	specs := []SpecialistResult{
		{Specialist: SpecDocs, Findings: []Finding{
			{Path: "e.go", Line: 1, Comment: "drop", Severity: SeverityInfo},
			{Path: "e.go", Line: 2, Comment: "keep inline", Severity: SeverityInfo},
			{Path: "", Line: 0, Comment: "general", Severity: SeverityInfo},
		}},
	}
	d := &Draft{
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "e.go", 1, "RIGHT"): {},
			},
		},
	}
	out := SpecialistsForVibeCoach(d, specs)
	if len(out[0].Findings) != 2 {
		t.Fatalf("want 2 findings after strip, got %d: %#v", len(out[0].Findings), out[0].Findings)
	}
	if out[0].Findings[0].Comment != "keep inline" || out[0].Findings[1].Comment != "general" {
		t.Fatalf("unexpected findings: %#v", out[0].Findings)
	}
	if len(specs[0].Findings) != 3 {
		t.Fatal("SpecialistsForVibeCoach must not mutate input slice")
	}
}

func TestSpecialistsForVibeCoachNilDraftUnchanged(t *testing.T) {
	specs := []SpecialistResult{{Specialist: SpecDocs, Findings: []Finding{{Path: "a.go", Line: 1}}}}
	out := SpecialistsForVibeCoach(nil, specs)
	if len(out) != 1 || len(out[0].Findings) != 1 {
		t.Fatalf("got %#v", out)
	}
}

func TestFlatPostableFindingsForPostUserSkipKeys(t *testing.T) {
	fSkip := Finding{Path: "e.go", Line: 1, Severity: SeverityInfo, Comment: "skip"}
	fPost := Finding{Path: "e.go", Line: 2, Severity: SeverityInfo, Comment: "post"}
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{fSkip, fPost}},
		},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, fSkip): {},
		},
	}
	post := d.FlatPostableFindingsForPost()
	if len(post) != 1 || post[0].Finding.Line != 2 {
		t.Fatalf("got %#v", post)
	}
}

func TestFlatPostableFindingsForPostArbiterAndUserSkip(t *testing.T) {
	a := Finding{Path: "a.go", Line: 1, Severity: SeverityInfo, Comment: "arb"}
	b := Finding{Path: "b.go", Line: 1, Severity: SeverityInfo, Comment: "user"}
	c := Finding{Path: "c.go", Line: 1, Severity: SeverityInfo, Comment: "stay"}
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{a, b, c}},
		},
		RepoArbiter: &RepoArbiterResult{
			suppressKeySet: map[string]struct{}{
				suppressionKey(SpecDocs, "a.go", 1, "RIGHT"): {},
			},
		},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, b): {},
		},
	}
	post := d.FlatPostableFindingsForPost()
	if len(post) != 1 || post[0].Finding.Path != "c.go" {
		t.Fatalf("got %#v", post)
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

func TestHasNoFindings(t *testing.T) {
	cases := []struct {
		name string
		d    *Draft
		want bool
	}{
		{
			name: "nil draft",
			d:    nil,
			want: false,
		},
		{
			name: "empty draft",
			d:    &Draft{PR: &gh.PR{HeadSHA: "abc"}},
			want: true,
		},
		{
			name: "vibe-coach verdict only, no prompts",
			d: &Draft{
				PR:        &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment},
			},
			want: true,
		},
		{
			name: "inline finding present",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				Specialists: []SpecialistResult{
					{Specialist: SpecDocs, Findings: []Finding{
						{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo},
					}},
				},
			},
			want: false,
		},
		{
			name: "general finding only",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				Specialists: []SpecialistResult{
					{Specialist: SpecDocs, Findings: []Finding{
						{Path: "", Line: 0, Comment: "pr-wide", Severity: SeverityInfo},
					}},
				},
			},
			want: false,
		},
		{
			name: "agent failure",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				Specialists: []SpecialistResult{
					{Specialist: SpecDocs, Err: fmtErrorf("boom")},
				},
			},
			want: false,
		},
		{
			name: "vibe-coach prompt present",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{
					Verdict: VibeVerdictComment,
					Prompts: []AuthorPrompt{{Title: "T", AgentPrompt: "do x"}},
				},
			},
			want: false,
		},
		{
			name: "repo arbiter suppression present",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				RepoArbiter: &RepoArbiterResult{
					Suppressed: []SuppressedFindingRef{{Specialist: SpecDocs, Path: "a.go", Line: 1}},
				},
			},
			want: false,
		},
		{
			name: "vibe-coach summary is content",
			d: &Draft{
				PR: &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{
					Verdict: VibeVerdictApprove,
					Summary: "Looks good, ship it.",
				},
			},
			want: false,
		},
		{
			name: "vibe-coach request_changes verdict alone",
			d: &Draft{
				PR:        &gh.PR{HeadSHA: "abc"},
				VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges},
			},
			want: false,
		},
		{
			name: "repo arbiter user summary",
			d: &Draft{
				PR:          &gh.PR{HeadSHA: "abc"},
				RepoArbiter: &RepoArbiterResult{UserSummary: "Note from experts."},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.HasNoFindings(); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
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
	if !strings.Contains(body, "not a replacement for manual review") {
		t.Fatalf("no-findings body should keep the human-reviewer disclaimer: %s", body)
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
	if !strings.Contains(body, "not a replacement for manual review") {
		t.Fatalf("disclosure should include the human-reviewer disclaimer: %s", body)
	}
	if !strings.Contains(body, "produced by **appr-ai-sal**") {
		t.Fatalf("AprrAISalReviewBodyMarker substring must remain: %s", body)
	}
}

func TestRenderBodyForEventApproveNoFindingsPostsBody(t *testing.T) {
	d := &Draft{PR: &gh.PR{HeadSHA: "abc"}}
	body := d.RenderBodyForEvent("APPROVE")
	if body == "" {
		t.Fatal("expected non-empty body for APPROVE when no findings")
	}
	if !strings.Contains(body, "No issues found by any agent") {
		t.Fatalf("expected no-findings notice in APPROVE body: %s", body)
	}
}

func TestRenderBodyForEventApproveWithFindingsEmpty(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo},
			}},
		},
	}
	if body := d.RenderBodyForEvent("APPROVE"); body != "" {
		t.Fatalf("APPROVE with findings should still post empty body: %q", body)
	}
}

// fmtErrorf is a tiny helper kept local to types_test.go so the no-findings
// test can construct an error without dragging in the fmt import everywhere.
func fmtErrorf(s string) error {
	return errSimple(s)
}

type errSimple string

func (e errSimple) Error() string { return string(e) }

func TestReconciledMergeVerdictDowngradesWhenAllInlinesSkipped(t *testing.T) {
	skipped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip me"}
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
	if got := d.EffectiveMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("EffectiveMergeVerdict = %q want request_changes", got)
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictComment {
		t.Fatalf("ReconciledMergeVerdict = %q want comment", got)
	}
	if got := d.PostEvent(); got != "COMMENT" {
		t.Fatalf("PostEvent = %q want COMMENT", got)
	}
	note := d.VerdictReconciliationNote()
	if !strings.Contains(note, "downgraded") || !strings.Contains(note, "Request changes") {
		t.Fatalf("VerdictReconciliationNote should explain the downgrade, got: %s", note)
	}
}

func TestReconciledMergeVerdictKeepsRequestChangesWithGeneralErrors(t *testing.T) {
	// Mirror the user's screenshot: every inline got skipped/suppressed but
	// PR-wide error severities remain — the verdict should hold.
	skipped := Finding{Path: "a.go", Line: 1, Side: "RIGHT", Severity: SeverityWarning, Comment: "skip"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				skipped,
				{Path: "", Line: 0, Severity: SeverityError, Comment: "README missing entries"},
			}},
			{Specialist: SpecTesting, Findings: []Finding{
				{Path: "", Line: 0, Severity: SeverityError, Comment: "no tests added"},
			}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictRequestChanges, Summary: "Block."},
		UserSkipPostKeys: map[string]struct{}{
			FindingSuppressionKey(SpecDocs, skipped): {},
		},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("ReconciledMergeVerdict = %q want request_changes (PR-wide error blockers remain)", got)
	}
	if note := d.VerdictReconciliationNote(); note != "" {
		t.Fatalf("VerdictReconciliationNote should be empty when no downgrade happened, got: %s", note)
	}
}

func TestReconciledMergeVerdictKeepsRequestChangesWithSurvivingPrompt(t *testing.T) {
	live := Finding{Path: "b.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning, Comment: "stays"}
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{live}},
		},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Block.",
			Prompts: []AuthorPrompt{
				{Title: "Keep me", AgentPrompt: "do it",
					FindingRefs: []FindingRef{{Specialist: SpecDocs, Path: "b.go", Line: 2}}},
			},
		},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("ReconciledMergeVerdict = %q want request_changes (surviving prompt is a blocker)", got)
	}
}

func TestReconciledMergeVerdictKeepsRequestChangesWhenArbiterOverrode(t *testing.T) {
	d := &Draft{
		PR:        &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove},
		RepoArbiter: &RepoArbiterResult{
			VerdictOverride:  VibeVerdictRequestChanges,
			EffectiveVerdict: VibeVerdictRequestChanges,
			UserSummary:      "Repo arbiter says block.",
		},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictRequestChanges {
		t.Fatalf("ReconciledMergeVerdict = %q want request_changes (arbiter override is a blocker)", got)
	}
}

func TestReconciledMergeVerdictPassesThroughNonRequestChanges(t *testing.T) {
	d := &Draft{
		PR:        &gh.PR{HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove, Summary: "ok"},
	}
	if got := d.ReconciledMergeVerdict(); got != VibeVerdictApprove {
		t.Fatalf("ReconciledMergeVerdict = %q want approve (no downgrade for non-request-changes)", got)
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
