package gh

import (
	"context"
	"encoding/json"
	"fmt"
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

// GetDiscussion fetches the PR's issue comments + review-summary bodies in
// one GraphQL round trip and returns them merged + chronologically sorted.
// Reviews whose body is blank (the "Approved with no comment" case) are
// dropped so they don't clutter the timeline.
func GetDiscussion(ctx context.Context, ref Ref) ([]DiscussionEvent, error) {
	out, err := runGraphQL(ctx, graphqlDiscussionQuery, map[string]string{
		"owner":  ref.Owner,
		"name":   ref.Repo,
		"number": fmt.Sprintf("%d", ref.Number),
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Comments struct {
						Nodes []struct {
							Body      string `json:"body"`
							CreatedAt string `json:"createdAt"`
							URL       string `json:"url"`
							Author    struct {
								Login string `json:"login"`
							} `json:"author"`
						} `json:"nodes"`
					} `json:"comments"`
					Reviews struct {
						Nodes []struct {
							Body        string `json:"body"`
							State       string `json:"state"`
							SubmittedAt string `json:"submittedAt"`
							URL         string `json:"url"`
							Author      struct {
								Login string `json:"login"`
							} `json:"author"`
						} `json:"nodes"`
					} `json:"reviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse discussion response: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("graphql discussion: %s", strings.Join(msgs, "; "))
	}
	pr := resp.Data.Repository.PullRequest
	events := make([]DiscussionEvent, 0, len(pr.Comments.Nodes)+len(pr.Reviews.Nodes))
	for _, c := range pr.Comments.Nodes {
		t, _ := time.Parse(time.RFC3339, c.CreatedAt)
		events = append(events, DiscussionEvent{
			Kind:   DiscussionComment,
			Author: c.Author.Login,
			Body:   strings.TrimSpace(c.Body),
			When:   t,
			URL:    c.URL,
		})
	}
	for _, r := range pr.Reviews.Nodes {
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
	return events, nil
}

const graphqlDiscussionQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      comments(first: 100) {
        nodes {
          body
          createdAt
          url
          author { login }
        }
      }
      reviews(first: 100) {
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
