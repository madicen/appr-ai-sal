package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// Diffs used across the incremental tests. foo.go is identical between old and
// new; bar.go changes; baz.go is new.
const (
	oldIncDiff = "diff --git a/foo.go b/foo.go\n" +
		"index 111..222 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package foo\n" +
		"+auth := checkToken(request.Header) // unique security relevant line\n" +
		" func Foo() {}\n" +
		"diff --git a/bar.go b/bar.go\n" +
		"index 333..444 100644\n" +
		"--- a/bar.go\n" +
		"+++ b/bar.go\n" +
		"@@ -1,2 +1,3 @@\n" +
		" package bar\n" +
		"+const OldConstantValueForBar = 1\n"

	newIncDiff = "diff --git a/foo.go b/foo.go\n" +
		"index 111..222 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package foo\n" +
		"+auth := checkToken(request.Header) // unique security relevant line\n" +
		" func Foo() {}\n" +
		"diff --git a/bar.go b/bar.go\n" +
		"index 333..555 100644\n" +
		"--- a/bar.go\n" +
		"+++ b/bar.go\n" +
		"@@ -1,2 +1,3 @@\n" +
		" package bar\n" +
		"+const NewConstantValueForBar = 2\n" +
		"diff --git a/baz.go b/baz.go\n" +
		"index 000..666 100644\n" +
		"--- a/baz.go\n" +
		"+++ b/baz.go\n" +
		"@@ -1,1 +1,2 @@\n" +
		" package baz\n" +
		"+var addedInBazUniqueLine = true\n"
)

func TestComputeInterdiff(t *testing.T) {
	id := computeInterdiff(oldIncDiff, newIncDiff)
	if !id.Unchanged["foo.go"] {
		t.Errorf("foo.go should be unchanged; changed=%v unchanged=%v", id.Changed, id.Unchanged)
	}
	if !id.Changed["bar.go"] {
		t.Errorf("bar.go should be changed")
	}
	if !id.Changed["baz.go"] {
		t.Errorf("baz.go (new) should be changed")
	}
	if id.Changed["foo.go"] || id.Unchanged["bar.go"] || id.Unchanged["baz.go"] {
		t.Errorf("misclassified: changed=%v unchanged=%v", id.Changed, id.Unchanged)
	}
}

func TestCarryForwardSurviveReviewGone(t *testing.T) {
	id := computeInterdiff(oldIncDiff, newIncDiff)
	newFiles := ParseDiff(newIncDiff)
	prior := []SpecialistResult{
		{
			Specialist: SpecSecurity,
			Findings: []Finding{
				// On an unchanged file, anchored code present → survive.
				{Path: "foo.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning,
					Comment: "token not validated", AnchorExcerpt: "auth := checkToken(request.Header) // unique security relevant line"},
				// On a changed file → dropped for re-review (specialist re-runs).
				{Path: "bar.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning,
					Comment: "bar issue", AnchorExcerpt: "const OldConstantValueForBar = 1"},
				// File no longer in the diff → gone.
				{Path: "gone.go", Line: 3, Side: "RIGHT", Severity: SeverityError,
					Comment: "was here", AnchorExcerpt: "something that used to exist here"},
			},
		},
		// PR agents are NOT carried forward.
		{
			Specialist: SpecDiscussion,
			Findings: []Finding{
				{Path: "foo.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning,
					Comment: "discussion thing", AnchorExcerpt: "auth := checkToken(request.Header) // unique security relevant line"},
			},
		},
	}

	carried, stats := carryForwardFindings(prior, newFiles, id.Changed)
	if stats.Survived != 1 || stats.Review != 1 || stats.Gone != 1 {
		t.Fatalf("stats = %+v; want Survived=1 Review=1 Gone=1", stats)
	}
	if len(carried) != 1 || carried[0].Specialist != SpecSecurity {
		t.Fatalf("carried = %+v; want a single security result", carried)
	}
	if len(carried[0].Findings) != 1 || carried[0].Findings[0].Path != "foo.go" {
		t.Fatalf("carried findings = %+v; want the foo.go finding only", carried[0].Findings)
	}
}

func TestClassifyCarriedFindingGoneOnUnchangedWhenExcerptAbsent(t *testing.T) {
	newFiles := ParseDiff(newIncDiff)
	changed := map[string]bool{"bar.go": true, "baz.go": true} // foo.go unchanged
	f := Finding{Path: "foo.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning,
		Comment: "stale", AnchorExcerpt: "this exact line is definitely not present in foo anymore"}
	_, decision := classifyCarriedFinding(f, newFiles, changed)
	if decision != carryGone {
		t.Fatalf("decision = %v; want carryGone (excerpt no longer matches)", decision)
	}
}

