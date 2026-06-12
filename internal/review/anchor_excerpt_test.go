package review

import (
	"strings"
	"testing"
)

const anchorExcerptGoDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,5 +1,8 @@
 package foo
 
+func ParseConfig(p string) (*Config, error) {
+	return nil, nil
+}
`

func TestValidateAnchorExcerptKeepsMatchingExcerpt(t *testing.T) {
	files := ParseDiff(anchorExcerptGoDiff)
	f := Finding{
		Path:          "foo.go",
		Line:          3,
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "ParseConfig lacks a godoc comment.",
		AnchorExcerpt: "func ParseConfig(p string) (*Config, error) {",
		Suggestion: "// ParseConfig parses the file at p.\n" +
			"func ParseConfig(p string) (*Config, error) {",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatalf("matching excerpt should not be stripped: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorExcerptKeepsWhitespaceOnlyDifference(t *testing.T) {
	files := ParseDiff(anchorExcerptGoDiff)
	// Excerpt with collapsed internal whitespace must still pass.
	f := Finding{
		Path:          "foo.go",
		Line:          3,
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "ParseConfig lacks a godoc comment.",
		AnchorExcerpt: "func   ParseConfig(p string)  (*Config, error)   {",
		Suggestion:    "// ParseConfig parses the file at p.\nfunc ParseConfig(p string) (*Config, error) {",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatalf("whitespace-only difference should not strip: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorExcerptStripsContentMismatch(t *testing.T) {
	files := ParseDiff(anchorExcerptGoDiff)
	f := Finding{
		Path:          "foo.go",
		Line:          3,
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "ParseConfig lacks a godoc comment.",
		AnchorExcerpt: `func LoadConfig(path string) error {`, // different identifier
		Suggestion:    "// LoadConfig loads.\nfunc LoadConfig(path string) error {",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("content mismatch should strip the suggestion, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "anchor excerpt mismatch") {
		t.Fatalf("expected reason to mention anchor excerpt mismatch, got: %q", out[0].SuggestionStrippedReason)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "foo.go:3") {
		t.Fatalf("expected reason to include the path:line, got: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorExcerptSilentWhenExcerptEmpty(t *testing.T) {
	files := ParseDiff(anchorExcerptGoDiff)
	f := Finding{
		Path:          "foo.go",
		Line:          3,
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "ParseConfig lacks a godoc comment.",
		AnchorExcerpt: "", // model didn't emit it
		Suggestion:    "// ParseConfig parses the file at p.\nfunc ParseConfig(p string) (*Config, error) {",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatalf("missing excerpt must be a silent no-op, got reason %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorExcerptSilentWhenFileNotInDiff(t *testing.T) {
	files := ParseDiff(anchorExcerptGoDiff)
	f := Finding{
		Path:          "other.go",
		Line:          3,
		Side:          "RIGHT",
		Severity:      SeverityInfo,
		Comment:       "stray finding from outside the diff",
		AnchorExcerpt: "this excerpt cannot be validated",
		Suggestion:    "no validation possible",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatalf("excerpt with unknown path must not strip, got: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorExcerptIgnoresGeneralFindings(t *testing.T) {
	files := ParseDiff(anchorExcerptGoDiff)
	f := Finding{
		Path:          "",
		Line:          0,
		Severity:      SeverityInfo,
		Comment:       "PR-wide note.",
		AnchorExcerpt: "irrelevant for PR-wide findings",
		Suggestion:    "n/a",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatal("general findings must pass through untouched")
	}
}

func TestNormaliseExcerpt(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"foo bar", "foo bar"},
		{"  foo   bar\t baz\n", "foo bar baz"},
		{"\t\tfunc  X  () {  ", "func X () {"},
	}
	for _, tc := range cases {
		if got := normaliseExcerpt(tc.in); got != tc.want {
			t.Errorf("normaliseExcerpt(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// anchorRelocateDiff contains a hunk where multiple post-image lines have
// distinct, substantive content so we can exercise the re-anchor path
// (lines 3 and 6 share `result := process(config)` content for the
// ambiguous-match case).
const anchorRelocateDiff = `diff --git a/work.go b/work.go
--- a/work.go
+++ b/work.go
@@ -1,2 +1,9 @@
 package work
 
+config := loadConfig("primary")
+result := process(config)
+log.Println("primary run complete")
+config := loadConfig("secondary")
+result := process(config)
+log.Println("secondary run complete")
`

// TestValidateAnchorExcerptReanchorsOnUniqueMatch reproduces the
// screenshot failure mode: the model anchors at the wrong line but its
// quoted excerpt uniquely matches a different line in the same hunk, so
// we re-anchor and keep the suggestion intact.
func TestValidateAnchorExcerptReanchorsOnUniqueMatch(t *testing.T) {
	files := ParseDiff(anchorRelocateDiff)
	f := Finding{
		Path:          "work.go",
		Line:          8, // wrong: "log.Println(\"secondary run complete\")"
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "Use log.Printf for structured logs.",
		AnchorExcerpt: `log.Println("primary run complete")`,
		Suggestion:    `log.Printf("primary run complete\n")`,
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Line != 5 {
		t.Fatalf("expected re-anchor to line 5, got Line=%d", out[0].Line)
	}
	if out[0].AnchorRelocatedFrom != 8 {
		t.Fatalf("expected AnchorRelocatedFrom=8, got %d", out[0].AnchorRelocatedFrom)
	}
	if out[0].Suggestion == "" {
		t.Fatalf("suggestion should survive a clean relocation, got empty (reason=%q)", out[0].SuggestionStrippedReason)
	}
	if out[0].SuggestionStrippedReason != "" {
		t.Fatalf("clean relocation should not record a stripped reason, got: %q", out[0].SuggestionStrippedReason)
	}
}

// TestValidateAnchorExcerptStripsOnAmbiguousMatch confirms we don't pick
// a random line when the excerpt matches multiple post-image lines. The
// reason string names the ambiguity so the TUI can surface it.
func TestValidateAnchorExcerptStripsOnAmbiguousMatch(t *testing.T) {
	files := ParseDiff(anchorRelocateDiff)
	// Lines 4 and 7 both have content "result := process(config)".
	f := Finding{
		Path:          "work.go",
		Line:          5, // distinct from both matches
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "Capture process errors here.",
		AnchorExcerpt: `result := process(config)`,
		Suggestion: "result, err := process(config)\n" +
			"if err != nil {\n" +
			"	return err\n" +
			"}",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Line != 5 {
		t.Fatalf("ambiguous case should not change Line, got %d", out[0].Line)
	}
	if out[0].AnchorRelocatedFrom != 0 {
		t.Fatalf("ambiguous case should not record a relocation, got AnchorRelocatedFrom=%d", out[0].AnchorRelocatedFrom)
	}
	if out[0].Suggestion != "" {
		t.Fatalf("suggestion should be stripped on ambiguous match, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "ambiguous") {
		t.Fatalf("expected 'ambiguous' reason, got: %q", out[0].SuggestionStrippedReason)
	}
}

// TestValidateAnchorExcerptShortExcerptStripsWithoutRelocation guards
// against false-positive relocation on short syntactic excerpts like `}`
// or `return nil` that match all over the file. The substantiveSuggestionLineMin
// floor (shared with suggestion_validate.go) suppresses the relocate
// attempt and we strip the suggestion instead.
func TestValidateAnchorExcerptShortExcerptStripsWithoutRelocation(t *testing.T) {
	files := ParseDiff(anchorRelocateDiff)
	f := Finding{
		Path:          "work.go",
		Line:          5, // anchor line is `log.Println(...)` — does not trim to `}`
		Side:          "RIGHT",
		Severity:      SeverityInfo,
		Comment:       "Close brace placement.",
		AnchorExcerpt: "}", // 1 char after trim → below threshold
		Suggestion:    "// some replacement",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Line != 5 {
		t.Fatalf("short-excerpt case should never relocate, got Line=%d", out[0].Line)
	}
	if out[0].AnchorRelocatedFrom != 0 {
		t.Fatalf("short-excerpt case should not record a relocation, got AnchorRelocatedFrom=%d", out[0].AnchorRelocatedFrom)
	}
	if out[0].Suggestion != "" {
		t.Fatalf("short-excerpt case should strip suggestion, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "too short") {
		t.Fatalf("expected 'too short' reason, got: %q", out[0].SuggestionStrippedReason)
	}
}

// TestValidateAnchorExcerptRelocatedFindingFlowsThroughPruneSuggestions
// is the order-of-operations guard: after we relocate, the downstream
// validateAndPruneSuggestions must look at the *new* line so it doesn't
// false-positive on the original (wrong) anchor.
func TestValidateAnchorExcerptRelocatedFindingFlowsThroughPruneSuggestions(t *testing.T) {
	files := ParseDiff(anchorRelocateDiff)
	f := Finding{
		Path:          "work.go",
		Line:          8, // wrong anchor
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "Switch to Printf.",
		AnchorExcerpt: `log.Println("primary run complete")`,
		Suggestion:    `log.Printf("primary run complete\n")`,
	}
	relocated := validateAnchorExcerpt([]Finding{f}, files)
	final := validateAndPruneSuggestions(relocated, files)
	if final[0].Line != 5 {
		t.Fatalf("expected relocated line 5 to survive prune pass, got Line=%d", final[0].Line)
	}
	if final[0].Suggestion == "" {
		t.Fatalf("relocated suggestion should survive prune pass, got empty (reason=%q)", final[0].SuggestionStrippedReason)
	}
}

// shortContentRelocateDiff mirrors the production.yaml screenshot: a memory
// limit on its own short line. The model anchored one line up (the cpu line)
// but quoted the memory line, which is only 12 characters — under
// substantiveSuggestionLineMin.
const shortContentRelocateDiff = `diff --git a/deploy/app.yaml b/deploy/app.yaml
--- a/deploy/app.yaml
+++ b/deploy/app.yaml
@@ -200,4 +200,4 @@ spec:
       requests:
