package review

import (
	"strings"
	"testing"
)

// unitSuffixDiff is a Kubernetes-style YAML manifest where a memory limit
// uses the decimal "M" suffix instead of the binary "Mi". A design/scope
// finding might name the corrected value in prose but ship no suggestion;
// synthesizeSuggestions should fill it in from the comment.
const unitSuffixDiff = `diff --git a/deploy/app.yaml b/deploy/app.yaml
--- a/deploy/app.yaml
+++ b/deploy/app.yaml
@@ -10,3 +10,4 @@ spec:
   containers:
   - name: app
     resources:
+        memory: 717M
`

func unitSuffixFinding() Finding {
	return Finding{
		Path:     "deploy/app.yaml",
		Line:     13,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "Kubernetes memory limits are binary quantities; use `717Mi` instead of `717M`.",
	}
}

func TestSynthesizeSuggestionsFillsUnitSuffix(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	out := synthesizeSuggestions([]Finding{unitSuffixFinding()}, files)
	if got := strings.TrimSpace(out[0].Suggestion); got != "memory: 717Mi" {
		t.Fatalf("synthesized suggestion = %q, want %q", got, "memory: 717Mi")
	}
	if !out[0].SuggestionSynthesized {
		t.Fatalf("SuggestionSynthesized should be set on a synthesized finding")
	}
}

func TestSynthesizeSuggestionsArrowPhrasing(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := unitSuffixFinding()
	f.Comment = "Wrong unit: `717M` -> `717Mi`."
	out := synthesizeSuggestions([]Finding{f}, files)
	if got := strings.TrimSpace(out[0].Suggestion); got != "memory: 717Mi" {
		t.Fatalf("arrow-phrasing suggestion = %q, want %q", got, "memory: 717Mi")
	}
}

func TestSynthesizeSuggestionsReplaceWithPhrasing(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := unitSuffixFinding()
	f.Comment = "Please replace `717M` with `717Mi` for binary units."
	out := synthesizeSuggestions([]Finding{f}, files)
	if got := strings.TrimSpace(out[0].Suggestion); got != "memory: 717Mi" {
		t.Fatalf("replace-with suggestion = %q, want %q", got, "memory: 717Mi")
	}
}

func TestSynthesizeSuggestionsRatherThanPhrasing(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := unitSuffixFinding()
	f.Comment = "Should be `717Mi` rather than `717M`."
	out := synthesizeSuggestions([]Finding{f}, files)
	if got := strings.TrimSpace(out[0].Suggestion); got != "memory: 717Mi" {
		t.Fatalf("rather-than suggestion = %q, want %q", got, "memory: 717Mi")
	}
}

func TestSynthesizeSuggestionsSkipsWhenModelSuppliedOne(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := unitSuffixFinding()
	f.Suggestion = "        memory: 999Mi"
	out := synthesizeSuggestions([]Finding{f}, files)
	if strings.TrimSpace(out[0].Suggestion) != "memory: 999Mi" {
		t.Fatalf("must not overwrite a model-authored suggestion, got %q", out[0].Suggestion)
	}
	if out[0].SuggestionSynthesized {
		t.Fatalf("SuggestionSynthesized must stay false when the model supplied the suggestion")
	}
}

func TestSynthesizeSuggestionsSkipsWhenSuggestionWasStripped(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := unitSuffixFinding()
	// A prior gate deliberately cleared the suggestion. Synthesis must not
	// resurrect one over its head.
	f.SuggestionStrippedReason = "anchor mismatch"
	out := synthesizeSuggestions([]Finding{f}, files)
	if strings.TrimSpace(out[0].Suggestion) != "" {
		t.Fatalf("synthesis must respect a stripped reason, got %q", out[0].Suggestion)
	}
	if out[0].SuggestionSynthesized {
		t.Fatalf("SuggestionSynthesized must stay false when a strip reason is present")
	}
}

func TestSynthesizeSuggestionsSkipsWhenOldTokenAmbiguous(t *testing.T) {
	// The anchor line contains the old token twice, so the single
	// substitution target is ambiguous — refuse to guess.
	const diff = `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,2 +1,3 @@
 package x
+var s = foo + foo
`
	files := ParseDiff(diff)
	f := Finding{
		Path:     "x.go",
		Line:     2,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "use `bar` instead of `foo`.",
	}
	out := synthesizeSuggestions([]Finding{f}, files)
	if strings.TrimSpace(out[0].Suggestion) != "" {
		t.Fatalf("ambiguous old token (2 occurrences) must not synthesize, got %q", out[0].Suggestion)
	}
}

func TestSynthesizeSuggestionsSkipsWithoutReplacementPhrase(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := unitSuffixFinding()
	// Names a value but no explicit replacement phrasing — stay silent.
	f.Comment = "This memory limit looks low for the workload."
	out := synthesizeSuggestions([]Finding{f}, files)
	if strings.TrimSpace(out[0].Suggestion) != "" {
		t.Fatalf("no replacement phrase should yield no suggestion, got %q", out[0].Suggestion)
	}
}

func TestSynthesizeSuggestionsSkipsGeneralFindings(t *testing.T) {
	files := ParseDiff(unitSuffixDiff)
	f := unitSuffixFinding()
	f.Path = ""
	f.Line = 0
	out := synthesizeSuggestions([]Finding{f}, files)
	if strings.TrimSpace(out[0].Suggestion) != "" {
		t.Fatalf("PR-wide findings have no anchor and must not synthesize, got %q", out[0].Suggestion)
	}
}

func TestReviewCommentBodyDisclosesSynthesizedSuggestion(t *testing.T) {
	f := unitSuffixFinding()
	f.Suggestion = "        memory: 717Mi"
	f.SuggestionSynthesized = true
	body := ReviewCommentBody("design", f)
	if !strings.Contains(body, "derived from the comment by appr-ai-sal") {
		t.Fatalf("posted body must disclose a synthesized suggestion, got:\n%s", body)
	}
	if !strings.Contains(body, "```suggestion") {
		t.Fatalf("posted body must still contain the suggestion block, got:\n%s", body)
	}
}

func TestReviewCommentBodyNoDisclosureForModelSuggestion(t *testing.T) {
	f := unitSuffixFinding()
	f.Suggestion = "        memory: 717Mi"
	// SuggestionSynthesized stays false.
	body := ReviewCommentBody("design", f)
	if strings.Contains(body, "derived from the comment by appr-ai-sal") {
		t.Fatalf("model-authored suggestions must not carry the synthesis disclosure, got:\n%s", body)
	}
}
