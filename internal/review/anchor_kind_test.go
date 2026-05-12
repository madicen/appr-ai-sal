package review

import (
	"strings"
	"testing"
)

// screenshotIngressPipelineDiff mirrors the user's screenshot: four `+`
// comment lines added to a Go file. The specialist anchored at the LAST of
// the four comment lines (line 76) and emitted a struct-literal as the
// suggestion while claiming "the function name should be in snake_case".
const screenshotIngressPipelineDiff = `diff --git a/internal/ingress/ingress_pipeline.go b/internal/ingress/ingress_pipeline.go
--- a/internal/ingress/ingress_pipeline.go
+++ b/internal/ingress/ingress_pipeline.go
@@ -70,3 +70,9 @@
 package ingress
 
 var ingressValidators = []ingestValidatorFunc{
+	// price-shape / SKU-shape / availability-token regexes see plain
+	// text. Without this, a JSON-LD description like
+	// "<span data-mce-fragment=\"1\">…</span>" lands in the DB verbatim
+	// — schema.org documents description as plain text but Shopify and
+	// other clients ship escaped HTML in product descriptions.
+}
`

func TestValidateAnchorKindStripsScreenshotFailure(t *testing.T) {
	files := ParseDiff(screenshotIngressPipelineDiff)
	if len(files) != 1 {
		t.Fatalf("ParseDiff: got %d files, want 1", len(files))
	}
	f := Finding{
		Path:       "internal/ingress/ingress_pipeline.go",
		Line:       76,
		Side:       "RIGHT",
		Severity:   SeverityWarning,
		Comment:    "The function name should be in snake_case according to naming conventions.",
		Suggestion: `ingestValidatorFunc{N: "strip_html", F: ingressStripHTML}`,
	}
	out := validateAnchorKind([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("suggestion should be stripped (anchor is a comment-only line, comment names a declaration), got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "comment-only") {
		t.Fatalf("expected stripped reason to mention comment-only anchor, got: %q", out[0].SuggestionStrippedReason)
	}
	if !strings.Contains(out[0].Comment, "function name") {
		t.Fatalf("comment must be preserved so the human still sees the issue, got: %q", out[0].Comment)
	}
}

func TestValidateAnchorKindKeepsDocCommentAboveDeclaration(t *testing.T) {
	const goDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,5 +1,8 @@
 package foo
 
+// ParseConfig is a helper.
+func ParseConfig(p string) (*Config, error) {
+	return nil, nil
+}
`
	files := ParseDiff(goDiff)
	// The specialist (correctly) anchors at the comment line and the
	// suggestion replaces it with a richer doc block — both anchor and
	// suggestion are comment-only, so the gate must NOT fire.
	f := Finding{
		Path:     "foo.go",
		Line:     3,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "ParseConfig is exported but lacks a proper godoc comment.",
		Suggestion: "// ParseConfig parses the file at p and returns the resulting Config.\n" +
			"// It returns an error if the file is missing or malformed.",
	}
	out := validateAnchorKind([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatalf("comment-to-comment doc rewrite was incorrectly stripped: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorKindStripsDeclarationFixAtBlankLine(t *testing.T) {
	const goDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,5 @@
 package foo
 
+
 func A() {}
`
	files := ParseDiff(goDiff)
	f := Finding{
		Path:       "foo.go",
		Line:       3,
		Side:       "RIGHT",
		Severity:   SeverityWarning,
		Comment:    "The function should be renamed to FunctionA per package conventions.",
		Suggestion: "func FunctionA() {}",
	}
	out := validateAnchorKind([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("declaration-rename anchored at blank line should be stripped, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "blank") {
		t.Fatalf("expected stripped reason to mention blank anchor, got: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorKindStripsProseFixAtClosingBrace(t *testing.T) {
	const goDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,5 +1,7 @@
 package foo
 
 func A() {
+	log.Print("hello, wrold")
 }
`
	files := ParseDiff(goDiff)
	// The model said "typo: 'wrold' → 'world'" but anchored at the `}`
	// instead of the log line. Replacing `}` with the fixed log line
	// would delete the brace.
	f := Finding{
		Path:       "foo.go",
		Line:       5,
		Side:       "RIGHT",
		Severity:   SeverityInfo,
		Comment:    "typo: 'wrold' should be 'world' in the log line.",
		Suggestion: `	log.Print("hello, world")`,
	}
	out := validateAnchorKind([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("prose fix anchored at `}` should be stripped, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "closing brace") {
		t.Fatalf("expected stripped reason to mention closing brace, got: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorKindLeavesCodeAnchorAlone(t *testing.T) {
	const goDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package foo
 
+var name = strings.toLower(s)
`
	files := ParseDiff(goDiff)
	f := Finding{
		Path:       "foo.go",
		Line:       3,
		Side:       "RIGHT",
		Severity:   SeverityWarning,
		Comment:    "Go's strings package uses ToLower (capitalised). This won't compile.",
		Suggestion: "var name = strings.ToLower(s)",
	}
	out := validateAnchorKind([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatalf("anchor is real code, gate must not fire: %q", out[0].SuggestionStrippedReason)
	}
}

func TestValidateAnchorKindLeavesGeneralFindingsAlone(t *testing.T) {
	files := ParseDiff(screenshotIngressPipelineDiff)
	f := Finding{
		Path:       "",
		Line:       0,
		Severity:   SeverityInfo,
		Comment:    "PR-wide: rename helpers to follow the package's MixedCaps convention.",
		Suggestion: "(suggestion content for a general finding)",
	}
	out := validateAnchorKind([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatal("general findings (no path/line) must pass through untouched")
	}
}

func TestClassifyAnchorLine(t *testing.T) {
	cases := []struct {
		in   string
		want anchorLineKind
	}{
		{"", kindBlank},
		{"   \t  ", kindBlank},
		{"}", kindClosingBrace},
		{"   })", kindClosingBrace},
		{"};", kindClosingBrace},
		{"// just a comment", kindCommentOnly},
		{"  # python comment", kindCommentOnly},
		{"-- sql comment", kindCommentOnly},
		{" * jsdoc continuation", kindCommentOnly},
		{"x := 1 // trailing comment", kindCode},
		{`fmt.Println("hi")`, kindCode},
	}
	for _, tc := range cases {
		if got := classifyAnchorLine(tc.in); got != tc.want {
			t.Errorf("classifyAnchorLine(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestClassifyCommentIntent(t *testing.T) {
	cases := []struct {
		in   string
		want commentIntent
	}{
		{"", intentUnknown},
		{"The function name should be in snake_case.", intentDeclaration},
		{"Rename this variable to something descriptive.", intentDeclaration},
		{"typo: 'wrold' → 'world'", intentProse},
		{"misspelled 'occured' in the log message", intentProse},
		{"This block could be simplified with an early return.", intentUnknown},
		// "should cover" is intentionally NOT matched by declarationVerbRe
		// (only "should be / should have / is missing / lacks / rename /
		// use / name / named / naming / convention"), so this stays in
		// intentUnknown even though it mentions "tests".
		{"functional tests should cover the error path.", intentUnknown},
	}
	for _, tc := range cases {
		if got := classifyCommentIntent(tc.in); got != tc.want {
			t.Errorf("classifyCommentIntent(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