+        cpu: 271m
+        memory: 717M
       limits:
`

// TestValidateAnchorExcerptRelocatesShortContentExcerpt is the fix for the
// screenshot failure: a short but content-bearing excerpt ("memory: 717M")
// that uniquely matches one post-image line should relocate instead of being
// stripped for being under the length floor.
func TestValidateAnchorExcerptRelocatesShortContentExcerpt(t *testing.T) {
	files := ParseDiff(shortContentRelocateDiff)
	f := Finding{
		Path:          "deploy/app.yaml",
		Line:          201, // wrong: anchored at "cpu: 271m"
		Side:          "RIGHT",
		Severity:      SeverityWarning,
		Comment:       "Use binary units (Mi) for Kubernetes memory limits.",
		AnchorExcerpt: "memory: 717M",
		Suggestion:    "        memory: 717Mi",
	}
	out := validateAnchorExcerpt([]Finding{f}, files)
	if out[0].Line != 202 {
		t.Fatalf("expected relocate to line 202 (the memory line), got Line=%d", out[0].Line)
	}
	if out[0].AnchorRelocatedFrom != 201 {
		t.Fatalf("expected AnchorRelocatedFrom=201, got %d", out[0].AnchorRelocatedFrom)
	}
	if strings.TrimSpace(out[0].Suggestion) != "memory: 717Mi" {
		t.Fatalf("short content excerpt should keep its suggestion after relocation, got %q (reason=%q)", out[0].Suggestion, out[0].SuggestionStrippedReason)
	}
}

// TestExcerptRelocatableWhenShort pins the content-vs-syntactic distinction
// that gates short-excerpt relocation.
func TestExcerptRelocatableWhenShort(t *testing.T) {
	cases := []struct {
		excerpt string
		want    bool
	}{
		{"memory: 717M", true},  // key: value
		{"replicas = 3", true},  // key = value
		{"port 8080", true},     // bare numeric literal
		{"}", false},            // closing brace
		{"})", false},           // closing brace
		{"return nil", false},   // code but no value/digit
		{"", false},             // blank
		{"// note", false},      // comment-only
	}
	for _, tc := range cases {
		if got := excerptRelocatableWhenShort(tc.excerpt); got != tc.want {
			t.Errorf("excerptRelocatableWhenShort(%q) = %v, want %v", tc.excerpt, got, tc.want)
		}
	}
}
