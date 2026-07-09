package gh

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestGraphQLPRAgentDataQueryPassesPRNumberToIsRequired(t *testing.T) {
	if !strings.Contains(graphqlPRAgentDataQuery, "isRequired(pullRequestNumber: $number)") {
		t.Fatal("graphqlPRAgentDataQuery must pass pullRequestNumber to isRequired on check contexts")
	}
	if strings.Contains(graphqlPRAgentDataQuery, "diffSide") {
		t.Fatal("graphqlPRAgentDataQuery must not request diffSide — GitHub's PullRequestReviewComment type has no such field")
	}
}

// TestGetPRAgentDataIntegrationLive hits the real GitHub API when
// APPR_GH_LIVE=1. It guards the isRequired(pullRequestNumber) wiring that
// otherwise makes every PR-agent prefetch fail on repos with check runs.
func TestGetPRAgentDataIntegrationLive(t *testing.T) {
	if os.Getenv("APPR_GH_LIVE") != "1" {
		t.Skip("set APPR_GH_LIVE=1 to run live GitHub GraphQL integration test")
	}
	data, err := GetPRAgentData(context.Background(), Ref{Owner: "golang", Repo: "go", Number: 80328})
	if err != nil {
		t.Fatalf("GetPRAgentData: %v", err)
	}
	if data.Checks == nil {
		t.Fatal("Checks should never be nil after a successful fetch")
	}
	if data.Checks.HeadSHA == "" {
		t.Fatal("expected head SHA on a live PR")
	}
	if len(data.Checks.Runs) == 0 {
		t.Fatal("expected at least one check run on golang/go#80328")
	}
}
