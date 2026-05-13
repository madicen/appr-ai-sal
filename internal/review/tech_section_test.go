package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review/techagents"
)

// TestBuildReviewUserPromptOrdersLangThenTechThenRepo verifies that the
// three context blocks injected into a specialist prompt show up in the
// fixed broadest-to-narrowest order: language conventions → technology
// experts → per-specialist repo-agent brief. Anything else risks the
// repo-specific deltas being shadowed by tech defaults further down.
func TestBuildReviewUserPromptOrdersLangThenTechThenRepo(t *testing.T) {
	pr := &gh.PR{Number: 1, Title: "x", Repository: "acme/widget", BaseRef: "main", HeadRef: "feat"}
	got := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced,
		"REPO_BRIEF_BODY",
		"",
		"## Language: Go\n\nLANG_BODY",
		"## Technology context: Kestra\n\nTECH_BODY",
	)
	idxLang := strings.Index(got, "LANG_BODY")
	idxTech := strings.Index(got, "TECH_BODY")
	idxRepo := strings.Index(got, "REPO_BRIEF_BODY")
	if idxLang < 0 || idxTech < 0 || idxRepo < 0 {
		t.Fatalf("missing one of the section bodies: lang=%d tech=%d repo=%d in:\n%s", idxLang, idxTech, idxRepo, got)
	}
	if !(idxLang < idxTech && idxTech < idxRepo) {
		t.Fatalf("ordering wrong: lang=%d tech=%d repo=%d (must be lang < tech < repo)", idxLang, idxTech, idxRepo)
	}
}

// TestBuildReviewUserPromptHasContextIntroOnTechAlone proves that having
// a tech section is enough to flip the intro to the "context inlined"
// variant, even with no lang or repo brief. Without this the augment-for-provider
// pass would prepend the wrong tooling-hint variant for non-Claude
// providers.
func TestBuildReviewUserPromptHasContextIntroOnTechAlone(t *testing.T) {
	pr := &gh.PR{Number: 1, Title: "x", Repository: "acme/widget", BaseRef: "main", HeadRef: "feat"}
	got := buildReviewUserPrompt(pr, "diff", aiconfig.ReviewBalanced,
		"",
		"",
		"",
		"## Technology context: Kestra\n\nTECH",
	)
	if !strings.Contains(got, claudeReviewIntro) {
		t.Fatalf("tech-only context should pick the with-context intro, got:\n%s", got[:min(160, len(got))])
	}
	if strings.Contains(got, claudeReviewIntroNoRepo) {
		t.Fatalf("tech-only context must NOT keep the no-context intro")
	}
}

// TestComposeTechSectionEmitsInjectedDetail verifies that the runner's
// composeTechSection helper produces a labelled markdown block per tech,
// emits one Progress event reporting which techs were injected, and
// returns the empty string when the toggle is disabled. The overlay
// relies on this exact Detail format ("injected k+t") to populate the
// Context-injection row.
func TestComposeTechSectionEmitsInjectedDetail(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")

	owner, repo := "acme", "widget"
	if err := techagents.SaveAgent(owner, repo, techagents.Agent{
		Tech:    "kestra",
		Label:   "Kestra",
		Context: "kestra body lives under flows/",
	}); err != nil {
		t.Fatalf("seed kestra: %v", err)
	}
	if err := techagents.SaveAgent(owner, repo, techagents.Agent{
		Tech:    "terraform",
		Label:   "Terraform",
		Context: "terraform modules under infra/",
	}); err != nil {
		t.Fatalf("seed terraform: %v", err)
	}

	pr := &gh.PR{Owner: owner, Repo: repo, Number: 1, Title: "x", Repository: owner + "/" + repo}
	rc := repoconfig.Default()

	out := make(chan Progress, 4)
	got := composeTechSection(pr, rc, out)
	close(out)

	if !strings.Contains(got, "## Technology context: Kestra") {
		t.Fatalf("tech section missing kestra header:\n%s", got)
	}
	if !strings.Contains(got, "## Technology context: Terraform") {
		t.Fatalf("tech section missing terraform header:\n%s", got)
	}
	if !strings.Contains(got, "kestra body lives under flows/") {
		t.Fatalf("tech section missing kestra body:\n%s", got)
	}

	var p Progress
	select {
	case p = <-out:
	default:
		t.Fatal("expected one Progress event on the channel")
	}
	if p.Stage != "tech-agents" {
		t.Fatalf("stage: got %q want %q", p.Stage, "tech-agents")
	}
	if !strings.HasPrefix(p.Detail, "injected ") {
		t.Fatalf("detail should start with 'injected ', got %q", p.Detail)
	}
	// Both labels should appear in the detail (sort order is alphabetical
	// since SortedTechs sorts by canonical key).
	if !strings.Contains(p.Detail, "Kestra") || !strings.Contains(p.Detail, "Terraform") {
		t.Fatalf("detail should mention both techs, got %q", p.Detail)
	}
}

func TestComposeTechSectionDisabledEmitsDisabled(t *testing.T) {
	pr := &gh.PR{Owner: "acme", Repo: "widget", Number: 1, Title: "x", Repository: "acme/widget"}
	rc := repoconfig.Default()
	rc.TechAgents = false

	out := make(chan Progress, 1)
	got := composeTechSection(pr, rc, out)
	close(out)
	if got != "" {
		t.Fatalf("disabled toggle should produce empty section, got %q", got)
	}
	p := <-out
	if p.Stage != "tech-agents" || p.Detail != "disabled" {
		t.Fatalf("expected tech-agents/disabled, got %+v", p)
	}
}

func TestComposeTechSectionNoneEmitsNone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("APPR_AI_SAL_CACHE_DIR", "")

	pr := &gh.PR{Owner: "acme", Repo: "widget", Number: 1, Title: "x", Repository: "acme/widget"}
	rc := repoconfig.Default()
	out := make(chan Progress, 1)
	got := composeTechSection(pr, rc, out)
	close(out)
	if got != "" {
		t.Fatalf("no techs configured should produce empty section, got %q", got)
	}
	p := <-out
	if p.Stage != "tech-agents" || p.Detail != "none" {
		t.Fatalf("expected tech-agents/none, got %+v", p)
	}
}

func TestComposeTechSectionNilPRNoEvent(t *testing.T) {
	out := make(chan Progress, 1)
	got := composeTechSection(nil, repoconfig.Default(), out)
	close(out)
	if got != "" {
		t.Fatalf("nil pr should produce empty section, got %q", got)
	}
	if _, ok := <-out; ok {
		t.Fatal("nil pr should not emit a Progress event")
	}
}
