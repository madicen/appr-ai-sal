package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFileRel writes content to dir/rel, creating parent directories.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// --- Q6.1 multi-line suggestions -----------------------------------------

const multiLineRangeDiff = `diff --git a/cases.py b/cases.py
--- a/cases.py
+++ b/cases.py
@@ -1,2 +1,7 @@
 import os
 
+CASES = [
+    ("alpha", 1),
+    ("beta", 2),
+    ("gamma", 3),
+]
`

func TestMultiLineRangeGateKeepsValidRange(t *testing.T) {
	files := ParseDiff(multiLineRangeDiff)
	f := Finding{
		Path:      "cases.py",
		Line:      6, // ("gamma", 3),
		StartLine: 4, // ("alpha", 1),
		Side:      "RIGHT",
		Severity:  SeverityWarning,
		Comment:   "Multiply every case value by 10.",
		Suggestion: "    (\"alpha\", 10),\n" +
			"    (\"beta\", 20),\n" +
			"    (\"gamma\", 30),",
	}
	out := validateMultiLineSuggestionRange([]Finding{f}, files)
	if out[0].StartLine != 4 {
		t.Fatalf("valid contiguous range must keep StartLine=4, got %d (reason=%q)", out[0].StartLine, out[0].SuggestionStrippedReason)
	}
	if strings.TrimSpace(out[0].Suggestion) == "" {
		t.Fatalf("valid multi-line range must keep its suggestion")
	}
}

