package review

import (
	"strings"
	"testing"
)

func TestSuggestionPostsToGitHub(t *testing.T) {
	cases := []struct {
		name string
		f    Finding
		want bool
	}{
		{"empty", Finding{Suggestion: ""}, false},
		{"code", Finding{Comment: "fix typo", Suggestion: "return nil"}, true},
		{"same as comment", Finding{Comment: "hello", Suggestion: "hello"}, false},
		{"fenced", Finding{Comment: "x", Suggestion: "```go\nx\n```"}, false},
		{"huge", Finding{Comment: "x", Suggestion: strings.Repeat("a", 9000)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SuggestionPostsToGitHub(tc.f); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSpecialistsHaveAnyFindings(t *testing.T) {
	if SpecialistsHaveAnyFindings(nil) {
		t.Fatal("nil slice should be false")
	}
	if SpecialistsHaveAnyFindings([]SpecialistResult{}) {
		t.Fatal("empty slice should be false")
	}
	if SpecialistsHaveAnyFindings([]SpecialistResult{{Specialist: "x", Findings: []Finding{}}}) {
		t.Fatal("no findings should be false")
	}
	if !SpecialistsHaveAnyFindings([]SpecialistResult{{Specialist: "x", Findings: []Finding{{Path: "a.go", Line: 1, Comment: "x"}}}}) {
		t.Fatal("one finding should be true")
	}
}

func TestFindingIsInlinePostable(t *testing.T) {
	if !findingIsInlinePostable(Finding{Path: "x.go", Line: 1, Comment: "ok"}) {
		t.Fatal("expected postable")
	}
	if findingIsInlinePostable(Finding{Path: "", Line: 0, Comment: "g"}) {
		t.Fatal("general should not be postable")
	}
	if findingIsInlinePostable(Finding{Path: "x.go", Line: 1, Comment: "  "}) {
		t.Fatal("empty comment not postable")
	}
}
