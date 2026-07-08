package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestAllPRAgentsAndMembership(t *testing.T) {
	want := []string{SpecDescription, SpecChecks, SpecDiscussion, SpecScope}
	if len(AllPRAgents) != len(want) {
		t.Fatalf("AllPRAgents = %v, want %v", AllPRAgents, want)
	}
	for i, n := range want {
		if AllPRAgents[i] != n {
			t.Fatalf("AllPRAgents[%d] = %q want %q", i, AllPRAgents[i], n)
		}
		if !IsPRAgent(n) {
			t.Fatalf("IsPRAgent(%q) = false, want true", n)
		}
	}
	if IsPRAgent(SpecSecurity) {
		t.Fatalf("IsPRAgent(%q) = true, want false (code specialist)", SpecSecurity)
	}
}

// Each PR agent must have an embedded system prompt so SpecialistPrompt can
// load it the same way it loads code-specialist prompts.
func TestPRAgentPromptsLoad(t *testing.T) {
	for _, name := range AllPRAgents {
		p, err := SpecialistPrompt(name)
		if err != nil {
			t.Fatalf("SpecialistPrompt(%q): %v", name, err)
		}
		if strings.TrimSpace(p) == "" {
			t.Fatalf("SpecialistPrompt(%q) returned empty prompt", name)
		}
	}
}

func prAgentTestPR() *gh.PR {
	return &gh.PR{
		Repository:   "o/r",
		Number:       7,
		Title:        "Add timeout flag",
		Author:       "alice",
		Body:         "Adds a --timeout flag to bound long runs.",
		BaseRef:      "main",
		HeadRef:      "feature",
		ChangedFiles: 2,
		Additions:    10,
		Deletions:    1,
	}
}

const prAgentTestDiff = `diff --git a/run.go b/run.go
--- a/run.go
+++ b/run.go
@@ -1,1 +1,2 @@
 package main
+var timeout int
`

