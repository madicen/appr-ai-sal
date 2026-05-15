package gh

import (
	"testing"
)

func TestDeriveReviewState_Approvals(t *testing.T) {
	latest := []LatestReview{
		{AuthorLogin: "alice", State: ReviewStateApproved},
		{AuthorLogin: "bob", State: ReviewStateCommented},
		{AuthorLogin: "carol", State: ReviewStateApproved},
	}
	rs := DeriveReviewState("madicen", ReviewDecisionReviewRequired, latest, nil)
	if rs.Approvals != 2 {
		t.Fatalf("Approvals = %d, want 2", rs.Approvals)
	}
	if rs.ChangesRequested != 0 {
		t.Fatalf("ChangesRequested = %d, want 0", rs.ChangesRequested)
	}
	if rs.ViewerHasApproved || rs.ViewerHasReviewed {
		t.Fatalf("viewer flags should be false, got approved=%v reviewed=%v", rs.ViewerHasApproved, rs.ViewerHasReviewed)
	}
	if !rs.NeedsViewerReview() {
		t.Fatal("NeedsViewerReview = false, want true (decision is REVIEW_REQUIRED and viewer hasn't reviewed)")
	}
}

func TestDeriveReviewState_ViewerApproved(t *testing.T) {
	latest := []LatestReview{
		{AuthorLogin: "alice", State: ReviewStateCommented},
		{AuthorLogin: "Madicen", State: ReviewStateApproved}, // case-insensitive match
	}
	rs := DeriveReviewState("madicen", ReviewDecisionReviewRequired, latest, nil)
	if !rs.ViewerHasReviewed {
		t.Fatal("ViewerHasReviewed = false, want true")
	}
	if !rs.ViewerHasApproved {
		t.Fatal("ViewerHasApproved = false, want true")
	}
	if rs.NeedsViewerReview() {
		t.Fatal("NeedsViewerReview = true, want false (viewer has approved)")
	}
}

func TestDeriveReviewState_ChangesRequested(t *testing.T) {
	latest := []LatestReview{
		{AuthorLogin: "alice", State: ReviewStateChangesRequested},
	}
	rs := DeriveReviewState("madicen", ReviewDecisionChangesRequested, latest, nil)
	if rs.ChangesRequested != 1 {
		t.Fatalf("ChangesRequested = %d, want 1", rs.ChangesRequested)
	}
	if !rs.NeedsViewerReview() {
		t.Fatal("NeedsViewerReview = false, want true (viewer hasn't reviewed and decision != APPROVED)")
	}
}

func TestDeriveReviewState_PRApprovedSuppressesNeedsYou(t *testing.T) {
	latest := []LatestReview{
		{AuthorLogin: "alice", State: ReviewStateApproved},
	}
	rs := DeriveReviewState("madicen", ReviewDecisionApproved, latest, nil)
	if rs.NeedsViewerReview() {
		t.Fatal("NeedsViewerReview = true, want false (decision is APPROVED)")
	}
}

func TestDeriveReviewState_ViewerStillRequested(t *testing.T) {
	requests := []ReviewRequest{
		{TeamSlug: "team-a"},
		{Login: "MADICEN"}, // case-insensitive match
	}
	rs := DeriveReviewState("madicen", ReviewDecisionReviewRequired, nil, requests)
	if !rs.ViewerStillRequested {
		t.Fatal("ViewerStillRequested = false, want true")
	}
}

func TestDeriveReviewState_EmptyViewerLeavesViewerFlagsFalse(t *testing.T) {
	latest := []LatestReview{
		{AuthorLogin: "alice", State: ReviewStateApproved},
	}
	requests := []ReviewRequest{
		{Login: "alice"},
	}
	rs := DeriveReviewState("", ReviewDecisionReviewRequired, latest, requests)
	if rs.ViewerHasReviewed || rs.ViewerHasApproved || rs.ViewerStillRequested {
		t.Fatalf("viewer flags should all be false with empty viewer; got reviewed=%v approved=%v requested=%v",
			rs.ViewerHasReviewed, rs.ViewerHasApproved, rs.ViewerStillRequested)
	}
	if rs.Approvals != 1 {
		t.Fatalf("Approvals = %d, want 1 (PR-wide counter still populated)", rs.Approvals)
	}
}

const sampleGraphQLResponse = `{
  "data": {
    "viewer": { "login": "madicen" },
    "search": {
      "nodes": [
        {
          "number": 101,
          "title": "needs you (direct)",
          "url": "https://github.com/o/r/pull/101",
          "body": "",
          "isDraft": false,
          "createdAt": "2026-05-10T10:00:00Z",
          "updatedAt": "2026-05-11T10:00:00Z",
          "author": { "login": "alice" },
          "repository": { "nameWithOwner": "o/r" },
          "reviewDecision": "REVIEW_REQUIRED",
          "latestReviews": { "nodes": [] },
          "reviewRequests": {
            "nodes": [
              { "requestedReviewer": { "__typename": "User", "login": "madicen" } },
              { "requestedReviewer": { "__typename": "Team", "slug": "team-a" } }
            ]
          }
        },
        {
          "number": 102,
          "title": "approved + needs more",
          "url": "https://github.com/o/r/pull/102",
          "body": "",
          "isDraft": false,
          "createdAt": "2026-05-09T10:00:00Z",
          "updatedAt": "2026-05-11T09:00:00Z",
          "author": { "login": "bob" },
          "repository": { "nameWithOwner": "o/r" },
          "reviewDecision": "REVIEW_REQUIRED",
          "latestReviews": {
            "nodes": [
              { "author": { "login": "carol" }, "state": "APPROVED" }
            ]
          },
          "reviewRequests": {
            "nodes": [
              { "requestedReviewer": { "__typename": "Team", "slug": "team-a" } }
            ]
          }
        },
        {
          "number": 103,
          "title": "fully approved",
          "url": "https://github.com/o/r/pull/103",
          "body": "",
          "isDraft": false,
          "createdAt": "2026-05-08T10:00:00Z",
          "updatedAt": "2026-05-08T10:00:00Z",
          "author": { "login": "dan" },
          "repository": { "nameWithOwner": "o/r" },
          "reviewDecision": "APPROVED",
          "latestReviews": {
            "nodes": [
              { "author": { "login": "carol" }, "state": "APPROVED" }
            ]
          },
          "reviewRequests": { "nodes": [] }
        }
      ]
    }
  }
}`

