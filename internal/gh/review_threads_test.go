package gh

import (
	"context"
	"testing"
)

func TestGetReviewThreadsParsesResolvedState(t *testing.T) {
	payload := `{
  "data": {
    "repository": {
      "pullRequest": {
        "reviewThreads": {
          "nodes": [
            {
              "isResolved": false,
              "isOutdated": true,
              "comments": {
                "nodes": [
                  {"body": "please return an error", "path": "run.go", "line": null, "originalLine": 42, "author": {"login": "bob"}}
                ]
              }
            },
            {
              "isResolved": true,
              "isOutdated": false,
              "comments": {
                "nodes": [
                  {"body": "nit fixed", "path": "main.go", "line": 7, "originalLine": 7, "author": {"login": "carol"}}
                ]
              }
            }
          ]
        }
      }
    }
  }
}`
	prev := runGraphQL
	runGraphQL = func(_ context.Context, _ string, _ map[string]string) ([]byte, error) {
		return []byte(payload), nil
	}
	defer func() { runGraphQL = prev }()

	threads, err := GetReviewThreads(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("GetReviewThreads: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}

	first := threads[0]
	if first.IsResolved {
		t.Fatalf("first thread should be unresolved")
	}
	if !first.IsOutdated {
		t.Fatalf("first thread should be outdated")
	}
	if len(first.Comments) != 1 {
		t.Fatalf("first thread comments = %d want 1", len(first.Comments))
	}
	c := first.Comments[0]
	if c.Author != "bob" || c.Path != "run.go" {
		t.Fatalf("comment author/path = %q/%q want bob/run.go", c.Author, c.Path)
	}
	// line is null, so we fall back to originalLine (42).
	if c.Line != 42 {
		t.Fatalf("comment line = %d want 42 (fallback to originalLine)", c.Line)
	}

	if !threads[1].IsResolved {
		t.Fatalf("second thread should be resolved")
	}
	if threads[1].Comments[0].Line != 7 {
		t.Fatalf("second thread line = %d want 7", threads[1].Comments[0].Line)
	}
}

func TestGetReviewThreadsSurfacesGraphQLErrors(t *testing.T) {
	prev := runGraphQL
	runGraphQL = func(_ context.Context, _ string, _ map[string]string) ([]byte, error) {
		return []byte(`{"errors":[{"message":"boom"}]}`), nil
	}
	defer func() { runGraphQL = prev }()

	if _, err := GetReviewThreads(context.Background(), Ref{Owner: "o", Repo: "r", Number: 1}); err == nil {
		t.Fatalf("expected error from graphql errors payload")
	}
}