// Description / scope agents get the title, body, and diff with no extra
// data section.
func TestBuildPRAgentUserPromptDescription(t *testing.T) {
	got := buildPRAgentUserPrompt(SpecDescription, prAgentTestPR(), prAgentTestDiff, PRAgentInput{}, "", "")
	for _, want := range []string{"Add timeout flag", "Adds a --timeout flag", "```diff", "var timeout int"} {
		if !strings.Contains(got, want) {
			t.Fatalf("description prompt missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "## CI checks") || strings.Contains(got, "## Discussion") {
		t.Fatalf("description prompt should not include checks/discussion sections\n%s", got)
	}
}

// The checks agent's prompt surfaces failing runs with their output and
// annotations, and lists passing runs separately.
func TestFormatChecksSection(t *testing.T) {
	report := &gh.ChecksReport{
		RollupState: "FAILURE",
		Runs: []gh.CheckRun{
			{Name: "lint", App: "GitHub Actions", Conclusion: "FAILURE", Title: "gofmt failed", Summary: "run.go is not formatted",
				Annotations: []gh.CheckRunAnnotation{{Path: "run.go", Line: 2, Level: "FAILURE", Message: "File is not gofmt-ed"}},
				DetailsURL:  "https://example.test/run/1"},
			{Name: "build", Conclusion: "SUCCESS"},
		},
	}
	got := formatChecksSection(report)
	for _, want := range []string{"## CI checks", "Rollup state: FAILURE", "lint", "gofmt failed", "run.go is not formatted", "run.go:2", "https://example.test/run/1", "Passing / other checks: build"} {
		if !strings.Contains(got, want) {
			t.Fatalf("checks section missing %q\n%s", want, got)
		}
	}
}

func TestFormatChecksSectionNoFailures(t *testing.T) {
	report := &gh.ChecksReport{RollupState: "SUCCESS", Runs: []gh.CheckRun{{Name: "build", Conclusion: "SUCCESS"}}}
	got := formatChecksSection(report)
	if !strings.Contains(got, "No checks are currently failing") {
		t.Fatalf("expected no-failures note\n%s", got)
	}
}

func TestFormatChecksSectionNil(t *testing.T) {
	if !strings.Contains(formatChecksSection(nil), "could not be loaded") {
		t.Fatalf("nil report should explain missing check status")
	}
}

// The discussion agent's prompt includes unresolved threads but excludes
// resolved ones, and includes top-level conversation.
func TestFormatDiscussionSection(t *testing.T) {
	threads := []gh.ReviewThread{
		{IsResolved: false, Comments: []gh.ReviewThreadComment{{Author: "bob", Body: "please return an error here", Path: "run.go", Line: 2}}},
		{IsResolved: true, Comments: []gh.ReviewThreadComment{{Author: "carol", Body: "resolved nit", Path: "run.go", Line: 1}}},
	}
	disc := []gh.DiscussionEvent{{Kind: gh.DiscussionReview, Author: "dave", Verdict: "CHANGES_REQUESTED", Body: "please add a CHANGELOG entry"}}
	got := formatDiscussionSection(threads, disc)
	for _, want := range []string{"Unresolved review threads (1)", "@bob", "please return an error here", "run.go:2", "@dave", "please add a CHANGELOG entry"} {
		if !strings.Contains(got, want) {
			t.Fatalf("discussion section missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "resolved nit") {
		t.Fatalf("discussion section must exclude resolved threads\n%s", got)
	}
}

func TestFormatDiscussionSectionNone(t *testing.T) {
	got := formatDiscussionSection([]gh.ReviewThread{{IsResolved: true, Comments: []gh.ReviewThreadComment{{Body: "x"}}}}, nil)
	if !strings.Contains(got, "Unresolved review threads: none") {
		t.Fatalf("expected 'none' note when all threads resolved\n%s", got)
	}
}

// sortedPRAgentResults restores AllPRAgents order regardless of completion
// timing under parallel dispatch.
func TestSortedPRAgentResults(t *testing.T) {
	in := []SpecialistResult{
		{Specialist: SpecScope},
		{Specialist: SpecDescription},
		{Specialist: SpecDiscussion},
		{Specialist: SpecChecks},
	}
	got := sortedPRAgentResults(in)
	for i, name := range AllPRAgents {
		if got[i].Specialist != name {
			t.Fatalf("sorted[%d] = %q want %q", i, got[i].Specialist, name)
		}
	}
}

func inlineFinding(path string, line int, comment string) Finding {
	return Finding{Path: path, Line: line, Side: "RIGHT", Severity: SeverityWarning, Comment: comment, Suggestion: "x := 1"}
}

func TestConstrainPRAgentScopeForcesDescriptionAndScopePRWide(t *testing.T) {
	for _, name := range []string{SpecDescription, SpecScope} {
		out := constrainPRAgentScope(name, []Finding{inlineFinding("a.go", 10, "use Mi")}, nil)
		if len(out) != 1 {
			t.Fatalf("%s: expected the finding preserved as PR-wide, got %d", name, len(out))
		}
		f := out[0]
		if f.Path != "" || f.Line != 0 || f.Side != "" {
			t.Fatalf("%s: inline finding should be forced PR-wide, got path=%q line=%d side=%q", name, f.Path, f.Line, f.Side)
		}
		if f.Suggestion != "" {
			t.Fatalf("%s: PR-wide finding must not keep an inline suggestion", name)
		}
		if f.Comment != "use Mi" {
			t.Fatalf("%s: comment should survive, got %q", name, f.Comment)
		}
	}
}

func TestConstrainPRAgentScopeDiscussionKeepsThreadAnchoredFinding(t *testing.T) {
	threads := []gh.ReviewThread{
		{IsResolved: false, Comments: []gh.ReviewThreadComment{{Author: "bob", Path: "run.go", Line: 2}}},
	}
	out := constrainPRAgentScope(SpecDiscussion, []Finding{inlineFinding("run.go", 2, "bob's ask is unaddressed")}, threads)
	if len(out) != 1 || out[0].Line != 2 {
		t.Fatalf("discussion finding on an unresolved thread line should be kept, got %#v", out)
	}
}

func TestConstrainPRAgentScopeDiscussionDropsOffThreadFinding(t *testing.T) {
	threads := []gh.ReviewThread{
		{IsResolved: false, Comments: []gh.ReviewThreadComment{{Author: "bob", Path: "run.go", Line: 2}}},
	}
	// Inline finding on a line with no matching thread: code-review drift.
	out := constrainPRAgentScope(SpecDiscussion, []Finding{inlineFinding("deploy/app.yaml", 207, "memory unit should be Mi")}, threads)
	if len(out) != 0 {
		t.Fatalf("discussion finding with no matching thread should be dropped, got %#v", out)
	}
}

func TestConstrainPRAgentScopeDiscussionKeepsPRWide(t *testing.T) {
	out := constrainPRAgentScope(SpecDiscussion, []Finding{{Path: "", Line: 0, Severity: SeverityWarning, Comment: "please add a CHANGELOG entry"}}, nil)
	if len(out) != 1 {
		t.Fatalf("PR-wide discussion findings (conversation asks) should be kept, got %d", len(out))
	}
}

func TestConstrainPRAgentScopeLeavesChecksAlone(t *testing.T) {
	in := []Finding{inlineFinding("ci.yaml", 5, "fix the failing build step")}
	out := constrainPRAgentScope(SpecChecks, in, nil)
	if len(out) != 1 || out[0].Path != "ci.yaml" || out[0].Line != 5 {
		t.Fatalf("checks inline findings should be untouched, got %#v", out)
	}
}