func TestParseReviewSearchResponse(t *testing.T) {
	prs, viewer, err := parseReviewSearchResponse([]byte(sampleGraphQLResponse))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if viewer != "madicen" {
		t.Fatalf("viewer = %q, want %q", viewer, "madicen")
	}
	if len(prs) != 3 {
		t.Fatalf("len(prs) = %d, want 3", len(prs))
	}
	// #101: direct request, viewer hasn't reviewed
	if !prs[0].ReviewState.ViewerStillRequested {
		t.Fatal("#101 ViewerStillRequested = false, want true")
	}
	if !prs[0].ReviewState.NeedsViewerReview() {
		t.Fatal("#101 NeedsViewerReview = false, want true")
	}
	if prs[0].ReviewState.Approvals != 0 {
		t.Fatalf("#101 Approvals = %d, want 0", prs[0].ReviewState.Approvals)
	}
	// #102: team-only request, has one approval, decision REVIEW_REQUIRED
	if prs[1].ReviewState.ViewerStillRequested {
		t.Fatal("#102 ViewerStillRequested = true, want false (team-only)")
	}
	if prs[1].ReviewState.Approvals != 1 {
		t.Fatalf("#102 Approvals = %d, want 1", prs[1].ReviewState.Approvals)
	}
	if !prs[1].ReviewState.NeedsViewerReview() {
		t.Fatal("#102 NeedsViewerReview = false, want true (decision still REVIEW_REQUIRED)")
	}
	// #103: fully approved, viewer hasn't reviewed but PR is done
	if prs[2].ReviewState.NeedsViewerReview() {
		t.Fatal("#103 NeedsViewerReview = true, want false (PR is APPROVED)")
	}
	// Owner/Repo split sanity check.
	if prs[0].Owner != "o" || prs[0].Repo != "r" {
		t.Fatalf("#101 owner/repo = %q/%q, want o/r", prs[0].Owner, prs[0].Repo)
	}
}

func TestParseReviewSearchResponse_GraphQLErrors(t *testing.T) {
	const body = `{"errors":[{"message":"bad query"}]}`
	_, _, err := parseReviewSearchResponse([]byte(body))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestParseReviewSearchResponseRichFields covers the new diff-stats and
// rollup-state fields the queue rows learned to render. It uses a minimal
// payload so the field-by-field assertions stay readable.
func TestParseReviewSearchResponseRichFields(t *testing.T) {
	const body = `{
  "data": {
    "viewer": { "login": "madicen" },
    "search": {
      "nodes": [
        {
          "number": 200, "title": "rich", "url": "https://github.com/o/r/pull/200",
          "body": "", "isDraft": false,
          "createdAt": "2026-05-10T10:00:00Z", "updatedAt": "2026-05-10T10:00:00Z",
          "additions": 142, "deletions": 37, "changedFiles": 6,
          "author": { "login": "alice" },
          "repository": { "nameWithOwner": "o/r" },
          "reviewDecision": "REVIEW_REQUIRED",
          "commits": {
            "nodes": [
              { "commit": { "statusCheckRollup": { "state": "FAILURE" } } }
            ]
          },
          "latestReviews": { "nodes": [] },
          "reviewRequests": { "nodes": [] }
        },
        {
          "number": 201, "title": "no rollup", "url": "https://github.com/o/r/pull/201",
          "body": "", "isDraft": false,
          "createdAt": "2026-05-10T10:00:00Z", "updatedAt": "2026-05-10T10:00:00Z",
          "additions": 0, "deletions": 0, "changedFiles": 0,
          "author": { "login": "alice" },
          "repository": { "nameWithOwner": "o/r" },
          "reviewDecision": "REVIEW_REQUIRED",
          "commits": { "nodes": [] },
          "latestReviews": { "nodes": [] },
          "reviewRequests": { "nodes": [] }
        }
      ]
    }
  }
}`
	prs, _, err := parseReviewSearchResponse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("len(prs) = %d, want 2", len(prs))
	}
	if prs[0].Additions != 142 || prs[0].Deletions != 37 || prs[0].ChangedFiles != 6 {
		t.Fatalf("rich PR diff stats: got +%d/-%d %d files, want +142/-37 6 files",
			prs[0].Additions, prs[0].Deletions, prs[0].ChangedFiles)
	}
	if prs[0].ChecksState != "FAILURE" {
		t.Fatalf("rich PR ChecksState = %q, want FAILURE", prs[0].ChecksState)
	}
	if prs[1].ChecksState != "" {
		t.Fatalf("PR with empty commits.nodes should have empty ChecksState; got %q", prs[1].ChecksState)
	}
}