func TestClassifyCarriedFindingSurvivesWithoutExcerpt(t *testing.T) {
	newFiles := ParseDiff(newIncDiff)
	changed := map[string]bool{"bar.go": true}
	f := Finding{Path: "foo.go", Line: 2, Side: "RIGHT", Severity: SeverityInfo, Comment: "no excerpt"}
	got, decision := classifyCarriedFinding(f, newFiles, changed)
	if decision != carrySurvive {
		t.Fatalf("decision = %v; want carrySurvive (unchanged file, best-effort)", decision)
	}
	if got.Line != 2 {
		t.Errorf("Line = %d; want 2 (unchanged)", got.Line)
	}
}

func TestReduceDiffToFiles(t *testing.T) {
	reduced := reduceDiffToFiles(newIncDiff, map[string]bool{"bar.go": true, "baz.go": true})
	if strings.Contains(reduced, "a/foo.go") {
		t.Errorf("reduced diff should not contain the unchanged foo.go stanza:\n%s", reduced)
	}
	if !strings.Contains(reduced, "a/bar.go") || !strings.Contains(reduced, "a/baz.go") {
		t.Errorf("reduced diff missing a changed stanza:\n%s", reduced)
	}
	// The kept stanzas must be byte-identical (line numbers intact) — assert a
	// hunk header survived verbatim.
	if !strings.Contains(reduced, "@@ -1,2 +1,3 @@") {
		t.Errorf("reduced diff lost bar.go hunk header:\n%s", reduced)
	}
	if reduceDiffToFiles(newIncDiff, nil) != "" {
		t.Errorf("empty keep set should yield empty diff")
	}
}

func TestMergeCarriedFindings(t *testing.T) {
	fresh := []SpecialistResult{
		{Specialist: SpecSecurity, Findings: []Finding{{Path: "bar.go", Line: 2, Comment: "fresh"}}},
		{Specialist: SpecDesign, Findings: []Finding{}},
	}
	carried := []SpecialistResult{
		{Specialist: SpecSecurity, Findings: []Finding{{Path: "foo.go", Line: 2, Comment: "carried"}}},
		{Specialist: SpecTesting, Findings: []Finding{{Path: "foo.go", Line: 9, Comment: "carried-testing"}}},
	}
	merged := mergeCarriedFindings(fresh, carried)

	var sec *SpecialistResult
	var testing *SpecialistResult
	for i := range merged {
		switch merged[i].Specialist {
		case SpecSecurity:
			sec = &merged[i]
		case SpecTesting:
			testing = &merged[i]
		}
	}
	if sec == nil || len(sec.Findings) != 2 {
		t.Fatalf("security should have fresh+carried (2) findings; got %+v", sec)
	}
	if testing == nil || len(testing.Findings) != 1 {
		t.Fatalf("testing (carried-only, absent from fresh) should be appended; got %+v", testing)
	}
}

func TestFormatPriorFindingsStatus(t *testing.T) {
	id := computeInterdiff(oldIncDiff, newIncDiff)
	prior := &CachedDraft{
		HeadSHA: "abcdef123456ff",
		Specialists: []SpecialistResult{
			{Specialist: SpecSecurity, Findings: []Finding{
				{Path: "bar.go", Line: 2, Severity: SeverityWarning, Comment: "bar concern"},
				{Path: "foo.go", Line: 2, Severity: SeverityError, Comment: "foo concern"},
			}},
		},
	}
	got := FormatPriorFindingsStatus(prior, id)
	if !strings.Contains(got, "## Prior review findings") {
		t.Fatalf("missing heading:\n%s", got)
	}
	if !strings.Contains(got, "abcdef123456") {
		t.Errorf("missing short prior SHA:\n%s", got)
	}
	if !strings.Contains(got, "file changed since") {
		t.Errorf("bar.go finding should be tagged changed:\n%s", got)
	}
	if !strings.Contains(got, "file unchanged since") {
		t.Errorf("foo.go finding should be tagged unchanged:\n%s", got)
	}
}

func TestFormatPriorFindingsStatusEmptyOnFirstReview(t *testing.T) {
	if got := FormatPriorFindingsStatus(nil, Interdiff{}); got != "" {
		t.Errorf("nil prior should render empty section, got %q", got)
	}
	empty := &CachedDraft{HeadSHA: "sha"}
	if got := FormatPriorFindingsStatus(empty, Interdiff{}); got != "" {
		t.Errorf("prior with no findings should render empty section, got %q", got)
	}
}

