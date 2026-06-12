package review

import (
	"strings"
	"testing"
)

func TestReviewCommentBodyPreservesSuggestionIndentation(t *testing.T) {
	f := Finding{
		Path:     "deploy/app.yaml",
		Line:     207,
		Side:     "RIGHT",
		Severity: SeverityWarning,
		Comment:  "Use binary units (Mi) for Kubernetes memory limits.",
		// Model/repair output often wraps the line in blank lines; the
		// indentation must survive so GitHub's Apply keeps valid YAML.
		Suggestion: "\n        memory: 717Mi\n",
	}
	body := ReviewCommentBody("design", f)
	if !strings.Contains(body, "```suggestion\n        memory: 717Mi\n```") {
		t.Fatalf("posted suggestion must preserve leading indentation and drop wrapping blank lines, got:\n%s", body)
	}
}

func TestTrimSuggestionBlockPreservesInteriorIndentation(t *testing.T) {
	in := "\n\n    if err != nil {\n        return err\n    }\n\n"
	want := "    if err != nil {\n        return err\n    }"
	if got := trimSuggestionBlock(in); got != want {
		t.Fatalf("trimSuggestionBlock = %q, want %q", got, want)
	}
}
