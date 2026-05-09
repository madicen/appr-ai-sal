package review

import (
	"strings"
	"testing"
)

// terraformDuplicateDiff models the user's screenshot: the docs agent
// anchored at line 18 (a `description` inside a different existing rule)
// and emitted a suggestion that would (a) delete that description and
// (b) duplicate the resource declaration two lines down.
const terraformDuplicateDiff = `diff --git a/main.tf b/main.tf
--- a/main.tf
+++ b/main.tf
@@ -10,15 +10,18 @@
 resource "aws_vpc_security_group_egress_rule" "allow_egress_to_jupyterhub" {
   security_group_id = module.cluster.security_group_ids[0]
   cidr_ipv4         = "10.0.0.0/8"
   from_port         = 443
   to_port           = 443
   ip_protocol       = "tcp"
   description       = "Allow Jupyterhub to reach the internal K8s API virtual IP"
 }
+
+resource "aws_vpc_security_group_egress_rule" "allow_egress_to_internal_hub" {
+  security_group_id = module.jupyterhub_singleuser_production.security_group_ids[0]
+  cidr_ipv4         = "172.30.0.0/16"
+  from_port         = 443
+  to_port           = 443
+  ip_protocol       = "tcp"
+  description       = "Allow Jupyter notebook pods to reach the internal hub and proxy"
+}
`

func TestValidateAndPruneSuggestionsDropsDuplicateOfNearbyDeclaration(t *testing.T) {
	files := ParseDiff(terraformDuplicateDiff)
	if len(files) != 1 {
		t.Fatalf("ParseDiff: got %d files, want 1", len(files))
	}
	f := Finding{
		Path:     "main.tf",
		Line:     16, // anchored at `description = "Allow Jupyterhub to reach..."`
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "missing doc comment on the new egress rule.",
		Suggestion: "// allow_egress_to_internal_hub allows Jupyter notebook pods to reach hub and proxy pods\n" +
			`resource "aws_vpc_security_group_egress_rule" "allow_egress_to_internal_hub" {`,
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("suggestion should have been cleared (duplicates the resource declaration), got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].Comment, "missing doc comment") {
		t.Fatalf("comment should be preserved so the human still sees the issue, got: %q", out[0].Comment)
	}
}

func TestValidateAndPruneSuggestionsKeepsValidGodocBlock(t *testing.T) {
	const goDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,5 +1,8 @@
 package foo
 
+func ParseConfig(p string) (*Config, error) {
+	return nil, nil
+}
`
	files := ParseDiff(goDiff)
	f := Finding{
		Path:     "foo.go",
		Line:     3,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "ParseConfig is exported but lacks a godoc comment.",
		Suggestion: "// ParseConfig parses the file at p and returns the resulting Config.\n" +
			"// It returns an error if the file is missing or malformed.\n" +
			"func ParseConfig(p string) (*Config, error) {",
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatal("valid doc-block suggestion was incorrectly dropped")
	}
}

func TestValidateAndPruneSuggestionsKeepsShortPunctuationLines(t *testing.T) {
	// A correct rewrite that ends with `}` should not be flagged just
	// because `}` happens to appear elsewhere — short syntactic lines are
	// below the substantiveness threshold.
	const goDiff = `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,6 +1,8 @@
 package x
 
+func A() {
+	return
+}
 
 func B() {
 }
`
	files := ParseDiff(goDiff)
	f := Finding{
		Path:     "x.go",
		Line:     5,
		Side:     "RIGHT",
		Severity: SeverityInfo,
		Comment:  "use a comment instead of bare return",
		Suggestion: "func A() {\n" +
			"	// no-op for now\n" +
			"}",
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatal("short syntactic lines like } should not trip the duplicate detector")
	}
}

func TestValidateAndPruneSuggestionsDropsNoOpSingleLineMatchingAnchor(t *testing.T) {
	const diff = `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,2 +1,3 @@
 package x
 
