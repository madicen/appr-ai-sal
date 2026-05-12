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
