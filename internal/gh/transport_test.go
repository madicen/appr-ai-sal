package gh

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	ghapi "github.com/cli/go-gh/v2/pkg/api"
)

// TestGraphQLQueryHelper exercises the single generic GraphQL helper (R6.1):
// the happy path unmarshals the `data` object into T, and a GraphQL-errors
// payload surfaces as a non-nil error.
func TestGraphQLQueryHelper(t *testing.T) {
	type viewerData struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}

	payload := `{"data":{"viewer":{"login":"octocat"}}}`
	stubGHResponder(t, func(*http.Request) (int, string) { return http.StatusOK, payload })

	got, err := graphQLQuery[viewerData](context.Background(), "query{viewer{login}}", nil)
	if err != nil {
		t.Fatalf("graphQLQuery: %v", err)
	}
	if got.Viewer.Login != "octocat" {
		t.Fatalf("login = %q, want octocat", got.Viewer.Login)
	}

	// Error payload → error.
	payload = `{"errors":[{"message":"bad query"}]}`
	if _, err := graphQLQuery[viewerData](context.Background(), "query{viewer{login}}", nil); err == nil {
		t.Fatal("expected error from graphql errors payload")
	}
}

func TestApiErrorFromHTTPError(t *testing.T) {
	he := &ghapi.HTTPError{
		StatusCode: 422,
		Message:    "Validation Failed",
		Errors: []ghapi.HTTPErrorItem{{
			Resource: "PullRequestReviewComment",
			Code:     "custom",
			Field:    "pull_request_review_thread.line",
			Message:  "could not be resolved",
		}},
		Headers: http.Header{"Retry-After": {"30"}},
	}
	ae := apiErrorFrom(he, "repos/o/r/pulls/1/comments")
	if ae == nil {
		t.Fatal("apiErrorFrom returned nil")
	}
	if ae.Status != 422 {
		t.Errorf("Status = %d, want 422", ae.Status)
	}
	if len(ae.Errors) != 1 || ae.Errors[0].Field != "pull_request_review_thread.line" {
		t.Errorf("error items not mapped: %+v", ae.Errors)
	}
	if !IsLineUnresolvable(ae) {
		t.Error("IsLineUnresolvable should match the mapped error")
	}
	if ae.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", ae.RetryAfter)
	}
	if !strings.Contains(strings.ToLower(ae.HumanReason), "refresh") {
		t.Errorf("HumanReason should mention refresh, got %q", ae.HumanReason)
	}
}

func TestApiErrorFromNonHTTPError(t *testing.T) {
	ae := apiErrorFrom(errors.New("connection refused"), "repos/o/r/pulls/1")
	if ae == nil {
		t.Fatal("apiErrorFrom returned nil for a plain error")
	}
	if ae.Status != 0 {
		t.Errorf("Status = %d, want 0 for a non-HTTP error", ae.Status)
	}
	if !strings.Contains(ae.Message, "connection refused") {
		t.Errorf("Message should carry the raw error, got %q", ae.Message)
	}
}

func TestRetryAfterFromHeaders(t *testing.T) {
	if got := retryAfterFromHeaders(http.Header{"Retry-After": {"5"}}); got != 5*time.Second {
		t.Errorf("delta-seconds: got %v, want 5s", got)
	}
	if got := retryAfterFromHeaders(nil); got != 0 {
		t.Errorf("nil headers: got %v, want 0", got)
	}
	if got := retryAfterFromHeaders(http.Header{"Retry-After": {"garbage"}}); got != 0 {
		t.Errorf("unparseable: got %v, want 0", got)
	}
}

// TestPostReviewMapsHTTPStatus confirms a non-2xx POST now surfaces the real
// HTTP status through the kept APIError taxonomy (R6.2), with the single inline
// comment attached for diagnostics.
func TestPostReviewMapsHTTPStatus(t *testing.T) {
	body := `{"message":"Validation Failed","errors":[{"resource":"PullRequestReviewComment","code":"custom","field":"pull_request_review_thread.line","message":"could not be resolved"}]}`
	stubGHResponder(t, func(r *http.Request) (int, string) {
		if r.Method != http.MethodPost {
			return http.StatusOK, `{}`
		}
		return http.StatusUnprocessableEntity, body
	})

	err := PostReview(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1}, Review{
		Event:    "COMMENT",
		Body:     "top",
		Comments: []ReviewComment{{Path: "a.go", Line: 3, Body: "c"}},
	})
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if ae.Status != 422 {
		t.Errorf("Status = %d, want 422", ae.Status)
	}
	if !IsLineUnresolvable(ae) {
		t.Error("IsLineUnresolvable should match")
	}
	if ae.Comment == nil || ae.Comment.Path != "a.go" {
		t.Errorf("single inline comment should be attached, got %+v", ae.Comment)
	}
}

