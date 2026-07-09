package gh

import (
	"context"
	"sort"
	"strings"
	"time"
)

// DiscussionEvent is one entry on a PR's "Conversation" tab — either a plain
// issue comment or a top-level review-summary body. We deliberately drop
// inline review-thread comments, label/state/timeline events, and empty
// "Approved without comment" reviews so the timeline stays focused on what
// the reviewer actually wrote.
type DiscussionEvent struct {
	Kind    DiscussionKind
	Author  string
	Body    string
	When    time.Time
	URL     string // permalink to the comment / review on GitHub
	Verdict string // only set for review-summary entries (APPROVED / CHANGES_REQUESTED / COMMENTED)
}

// DiscussionKind tags whether a DiscussionEvent originated as an issue
// comment or as the body of a submitted review. The center pane uses the
// kind to colour the byline (`@author commented` vs `@author requested
// changes`).
type DiscussionKind int

const (
	DiscussionComment DiscussionKind = iota
	DiscussionReview
)

// discussionCommentNode / discussionReviewNode mirror the two connections we
// pull from the PR's Conversation tab.
type discussionCommentNode struct {
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	URL       string `json:"url"`
	Author    struct {
		Login string `json:"login"`
	} `json:"author"`
}

type discussionReviewNode struct {
	Body        string `json:"body"`
	State       string `json:"state"`
	SubmittedAt string `json:"submittedAt"`
	URL         string `json:"url"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
}

// discussionData mirrors the `data` object of graphqlDiscussionQuery, with
// pageInfo on both connections so GetDiscussion can cursor-loop (R6.3).
type discussionData struct {
	Repository struct {
		PullRequest struct {
			Comments struct {
				PageInfo pageInfo                `json:"pageInfo"`
				Nodes    []discussionCommentNode `json:"nodes"`
			} `json:"comments"`
			Reviews struct {
				PageInfo pageInfo               `json:"pageInfo"`
				Nodes    []discussionReviewNode `json:"nodes"`
			} `json:"reviews"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// GetDiscussion fetches the PR's issue comments + review-summary bodies and
// returns them merged + chronologically sorted. Reviews whose body is blank
// (the "Approved with no comment" case) are dropped so they don't clutter the
// timeline. Both connections are cursor-looped so a long conversation is no
// longer truncated at the first 100 of each.
func GetDiscussion(ctx context.Context, ref Ref) ([]DiscussionEvent, error) {
	var comments []discussionCommentNode
	var reviews []discussionReviewNode
	commentCursor, reviewCursor := "", ""
	// Both connections page independently. We keep requesting until neither
	// has a next page; connections that are already exhausted just return an
	// empty page (after their endCursor) which we ignore.
	commentsDone, reviewsDone := false, false
	for !commentsDone || !reviewsDone {
		data, err := graphQLQuery[discussionData](ctx, graphqlDiscussionQuery, map[string]any{
			"owner":         ref.Owner,
			"name":          ref.Repo,
			"number":        ref.Number,
			"commentCursor": nullableCursor(commentCursor),
			"reviewCursor":  nullableCursor(reviewCursor),
		})
		if err != nil {
			return nil, err
		}
		cc := data.Repository.PullRequest.Comments
		rc := data.Repository.PullRequest.Reviews
		if !commentsDone {
			comments = append(comments, cc.Nodes...)
			if cc.PageInfo.HasNextPage && cc.PageInfo.EndCursor != "" {
				commentCursor = cc.PageInfo.EndCursor
			} else {
				commentsDone = true
			}
		}
		if !reviewsDone {
			reviews = append(reviews, rc.Nodes...)
			if rc.PageInfo.HasNextPage && rc.PageInfo.EndCursor != "" {
				reviewCursor = rc.PageInfo.EndCursor
			} else {
				reviewsDone = true
			}
		}
	}
	return discussionEventsFrom(comments, reviews), nil
}

// discussionEventsFrom merges the two node lists into the sorted event
// timeline. Shared by GetDiscussion and the fused PR-agent prefetch.
func discussionEventsFrom(comments []discussionCommentNode, reviews []discussionReviewNode) []DiscussionEvent {
	events := make([]DiscussionEvent, 0, len(comments)+len(reviews))
	for _, c := range comments {
		t, _ := time.Parse(time.RFC3339, c.CreatedAt)
		events = append(events, DiscussionEvent{
			Kind:   DiscussionComment,
			Author: c.Author.Login,
			Body:   strings.TrimSpace(c.Body),
			When:   t,
			URL:    c.URL,
		})
	}
	for _, r := range reviews {
		body := strings.TrimSpace(r.Body)
		if body == "" {
			// "Approved with no comment" — skip so the timeline stays
			// focused on what people actually wrote.
			continue
		}
		t, _ := time.Parse(time.RFC3339, r.SubmittedAt)
		events = append(events, DiscussionEvent{
			Kind:    DiscussionReview,
			Author:  r.Author.Login,
			Body:    body,
			When:    t,
			URL:     r.URL,
			Verdict: r.State,
		})
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].When.Before(events[j].When)
	})
	return events
}

const graphqlDiscussionQuery = `query($owner: String!, $name: String!, $number: Int!, $commentCursor: String, $reviewCursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      comments(first: 100, after: $commentCursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          body
          createdAt
          url
          author { login }
        }
      }
      reviews(first: 100, after: $reviewCursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          body
          state
          submittedAt
          url
          author { login }
        }
      }
    }
  }
}`