func TestMultiLineRangeGateStripsInvalidRange(t *testing.T) {
	files := ParseDiff(multiLineRangeDiff)
	// StartLine 999 is not anchorable → the whole range is invalid.
	f := Finding{
		Path:       "cases.py",
		Line:       6,
		StartLine:  999,
		Side:       "RIGHT",
		Severity:   SeverityWarning,
		Comment:    "Rewrite the block.",
		Suggestion: "    (\"x\", 0),",
	}
	out := validateMultiLineSuggestionRange([]Finding{f}, files)
	if out[0].StartLine != 0 {
		t.Fatalf("invalid range must clear StartLine, got %d", out[0].StartLine)
	}
	if out[0].Suggestion != "" {
		t.Fatalf("invalid multi-line range must strip the suggestion, got %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "multi-line suggestion range invalid") {
		t.Fatalf("expected a range-invalid reason, got %q", out[0].SuggestionStrippedReason)
	}
}

func TestMultiLineRangeGateDropsRangeWhenNoSuggestion(t *testing.T) {
	files := ParseDiff(multiLineRangeDiff)
	// A multi-line span with no suggestion falls back to a single-line anchor.
	f := Finding{
		Path:      "cases.py",
		Line:      6,
		StartLine: 4,
		Side:      "RIGHT",
		Severity:  SeverityWarning,
		Comment:   "These cases look off.",
	}
	out := validateMultiLineSuggestionRange([]Finding{f}, files)
	if out[0].StartLine != 0 {
		t.Fatalf("suggestion-less finding must drop the StartLine span, got %d", out[0].StartLine)
	}
}

func TestMultiLineRangeValid(t *testing.T) {
	files := ParseDiff(multiLineRangeDiff)
	if !multiLineRangeValid(Finding{Path: "cases.py", Line: 6, StartLine: 3}, files) {
		t.Fatalf("3..6 is a contiguous post-image range and must be valid")
	}
	if multiLineRangeValid(Finding{Path: "cases.py", Line: 4, StartLine: 6}, files) {
		t.Fatalf("start_line >= line must be invalid")
	}
	if multiLineRangeValid(Finding{Path: "cases.py", Line: 6, StartLine: 0}, files) {
		t.Fatalf("start_line 0 is single-line, not a range")
	}
}

func TestInlineReviewCommentCarriesMultiLineRange(t *testing.T) {
	f := Finding{Path: "cases.py", Line: 6, StartLine: 4, Side: "RIGHT", Comment: "c", Suggestion: "x"}
	c := InlineReviewComment("formatting", f)
	if c.StartLine != 4 || c.StartSide != "RIGHT" {
		t.Fatalf("multi-line finding must carry StartLine/StartSide, got start_line=%d start_side=%q", c.StartLine, c.StartSide)
	}
	// Marshaled payload must include start_line/start_side.
	b, _ := json.Marshal(c)
	if !strings.Contains(string(b), `"start_line":4`) || !strings.Contains(string(b), `"start_side":"RIGHT"`) {
		t.Fatalf("marshaled review comment missing multi-line fields: %s", b)
	}
	// A single-line finding must omit them entirely (byte-identical to before).
	single := InlineReviewComment("formatting", Finding{Path: "cases.py", Line: 6, Side: "RIGHT", Comment: "c"})
	sb, _ := json.Marshal(single)
	if strings.Contains(string(sb), "start_line") || strings.Contains(string(sb), "start_side") {
		t.Fatalf("single-line comment must omit multi-line fields: %s", sb)
	}
}

// --- Q6.2 cross-hunk anchor relocation -----------------------------------

const crossHunkDiff = `diff --git a/svc.go b/svc.go
--- a/svc.go
+++ b/svc.go
@@ -1,2 +1,5 @@
 package svc
 
+func helperOne(ctx context.Context) error {
+	return doWorkWithContext(ctx, "primary")
+}
@@ -40,2 +42,5 @@
 // tail
 
+func helperTwo(ctx context.Context) error {
+	return doWorkWithContext(ctx, "secondary-unique-marker-here")
+}
`

func TestValidateAnchorExcerptRelocatesCrossHunk(t *testing.T) {
	files := ParseDiff(crossHunkDiff)
	// Model anchored at line 3 (helperOne) but its quoted excerpt is the
	// UNIQUE secondary line living in the SECOND hunk (line 45).
	f := Finding{
		Path:          "svc.go",
		Line:          3,
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "Wrap the call.",
		AnchorExcerpt: `return doWorkWithContext(ctx, "secondary-unique-marker-here")`,
		Suggestion:    `	return doWorkWithContext(ctx, "secondary")`,
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Line != 45 {
		t.Fatalf("cross-hunk excerpt should relocate to line 45, got %d (reason=%q)", out[0].Line, out[0].SuggestionStrippedReason)
	}
	if out[0].AnchorRelocatedFrom != 3 {
		t.Fatalf("expected AnchorRelocatedFrom=3, got %d", out[0].AnchorRelocatedFrom)
	}
	if strings.TrimSpace(out[0].Suggestion) == "" {
		t.Fatalf("suggestion must survive a clean cross-hunk relocation")
	}
}

// --- Q6.3 wrong-line prose comments --------------------------------------

func TestValidateAnchorExcerptAnnotatesWrongLineProse(t *testing.T) {
	files := ParseDiff(anchorExcerptGoDiff)
	// Prose-only finding (no suggestion) whose excerpt matches nothing.
	f := Finding{
		Path:          "foo.go",
		Line:          3,
		Side:          "RIGHT",
		Severity:      SeverityError,
		Comment:       "This function mishandles the error.",
		AnchorExcerpt: "func TotallyDifferentSymbol(x int) bool {",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].AnchorMismatchNote == "" {
		t.Fatalf("wrong-line prose comment must be annotated with a mismatch note")
	}
	if out[0].Verified == nil || *out[0].Verified {
		t.Fatalf("wrong-line prose comment must be marked verified=false, got %v", out[0].Verified)
	}
	if out[0].Confidence == nil || *out[0].Confidence > 0.3 {
		t.Fatalf("wrong-line prose comment must have low confidence, got %v", out[0].Confidence)
	}
	if out[0].Severity != SeverityWarning {
		t.Fatalf("error finding on the wrong line must be demoted one rank to warning, got %q", out[0].Severity)
	}
}

// --- Q6.4 adjacent-line + PR-wide dedupe ---------------------------------

func adjFinding(line int, comment string) Finding {
	return Finding{Path: "deploy/app.yaml", Line: line, Side: "RIGHT", Severity: SeverityWarning, Comment: comment}
}

func TestDedupeAdjacentLineWindowCollapses(t *testing.T) {
	comment := "The memory unit M should be Mi for Kubernetes binary quantities here."
	// Two lines apart (205 vs 207) → within the ±2 window; near-identical
	// comments → collapse to one.
	specs := []SpecialistResult{
		{Specialist: SpecFormatting, Findings: []Finding{adjFinding(205, comment)}},
		{Specialist: SpecDesign, Findings: []Finding{adjFinding(207, comment)}},
	}
	out := dedupeInlineFindingsAcrossSpecialists(specs)
	if got := countFindings(out); got != 1 {
		t.Fatalf("adjacent (±2) near-duplicates should collapse to 1, got %d", got)
	}
	if len(findingsForSpecialist(out, SpecFormatting)) != 1 {
		t.Fatalf("formatting (higher lane) should keep the finding")
	}
}

func TestDedupeAdjacentLineWindowRespectsDistance(t *testing.T) {
	comment := "The memory unit M should be Mi for Kubernetes binary quantities here."
	// Three lines apart (205 vs 208) → outside the ±2 window → both survive.
	specs := []SpecialistResult{
		{Specialist: SpecFormatting, Findings: []Finding{adjFinding(205, comment)}},
		{Specialist: SpecDesign, Findings: []Finding{adjFinding(208, comment)}},
	}
	out := dedupeInlineFindingsAcrossSpecialists(specs)
	if got := countFindings(out); got != 2 {
		t.Fatalf("findings >2 lines apart must both survive, got %d", got)
	}
}

// --- Q6.5 witness expansion ----------------------------------------------

func TestWitnessInputsIncludePRWideFindings(t *testing.T) {
	specs := []SpecialistResult{
		{Specialist: SpecTesting, Findings: []Finding{
			{Path: "a_test.go", Line: 5, Side: "RIGHT", Severity: SeverityWarning, Comment: "inline testing finding"},
			{Path: "", Line: 0, Severity: SeverityWarning, Comment: "This PR adds no tests for the new package."},
		}},
		{Specialist: SpecDesign, Findings: []Finding{
			{Path: "", Line: 0, Severity: SeverityWarning, Comment: "design is not witnessable and must be excluded"},
		}},
	}
	inputs, _, _ := witnessInputsForSpecialists(specs)
	if len(inputs) != 2 {
		t.Fatalf("expected 2 witness inputs (inline + PR-wide testing), got %d", len(inputs))
	}
	sawPRWide := false
	for _, in := range inputs {
		if in.Specialist == SpecDesign {
			t.Fatalf("non-witnessable design finding must not be fed to the witness")
		}
		if in.Path == "" && in.Line == 0 {
			sawPRWide = true
		}
	}
	if !sawPRWide {
		t.Fatalf("PR-wide testing finding must be fed to the witness (Q6.5)")
	}
}

func TestWitnessInputsCollectFormattingFindings(t *testing.T) {
	specs := []SpecialistResult{
		{Specialist: SpecFormatting, Findings: []Finding{
			{Path: "x.go", Line: 3, Side: "RIGHT", Severity: SeverityInfo, Comment: "rename `foo_bar` to camelCase"},
		}},
	}
	_, _, formatting := witnessInputsForSpecialists(specs)
	if len(formatting) != 1 {
		t.Fatalf("formatting findings must be collected for the formatting harvester, got %d", len(formatting))
	}
}

func TestBuildFormattingConventionEvidenceReportsIdentifierStyle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pkg/other/b.go", "package other\nfunc getUserName() string { return userName }\nvar httpClient = newClient()\n")
	writeFile(t, dir, "pkg/other/c.go", "package other\nfunc parseRequestBody() {}\nvar maxRetryCount = 3\n")
	f := Finding{Path: "pkg/sub/a.go", Line: 2, Side: "RIGHT", Comment: "rename `parseRequestBody`"}
	ev := BuildFormattingConventionEvidence(dir, []Finding{f})
	if ev == "" {
		t.Fatalf("expected formatting evidence to be harvested from sibling files")
	}
	if !strings.Contains(ev, "camelCase") {
		t.Fatalf("expected a dominant camelCase identifier-style census, got:\n%s", ev)
	}
	if !strings.Contains(ev, "token `parseRequestBody`") {
		t.Fatalf("expected token-presence counting for the referenced identifier, got:\n%s", ev)
	}
}

func TestClassifyIdentifierStyle(t *testing.T) {
	cases := []struct {
		id   string
		want identifierStyle
	}{
		{"getUserName", idStyleCamel},
		{"ParseConfig", idStylePascal},
		{"max_retry_count", idStyleSnake},
		{"total", idStyleOther},       // single lowercase word
		{"HTTP", idStyleOther},        // all caps
		{"MAX_RETRIES", idStyleOther}, // screaming snake
	}
	for _, tc := range cases {
		if got := classifyIdentifierStyle(tc.id); got != tc.want {
			t.Errorf("classifyIdentifierStyle(%q) = %d, want %d", tc.id, got, tc.want)
		}
	}
}

// --- Q6.6 repair-pass excerpt parity -------------------------------------

func TestApplyRepairsRejectsExcerptMismatch(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := repairCandidateFinding()
	f.Line = 13
	// Repair names line 13 but quotes an excerpt that matches nothing → the
	// excerpt parity check rejects the repair (same as a first-pass mismatch).
	results := map[int]repairResult{0: {
		AnchorLine:    13,
		AnchorExcerpt: "this excerpt does not match the memory line at all",
		Replacement:   "        memory: 717Mi",
	}}
	out, applied := applyRepairs([]Finding{f}, files, results)
	if applied != 0 {
		t.Fatalf("excerpt-mismatched repair must be rejected, applied=%d", applied)
	}
	if out[0].Suggestion != "" || out[0].SuggestionRepaired {
		t.Fatalf("rejected repair must leave the finding suggestion-less, got %q repaired=%v", out[0].Suggestion, out[0].SuggestionRepaired)
	}
}

func TestApplyRepairsAcceptsMatchingExcerpt(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := repairCandidateFinding()
	f.Line = 13
	results := map[int]repairResult{0: {
		AnchorLine:    13,
		AnchorExcerpt: "        memory: 717M", // matches the real line 13
		Replacement:   "        memory: 717Mi",
	}}
	out, applied := applyRepairs([]Finding{f}, files, results)
	if applied != 1 {
		t.Fatalf("repair with a matching excerpt must be accepted, applied=%d", applied)
	}
	if !out[0].SuggestionRepaired || strings.TrimSpace(out[0].Suggestion) != "memory: 717Mi" {
		t.Fatalf("accepted repair must apply the suggestion, got repaired=%v suggestion=%q", out[0].SuggestionRepaired, out[0].Suggestion)
	}
}

// --- Q3.4 confidence/verified parsing ------------------------------------

func TestParseSpecialistJSONConfidenceVerified(t *testing.T) {
	raw := `{"summary":"s","findings":[
	  {"path":"a.go","line":3,"severity":"warning","comment":"has fields","confidence":0.9,"verified":true},
	  {"path":"b.go","line":4,"severity":"info","comment":"omits fields"}
	]}`
	o, err := parseSpecialistJSON(raw)
	if err != nil {
		t.Fatalf("parseSpecialistJSON: %v", err)
	}
	if len(o.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(o.Findings))
	}
	f0 := o.Findings[0]
	if f0.Confidence == nil || *f0.Confidence != 0.9 {
		t.Fatalf("finding 0 confidence should parse to 0.9, got %v", f0.Confidence)
	}
	if f0.Verified == nil || !*f0.Verified {
		t.Fatalf("finding 0 verified should parse to true, got %v", f0.Verified)
	}
	f1 := o.Findings[1]
	if f1.Confidence != nil || f1.Verified != nil {
		t.Fatalf("finding 1 omitted both fields; they must stay nil, got confidence=%v verified=%v", f1.Confidence, f1.Verified)
	}
}