// TestDiscussionPromptPriorStatusInjection verifies the discussion agent's
// prompt gains the prior-findings section on a re-review and stays identical to
// today on a first review (empty status).
func TestDiscussionPromptPriorStatusInjection(t *testing.T) {
	pr := &gh.PR{Owner: "acme", Repo: "widget", Number: 5, Repository: "acme/widget", Title: "t", Author: "a"}
	base := PRAgentInput{}
	withStatus := PRAgentInput{PriorFindingsStatus: "## Prior review findings\n\n- [file changed since] bar.go:2 (security, warning): x"}

	first := buildPRAgentUserPrompt(SpecDiscussion, pr, "diff --git a/x b/x\n", base, "", "")
	reReview := buildPRAgentUserPrompt(SpecDiscussion, pr, "diff --git a/x b/x\n", withStatus, "", "")

	if strings.Contains(first, "Prior review findings") {
		t.Errorf("first-review discussion prompt must not contain the prior-findings section")
	}
	if !strings.Contains(reReview, "Prior review findings") {
		t.Errorf("re-review discussion prompt should contain the prior-findings section:\n%s", reReview)
	}
}

// TestPlanIncrementalNilWithoutCache proves the first-review backward-compat
// guarantee at the planning layer: with no prior cache, planIncremental returns
// nil, so the runner takes the unchanged full-review path.
func TestPlanIncrementalNilWithoutCache(t *testing.T) {
	useTempDraftCache(t)
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 42}
	pr := &gh.PR{Owner: "acme", Repo: "widget", Number: 42, HeadSHA: "sha-new"}
	if plan := planIncremental(ref, pr, newIncDiff); plan != nil {
		t.Fatalf("planIncremental should be nil with no cache; got %+v", plan)
	}
	// Also nil when the PR has no head SHA to key on.
	if plan := planIncremental(ref, &gh.PR{Owner: "acme", Repo: "widget", Number: 42}, newIncDiff); plan != nil {
		t.Fatalf("planIncremental should be nil without a head SHA")
	}
}

// TestPlanIncrementalReReview drives the full plan end-to-end through the cache:
// save a prior review at an old SHA, then plan a re-review at a new SHA.
func TestPlanIncrementalReReview(t *testing.T) {
	useTempDraftCache(t)
	ref := gh.Ref{Owner: "acme", Repo: "widget", Number: 42}

	prior := &Draft{
		Ref:  ref,
		PR:   &gh.PR{Owner: "acme", Repo: "widget", Number: 42, HeadSHA: "sha-old"},
		Diff: oldIncDiff,
		Specialists: []SpecialistResult{
			{Specialist: SpecSecurity, Findings: []Finding{
				{Path: "foo.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning,
					Comment:       "token not validated",
					AnchorExcerpt: "auth := checkToken(request.Header) // unique security relevant line"},
				{Path: "bar.go", Line: 2, Side: "RIGHT", Severity: SeverityWarning,
					Comment: "bar issue", AnchorExcerpt: "const OldConstantValueForBar = 1"},
			}},
		},
	}
	if err := NewDraftCache().Save(prior, "sha-old"); err != nil {
		t.Fatalf("Save prior: %v", err)
	}

	pr := &gh.PR{Owner: "acme", Repo: "widget", Number: 42, HeadSHA: "sha-new"}
	plan := planIncremental(ref, pr, newIncDiff)
	if plan == nil {
		t.Fatalf("planIncremental returned nil for a re-review with a cached prior")
	}
	if !plan.interdiff.Unchanged["foo.go"] || !plan.interdiff.Changed["bar.go"] {
		t.Errorf("interdiff wrong: %+v", plan.interdiff)
	}
	// foo.go finding survives (unchanged); bar.go finding drops for re-review.
	if plan.stats.Survived != 1 || plan.stats.Review != 1 {
		t.Errorf("stats = %+v; want Survived=1 Review=1", plan.stats)
	}
	if len(plan.carried) != 1 || len(plan.carried[0].Findings) != 1 || plan.carried[0].Findings[0].Path != "foo.go" {
		t.Errorf("carried = %+v; want the foo.go security finding", plan.carried)
	}
	if !strings.Contains(plan.priorStatus, "Prior review findings") {
		t.Errorf("priorStatus should render the discussion section")
	}
}
