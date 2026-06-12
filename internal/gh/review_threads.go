package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ReviewThreadComment is one comment inside an inline review thread. Unlike
// the flat ListPullReviewComments view, these carry enough thread context for
// the Discussion PR agent to judge whether the concern was addressed: the
// path/line they were filed against and the author who raised them.
type ReviewThreadComment struct {
	Author string
	Body   string
	Path   string
	Line   int // post-image line; falls back to the original line when the thread is outdated
}

// ReviewThread is a single inline review-comment thread on a PR. GitHub tracks
// whether the human reviewer marked it resolved (IsResolved) and whether the
// anchored code has since changed out from under it (IsOutdated). The
// Discussion agent uses unresolved threads as the list of suggestions that may
// still need to be addressed in code.
type ReviewThread struct {
	IsResolved bool
	IsOutdated bool
	Comments   []ReviewThreadComment
}

// GetReviewThreads fetches the PR's inline review threads (with resolved /
// outdated state and the comments inside each) in one GraphQL round trip.
// Returns an empty slice when the PR has no inline threads.
//
// This is the one capability the rest of internal/gh does not already expose:
// GetDiscussion returns only top-level conversation comments and review
// summaries, and ListPullReviewComments returns a flat comment list with no
// thread / resolved state.
func GetReviewThreads(ctx context.Context, ref Ref) ([]ReviewThread, error) {
	out, err := runGraphQL(ctx, graphqlReviewThreadsQuery, map[string]string{
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
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							IsOutdated bool `json:"isOutdated"`
							Comments   struct {
								Nodes []struct {
									Body         string `json:"body"`
									Path         string `json:"path"`
									Line         *int   `json:"line"`
									OriginalLine *int   `json:"originalLine"`
									Author       struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse review threads response: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("graphql review threads: %s", strings.Join(msgs, "; "))
	}
	nodes := resp.Data.Repository.PullRequest.ReviewThreads.Nodes
	threads := make([]ReviewThread, 0, len(nodes))
	for _, t := range nodes {
		thread := ReviewThread{IsResolved: t.IsResolved, IsOutdated: t.IsOutdated}
		for _, c := range t.Comments.Nodes {
			line := 0
			if c.Line != nil {
				line = *c.Line
			} else if c.OriginalLine != nil {
				line = *c.OriginalLine
			}
			thread.Comments = append(thread.Comments, ReviewThreadComment{
				Author: c.Author.Login,
				Body:   strings.TrimSpace(c.Body),
				Path:   c.Path,
				Line:   line,
			})
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

const graphqlReviewThreadsQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          isResolved
          isOutdated
          comments(first: 50) {
            nodes {
              body
              path
              line
              originalLine
              author { login }
            }
          }
        }
      }
    }
  }
}`
