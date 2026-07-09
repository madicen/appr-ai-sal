package gh

import (
	"context"
	"strings"
	"testing"
)

// TestListModeQueryShapes pins the wire-level GitHub search queries we
// build for each ListMode. Drift here changes which PRs the TUI lists,
// so the assertions are deliberately blunt — exact substrings rather
// than a regex.
func TestListModeQueryShapes(t *testing.T) {
	cases := []struct {
		name    string
		mode    ListMode
		want    string
		notWant string
	}{
		{
			name:    "ReviewTeams uses review-requested:@me",
			mode:    ListModeReviewTeams,
			want:    "review-requested:@me",
			notWant: "author:@me",
		},
		{
			name:    "ReviewExplicit uses the same review-requested:@me query (narrow is client-side)",
			mode:    ListModeReviewExplicit,
			want:    "review-requested:@me",
			notWant: "author:@me",
		},
		{
			name:    "Authored uses author:@me",
			mode:    ListModeAuthored,
			want:    "author:@me",
			notWant: "review-requested:@me",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := listModeQuery(c.mode)
			if !strings.Contains(got, c.want) {
				t.Fatalf("query %q missing %q", got, c.want)
			}
			if c.notWant != "" && strings.Contains(got, c.notWant) {
				t.Fatalf("query %q contains forbidden %q", got, c.notWant)
			}
			if !strings.Contains(got, "is:pr") {
				t.Fatalf("query %q missing is:pr scope", got)
			}
			if !strings.Contains(got, "is:open") {
				t.Fatalf("query %q missing is:open scope", got)
			}
		})
	}
}

// TestListPRsExplicitNarrowsByViewerStillRequested confirms the
// client-side narrow for ListModeReviewExplicit drops PRs where the
// viewer is only requested via a team. The other two modes pass the
// GraphQL response through unchanged.
func TestListPRsExplicitNarrowsByViewerStillRequested(t *testing.T) {
	// Three PRs: one direct request, one team-only, one with both.
	const payload = `{
      "data": {
        "viewer": {"login": "madicen"},
        "search": {"nodes": [
          {
            "number": 1, "title": "direct", "url": "u1", "repository": {"nameWithOwner": "o/r"},
            "author": {"login": "x"},
            "reviewRequests": {"nodes": [{"requestedReviewer": {"__typename": "User", "login": "madicen"}}]}
          },
          {
            "number": 2, "title": "team only", "url": "u2", "repository": {"nameWithOwner": "o/r"},
            "author": {"login": "x"},
            "reviewRequests": {"nodes": [{"requestedReviewer": {"__typename": "Team", "slug": "team-a"}}]}
          },
          {
            "number": 3, "title": "both", "url": "u3", "repository": {"nameWithOwner": "o/r"},
            "author": {"login": "x"},
            "reviewRequests": {"nodes": [
              {"requestedReviewer": {"__typename": "User", "login": "madicen"}},
              {"requestedReviewer": {"__typename": "Team", "slug": "team-a"}}
            ]}
          }
        ]}
      }
    }`

	stubGraphQL(t, payload)

	all, err := ListPRs(context.Background(), ListModeReviewTeams)
	if err != nil {
		t.Fatalf("ListPRs(teams): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListMode teams: got %d PRs, want 3", len(all))
	}

	explicit, err := ListPRs(context.Background(), ListModeReviewExplicit)
	if err != nil {
		t.Fatalf("ListPRs(explicit): %v", err)
	}
	if len(explicit) != 2 {
		t.Fatalf("ListMode explicit: got %d PRs, want 2 (direct + both)", len(explicit))
	}
	for _, pr := range explicit {
		if pr.Number == 2 {
			t.Fatalf("ListMode explicit should drop team-only PR #2; got %+v", explicit)
		}
	}
}
