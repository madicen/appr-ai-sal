package gh

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestReplyToReviewThreadPostsMutation confirms the happy path issues the
// addPullRequestReviewThreadReply mutation carrying the thread id + body and
// treats a 200 payload as success.
func TestReplyToReviewThreadPostsMutation(t *testing.T) {
	var gotBody string
	stubGHResponder(t, func(r *http.Request) (int, string) {
		gotBody = readRequestBody(t, r)
		return http.StatusOK, `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"C_1","url":"https://example/1"}}}}`
	})

	err := ReplyToReviewThread(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1}, "PRRT_kw", "still present")
	if err != nil {
		t.Fatalf("ReplyToReviewThread: %v", err)
	}
	if !strings.Contains(gotBody, "addPullRequestReviewThreadReply") {
		t.Fatalf("request should carry the mutation, got %q", gotBody)
	}
	if !strings.Contains(gotBody, "PRRT_kw") || !strings.Contains(gotBody, "still present") {
		t.Fatalf("request should carry thread id + body variables, got %q", gotBody)
	}
}

// TestReplyToReviewThreadSurfacesErrors confirms a GraphQL-errors payload comes
// back as a non-nil error (fail-open: the caller reports it like a post
// failure) rather than being swallowed.
func TestReplyToReviewThreadSurfacesErrors(t *testing.T) {
	stubGraphQL(t, `{"errors":[{"message":"thread is resolved"}]}`)
	if err := ReplyToReviewThread(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1}, "PRRT_kw", "body"); err == nil {
		t.Fatal("expected error from graphql errors payload")
	}
}

// TestReplyToReviewThreadValidatesArgs confirms obviously-malformed calls fail
// locally without a round trip.
func TestReplyToReviewThreadValidatesArgs(t *testing.T) {
	if err := ReplyToReviewThread(context.Background(), Ref{}, "", "body"); err == nil {
		t.Fatal("empty thread id should error")
	}
	if err := ReplyToReviewThread(context.Background(), Ref{}, "PRRT_kw", "  "); err == nil {
		t.Fatal("empty body should error")
	}
}

// TestGetReviewThreadsExposesThreadID confirms the thread node id + comment
// diff side are parsed out (B3 reuses the id for the reply mutation).
func TestGetReviewThreadsExposesThreadID(t *testing.T) {
	payload := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[
      {"id":"PRRT_abc","isResolved":false,"isOutdated":false,"comments":{"pageInfo":{"hasNextPage":false},"totalCount":1,"nodes":[
        {"body":"please fix","path":"a.go","line":5,"diffSide":"RIGHT","author":{"login":"bob"}}
      ]}}
    ]}}}}}`
	stubGraphQL(t, payload)

	threads, err := GetReviewThreads(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("GetReviewThreads: %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "PRRT_abc" {
		t.Fatalf("thread id not parsed: %+v", threads)
	}
	if threads[0].Comments[0].Side != "RIGHT" {
		t.Fatalf("comment diff side not parsed: %+v", threads[0].Comments)
	}
}
