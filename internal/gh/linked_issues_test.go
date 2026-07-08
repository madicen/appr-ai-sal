package gh

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestParseClosingIssueRefs(t *testing.T) {
	body := `This PR does a thing.

Closes #12
fixes: #12
Resolves owner2/repo2#34
See also fixed https://github.com/other/proj/issues/56
mentions #99 without a keyword
Fixes #not-a-number`
	refs := parseClosingIssueRefs(body, "acme", "widget")

	// Expect: #12 (default repo, de-duped once), owner2/repo2#34, other/proj#56.
	// The bare "#99" is NOT preceded by a closing keyword; the "#not-a-number"
	// is not numeric.
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3: %+v", len(refs), refs)
	}
	want := map[string]bool{
		"acme/widget#12":  true,
		"owner2/repo2#34": true,
		"other/proj#56":   true,
	}
	for _, r := range refs {
		k := issueKey(r.owner+"/"+r.repo, r.number)
		if !want[k] {
			t.Errorf("unexpected ref %q", k)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("missing refs: %v", want)
	}
}

func TestGetLinkedIssuesClosingReferences(t *testing.T) {
	stubGraphQL(t, `{
  "data": {
    "repository": {
      "pullRequest": {
        "closingIssuesReferences": {
          "nodes": [
            {"number": 5, "title": "Fix the widget", "body": "widget is broken", "state": "OPEN", "repository": {"nameWithOwner": "acme/widget"}}
          ]
        }
      }
    }
  }
}`)

	issues, err := GetLinkedIssues(context.Background(), Ref{Owner: "acme", Repo: "widget", Number: 100}, "no keywords here")
	if err != nil {
		t.Fatalf("GetLinkedIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].Ref() != "acme/widget#5" || issues[0].Title != "Fix the widget" {
		t.Fatalf("unexpected issue %+v", issues[0])
	}
}

func TestGetLinkedIssuesKeywordFetch(t *testing.T) {
	// closingIssuesReferences returns nothing; the body links a cross-repo
	// issue via a keyword, so the single-issue query must be used to fetch it.
	stubGHResponder(t, func(r *http.Request) (int, string) {
		body := readRequestBody(t, r)
		switch {
		case strings.Contains(body, "closingIssuesReferences"):
			return http.StatusOK, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[]}}}}}`
		case strings.Contains(body, "issue(number:"):
			return http.StatusOK, `{"data":{"repository":{"issue":{"number":7,"title":"Cross-repo bug","body":"details","state":"CLOSED","repository":{"nameWithOwner":"other/proj"}}}}}`
		default:
			return http.StatusOK, `{"data":{}}`
		}
	})

	issues, err := GetLinkedIssues(context.Background(), Ref{Owner: "acme", Repo: "widget", Number: 101}, "Fixes other/proj#7")
	if err != nil {
		t.Fatalf("GetLinkedIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].Ref() != "other/proj#7" || issues[0].State != "CLOSED" {
		t.Fatalf("unexpected issue %+v", issues[0])
	}
}

func TestGetLinkedIssuesSurfacesPrimaryError(t *testing.T) {
	stubGraphQL(t, `{"errors":[{"message":"boom"}]}`)
	if _, err := GetLinkedIssues(context.Background(), Ref{Owner: "acme", Repo: "widget", Number: 102}, "closes #1"); err == nil {
		t.Fatalf("expected error from closingIssuesReferences graphql failure")
	}
}

func TestGetLinkedIssuesFailOpenOnPerIssueError(t *testing.T) {
	// The primary query succeeds (empty); a keyword-referenced issue fails to
	// fetch. That issue must be dropped fail-open, leaving zero issues and no
	// error — a private / deleted issue never breaks the review.
	stubGHResponder(t, func(r *http.Request) (int, string) {
		body := readRequestBody(t, r)
		if strings.Contains(body, "closingIssuesReferences") {
			return http.StatusOK, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[]}}}}}`
		}
		// Single-issue fetch: simulate a private / missing issue.
		return http.StatusOK, `{"errors":[{"message":"Could not resolve to an Issue"}]}`
	})

	issues, err := GetLinkedIssues(context.Background(), Ref{Owner: "acme", Repo: "widget", Number: 103}, "fixes #404")
	if err != nil {
		t.Fatalf("per-issue failure must be fail-open, got err: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("got %d issues, want 0 (inaccessible issue dropped)", len(issues))
	}
}
