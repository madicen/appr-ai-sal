package gh

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGetDiffUsesDiffAcceptHeader(t *testing.T) {
	ref := Ref{Owner: "o", Repo: "r", Number: 42}
	stubGHResponder(t, func(r *http.Request) (int, string) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github.v3.diff" {
			t.Fatalf("Accept header = %q, want application/vnd.github.v3.diff", got)
		}
		if !strings.Contains(r.URL.Path, "/pulls/42") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusOK, "diff-bytes\n"
	})
	out, err := GetDiff(context.Background(), ref)
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if out != "diff-bytes\n" {
		t.Fatalf("GetDiff = %q", out)
	}
}

func TestGetPRViaGraphQL(t *testing.T) {
	ref := Ref{Owner: "o", Repo: "r", Number: 7}
	payload := `{
  "data": {
    "viewer": {"login": "alice"},
    "repository": {
      "pullRequest": {
        "number": 7,
        "title": "Add feature",
        "url": "https://github.com/o/r/pull/7",
        "body": "Body text",
        "baseRefName": "main",
        "headRefName": "feat",
        "headRefOid": "abc123",
        "isDraft": false,
        "createdAt": "2025-01-01T00:00:00Z",
        "updatedAt": "2025-01-02T00:00:00Z",
        "additions": 10,
        "deletions": 2,
        "changedFiles": 1,
        "author": {"login": "bob"},
        "reviewDecision": "REVIEW_REQUIRED",
        "latestReviews": {"nodes": []},
        "reviewRequests": {"nodes": [{"requestedReviewer": {"__typename": "User", "login": "alice"}}]},
        "commits": {"nodes": [{"commit": {"statusCheckRollup": {"contexts": {"pageInfo": {"hasNextPage": false}, "nodes": [{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"}]}}}}]}
      }
    }
  }
}`
	stubGraphQL(t, payload)
	pr, err := GetPR(context.Background(), ref)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if pr.Title != "Add feature" || pr.HeadSHA != "abc123" {
		t.Fatalf("unexpected PR: %+v", pr)
	}
	if pr.ChecksState != "SUCCESS" {
		t.Fatalf("ChecksState = %q, want SUCCESS", pr.ChecksState)
	}
	if !pr.ReviewState.ViewerStillRequested {
		t.Fatalf("expected viewer still requested")
	}
}

func TestCheckAuthViaAPIRequiresViewer(t *testing.T) {
	viewerLoginMu.Lock()
	prev := viewerLoginCache
	viewerLoginCache = ""
	viewerLoginMu.Unlock()
	t.Cleanup(func() {
		viewerLoginMu.Lock()
		viewerLoginCache = prev
		viewerLoginMu.Unlock()
	})

	stubGHResponder(t, func(r *http.Request) (int, string) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/user") {
			body, _ := io.ReadAll(r.Body)
			t.Fatalf("unexpected request: %s %s body=%q", r.Method, r.URL.Path, body)
		}
		return http.StatusUnauthorized, `{"message":"Bad credentials"}`
	})
	err := checkAuthViaAPI(context.Background())
	if err == nil {
		t.Fatal("expected auth error")
	}
}
