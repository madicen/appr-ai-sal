package gh

import (
	"strings"
	"testing"
)

func TestGraphQLChecksQueryPassesPRNumberToIsRequired(t *testing.T) {
	if !strings.Contains(graphqlChecksQuery, "isRequired(pullRequestNumber: $number)") {
		t.Fatal("graphqlChecksQuery must pass pullRequestNumber to isRequired on check contexts")
	}
}

func TestGraphQLReviewThreadsQueryOmitsInvalidDiffSide(t *testing.T) {
	if strings.Contains(graphqlReviewThreadsQuery, "diffSide") {
		t.Fatal("graphqlReviewThreadsQuery must not request diffSide — GitHub's PullRequestReviewComment type has no such field")
	}
}

func TestCollapseChecksRollup(t *testing.T) {
	cases := []struct {
		name   string
		states []string
		want   string
	}{
		{name: "empty", states: nil, want: ""},
		{name: "all success", states: []string{"SUCCESS", "SUCCESS"}, want: "SUCCESS"},
		{name: "any failure wins", states: []string{"SUCCESS", "FAILURE", "PENDING"}, want: "FAILURE"},
		{name: "error beats pending", states: []string{"PENDING", "ERROR"}, want: "ERROR"},
		{name: "pending beats success", states: []string{"PENDING", "SUCCESS"}, want: "PENDING"},
		{name: "neutral folds to success", states: []string{"NEUTRAL", "SKIPPED"}, want: "SUCCESS"},
		{name: "in-progress is pending", states: []string{"IN_PROGRESS", "QUEUED"}, want: "PENDING"},
		{name: "lowercase normalised", states: []string{"success", "failure"}, want: "FAILURE"},
		{name: "blanks ignored", states: []string{"", "SUCCESS"}, want: "SUCCESS"},
		{name: "unknown folds to pending (defensive)", states: []string{"WAT"}, want: "PENDING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CollapseChecksRollup(tc.states)
			if got != tc.want {
				t.Fatalf("CollapseChecksRollup(%v) = %q, want %q", tc.states, got, tc.want)
			}
		})
	}
}