+const Greeting = "hello"
`
	files := ParseDiff(diff)
	f := Finding{
		Path:       "x.go",
		Line:       3,
		Side:       "RIGHT",
		Severity:   SeverityInfo,
		Comment:    "this is fine",
		Suggestion: `const Greeting = "hello"`,
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("no-op suggestion that just repeats the anchor should be cleared, got: %q", out[0].Suggestion)
	}
}

func TestValidateAndPruneSuggestionsLeavesGeneralFindingsAlone(t *testing.T) {
	files := ParseDiff(terraformDuplicateDiff)
	f := Finding{
		Path:       "",
		Line:       0,
		Severity:   SeverityInfo,
		Comment:    "PR-wide: README is out of date.",
		Suggestion: "should not be considered for an inline suggestion",
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	// General findings have suggestion stripped elsewhere; the validator
	// should ignore them and leave whatever is there for the caller.
	if out[0].Suggestion == "" {
		t.Fatal("general findings should not be touched by the inline-suggestion validator")
	}
}

// TestValidateAndPruneSuggestionsRecordsStrippedReason ensures the reason
// string flows onto the Finding so the TUI can show
// "(suggestion stripped: …)" instead of letting the reviewer guess.
func TestValidateAndPruneSuggestionsRecordsStrippedReason(t *testing.T) {
	files := ParseDiff(terraformDuplicateDiff)
	f := Finding{
		Path:     "main.tf",
		Line:     16,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "missing doc comment on the new egress rule.",
		Suggestion: "// allow_egress_to_internal_hub allows Jupyter notebook pods to reach hub and proxy pods\n" +
			`resource "aws_vpc_security_group_egress_rule" "allow_egress_to_internal_hub" {`,
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("suggestion should be cleared, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "duplicates") {
		t.Fatalf("expected stripped reason to mention duplication, got: %q", out[0].SuggestionStrippedReason)
	}
}

// TestValidateAndPruneSuggestionsDropsAnchorMismatch reproduces the
// "comment about `hold`, anchored at `enginsights-dev`" failure mode: the
// comment names a backtick identifier that appears nowhere in the
// surrounding hunk, so the suggestion is almost certainly anchored at the
// wrong line.
func TestValidateAndPruneSuggestionsDropsAnchorMismatch(t *testing.T) {
	const diff = `diff --git a/main.tf b/main.tf
--- a/main.tf
+++ b/main.tf
@@ -75,7 +75,8 @@
   names = [
     "enginsights-dev",
     "echelon",
     "global-mock-server-demo",
-    "cdp-shopify-ingestor"
+    "cdp-shopify-ingestor",
+    "hold",
   ]
`
	files := ParseDiff(diff)
	f := Finding{
		Path:     "main.tf",
		Line:     76, // anchored at "enginsights-dev"
		Side:     "RIGHT",
		Severity: SeverityWarning,
		// The comment names `cdp-shopify-ingestor` (which IS in the hunk)
		// AND `enginsights-dev`-which-is-not-the-target — but neither
		// quoted ident is what the comment is actually about. We use the
		// classic case: comment names `hold` (not in the hunk's *post*
		// image of the anchor area) — actually `hold` IS in the post image
		// here, so we use a totally absent identifier instead.
		Comment: "The DNS entry `nonexistent-entry-name` lacks a `# hold-it` comment explaining the carve-out.",
		Suggestion: "    # nonexistent-entry-name is the staging fallback queue\n" +
			`    "enginsights-dev",`,
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	if out[0].Suggestion != "" {
		t.Fatalf("suggestion should be stripped on anchor-vs-comment mismatch, got: %q", out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "anchor mismatch") {
		t.Fatalf("expected anchor-mismatch reason, got: %q", out[0].SuggestionStrippedReason)
	}
}

// TestValidateAndPruneSuggestionsKeepsBacktickIdentInHunk ensures the
// anchor-mismatch detector doesn't false-positive when at least one quoted
// identifier in the comment IS present in the hunk.
func TestValidateAndPruneSuggestionsKeepsBacktickIdentInHunk(t *testing.T) {
	const goDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,5 +1,8 @@
 package foo
 
+func ParseConfig(p string) (*Config, error) {
+	return nil, nil
+}
`
	files := ParseDiff(goDiff)
	f := Finding{
		Path:     "foo.go",
		Line:     3,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "`ParseConfig` is exported but lacks a godoc comment.",
		Suggestion: "// ParseConfig parses the file at p and returns the resulting Config.\n" +
			"// It returns an error if the file is missing or malformed.\n" +
			"func ParseConfig(p string) (*Config, error) {",
	}
	out := validateAndPruneSuggestions([]Finding{f}, files)
	if out[0].Suggestion == "" {
		t.Fatalf("anchor-match suggestion was incorrectly stripped: reason=%q", out[0].SuggestionStrippedReason)
	}
}

func TestExtractBacktickIdentifiersIgnoresShortAndOperators(t *testing.T) {
	idents := extractBacktickIdentifiers("see `ParseConfig`, `?`, `r`, and `cdp-shopify-ingestor`.")
	want := map[string]bool{"ParseConfig": true, "cdp-shopify-ingestor": true}
	if len(idents) != len(want) {
		t.Fatalf("idents=%v want %d entries", idents, len(want))
	}
	for _, id := range idents {
		if !want[id] {
			t.Fatalf("unexpected ident %q (kept short tokens or operators)", id)
		}
	}
}