// TestGetReviewThreadsPaginates verifies the reviewThreads cursor loop follows
// pageInfo across pages instead of truncating at the first page (R6.3).
func TestGetReviewThreadsPaginates(t *testing.T) {
	page1 := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":true,"endCursor":"CURSOR1"},"nodes":[{"isResolved":false,"isOutdated":false,"comments":{"pageInfo":{"hasNextPage":false},"totalCount":1,"nodes":[{"body":"one","path":"a.go","line":1,"author":{"login":"u1"}}]}}]}}}}}`
	page2 := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"isResolved":true,"isOutdated":false,"comments":{"pageInfo":{"hasNextPage":false},"totalCount":1,"nodes":[{"body":"two","path":"b.go","line":2,"author":{"login":"u2"}}]}}]}}}}}`
	var calls int
	stubGHResponder(t, func(r *http.Request) (int, string) {
		calls++
		if strings.Contains(readRequestBody(t, r), "CURSOR1") {
			return http.StatusOK, page2
		}
		return http.StatusOK, page1
	})

	threads, err := GetReviewThreads(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("GetReviewThreads: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 paginated calls, got %d", calls)
	}
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads across pages, got %d", len(threads))
	}
	if threads[0].Comments[0].Body != "one" || threads[1].Comments[0].Body != "two" {
		t.Fatalf("threads not accumulated in order: %+v", threads)
	}
}

// TestGetDiscussionPaginates verifies both the comments and reviews
// connections page independently (R6.3).
func TestGetDiscussionPaginates(t *testing.T) {
	page1 := `{"data":{"repository":{"pullRequest":{` +
		`"comments":{"pageInfo":{"hasNextPage":true,"endCursor":"CC1"},"nodes":[{"body":"c1","createdAt":"2026-01-01T00:00:00Z","url":"u1","author":{"login":"a"}}]},` +
		`"reviews":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"body":"r1","state":"COMMENTED","submittedAt":"2026-01-02T00:00:00Z","url":"ur1","author":{"login":"b"}}]}` +
		`}}}}`
	page2 := `{"data":{"repository":{"pullRequest":{` +
		`"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"body":"c2","createdAt":"2026-01-03T00:00:00Z","url":"u2","author":{"login":"a"}}]},` +
		`"reviews":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}` +
		`}}}}`
	var calls int
	stubGHResponder(t, func(r *http.Request) (int, string) {
		calls++
		if strings.Contains(readRequestBody(t, r), "CC1") {
			return http.StatusOK, page2
		}
		return http.StatusOK, page1
	})

	events, err := GetDiscussion(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("GetDiscussion: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 paginated calls, got %d", calls)
	}
	// c1 + r1 + c2 = 3 events (r1 has a body so it isn't dropped).
	if len(events) != 3 {
		t.Fatalf("expected 3 events across pages, got %d: %+v", len(events), events)
	}
}

// TestGetPRAgentDataFused confirms the fused prefetch is a SINGLE GraphQL call
// that populates checks + threads + discussion (R6.1 fusion).
func TestGetPRAgentDataFused(t *testing.T) {
	payload := `{"data":{"repository":{"pullRequest":{` +
		`"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"isResolved":false,"isOutdated":false,"comments":{"pageInfo":{"hasNextPage":false},"totalCount":1,"nodes":[{"body":"t","path":"a.go","line":1,"author":{"login":"u"}}]}}]},` +
		`"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"body":"c","createdAt":"2026-01-01T00:00:00Z","url":"u","author":{"login":"a"}}]},` +
		`"reviews":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"body":"rev","state":"COMMENTED","submittedAt":"2026-01-02T00:00:00Z","url":"ur","author":{"login":"b"}}]},` +
		`"commits":{"nodes":[{"commit":{"oid":"abc123","statusCheckRollup":{"state":"FAILURE","contexts":{"pageInfo":{"hasNextPage":false},"totalCount":1,"nodes":[{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"FAILURE"}]}}}}]}` +
		`}}}}`
	var calls int
	stubGHResponder(t, func(*http.Request) (int, string) {
		calls++
		return http.StatusOK, payload
	})

	data, err := GetPRAgentData(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("GetPRAgentData: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fused prefetch should be ONE call, got %d", calls)
	}
	if data.Checks == nil || data.Checks.RollupState != "FAILURE" || data.Checks.HeadSHA != "abc123" {
		t.Fatalf("checks not populated from fused query: %+v", data.Checks)
	}
	if len(data.Threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(data.Threads))
	}
	if len(data.Discussion) != 2 {
		t.Fatalf("discussion = %d, want 2 (comment + review body)", len(data.Discussion))
	}
}

// TestGetPRAgentDataOverflowFallback confirms that when the fused threads
// connection reports another page, GetPRAgentData falls back to the dedicated
// paginating fetcher rather than truncating.
func TestGetPRAgentDataOverflowFallback(t *testing.T) {
	fused := `{"data":{"repository":{"pullRequest":{` +
		`"reviewThreads":{"pageInfo":{"hasNextPage":true,"endCursor":"T1"},"nodes":[{"isResolved":false,"isOutdated":false,"comments":{"pageInfo":{"hasNextPage":false},"totalCount":1,"nodes":[{"body":"t1","path":"a.go","line":1,"author":{"login":"u"}}]}}]},` +
		`"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},` +
		`"reviews":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},` +
		`"commits":{"nodes":[]}` +
		`}}}}`
	// Standalone reviewThreads follow-up (graphqlReviewThreadsQuery has no
	// "commits(last: 1)" — that's how we tell the two documents apart).
	threadsPage1 := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":true,"endCursor":"T1"},"nodes":[{"isResolved":false,"isOutdated":false,"comments":{"pageInfo":{"hasNextPage":false},"nodes":[{"body":"t1","path":"a.go","line":1,"author":{"login":"u"}}]}}]}}}}}`
	threadsPage2 := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"isResolved":true,"isOutdated":false,"comments":{"pageInfo":{"hasNextPage":false},"nodes":[{"body":"t2","path":"b.go","line":2,"author":{"login":"u"}}]}}]}}}}}`
	stubGHResponder(t, func(r *http.Request) (int, string) {
		b := readRequestBody(t, r)
		switch {
		case strings.Contains(b, "commits(last: 1)"):
			return http.StatusOK, fused
		case strings.Contains(b, "T1"):
			return http.StatusOK, threadsPage2
		default:
			return http.StatusOK, threadsPage1
		}
	})

	data, err := GetPRAgentData(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("GetPRAgentData: %v", err)
	}
	if len(data.Threads) != 2 {
		t.Fatalf("overflow fallback should return both threads, got %d", len(data.Threads))
	}
}
