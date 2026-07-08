package gh

import (
	"context"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// ReviewThreadComment is one comment inside an inline review thread. Unlike
// the flat ListPullReviewComments view, these carry enough thread context for
// the Discussion PR agent to judge whether the concern was addressed: the
// path/line they were filed against and the author who raised them.
type ReviewThreadComment struct {
	Author string
	Body   string
	Path   string
	Line   int    // post-image line; falls back to the original line when the thread is outdated
	Side   string // diff side the comment anchors to (LEFT/RIGHT); "" when unknown
}

// ReviewThread is a single inline review-comment thread on a PR. GitHub tracks
// whether the human reviewer marked it resolved (IsResolved) and whether the
// anchored code has since changed out from under it (IsOutdated). The
// Discussion agent uses unresolved threads as the list of suggestions that may
// still need to be addressed in code.
//
// ID is the thread's GraphQL node id (PullRequestReviewThread.id). B3's
// thread-aware posting reuses it directly as the pullRequestReviewThreadId
// argument to the addPullRequestReviewThreadReply mutation (see
// ReplyToReviewThread), so an in-thread reply never needs to re-fetch the
// thread. It is empty only for threads parsed from a payload that predates the
// id being requested.
type ReviewThread struct {
	ID         string
	IsResolved bool
	IsOutdated bool
	Comments   []ReviewThreadComment
}

// reviewThreadsData mirrors the `data` object of graphqlReviewThreadsQuery.
// It carries pageInfo so GetReviewThreads can cursor-loop instead of silently
// truncating at the first 100 threads (R6.3).
type reviewThreadsData struct {
	Repository struct {
		PullRequest struct {
			ReviewThreads reviewThreadConnection `json:"reviewThreads"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

type reviewThreadConnection struct {
	PageInfo pageInfo           `json:"pageInfo"`
	Nodes    []reviewThreadNode `json:"nodes"`
}

type reviewThreadNode struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	IsOutdated bool   `json:"isOutdated"`
	Comments   struct {
		PageInfo   pageInfo `json:"pageInfo"`
		TotalCount int      `json:"totalCount"`
		Nodes      []struct {
			Body         string `json:"body"`
			Path         string `json:"path"`
			Line         *int   `json:"line"`
			OriginalLine *int   `json:"originalLine"`
			DiffSide     string `json:"diffSide"`
			Author       struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
}

// pageInfo is the standard GraphQL forward-pagination cursor block.
type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// GetReviewThreads fetches the PR's inline review threads (with resolved /
// outdated state and the comments inside each). It cursor-loops over the
// reviewThreads connection so PRs with more than one page of threads are no
// longer silently truncated. Returns an empty slice when the PR has no inline
// threads.
func GetReviewThreads(ctx context.Context, ref Ref) ([]ReviewThread, error) {
	var threads []ReviewThread
	cursor := ""
	for {
		data, err := graphQLQuery[reviewThreadsData](ctx, graphqlReviewThreadsQuery, map[string]any{
			"owner":  ref.Owner,
			"name":   ref.Repo,
			"number": ref.Number,
			"cursor": nullableCursor(cursor),
		})
		if err != nil {
			return nil, err
		}
		conn := data.Repository.PullRequest.ReviewThreads
		threads = append(threads, threadsFromNodes(ref, conn.Nodes)...)
		if !conn.PageInfo.HasNextPage || conn.PageInfo.EndCursor == "" {
			break
		}
		cursor = conn.PageInfo.EndCursor
	}
	return threads, nil
}

// threadsFromNodes converts parsed thread nodes into ReviewThreads. A thread
// with more comments than the single page we fetch (first: 50) is uncommon; a
// full nested cursor loop per thread is overkill, so we log an explicit
// overflow warning (R6.3) rather than paginate — the discussion agent still
// gets the first 50, which is more than enough to judge whether a concern was
// addressed.
func threadsFromNodes(ref Ref, nodes []reviewThreadNode) []ReviewThread {
	threads := make([]ReviewThread, 0, len(nodes))
	for _, t := range nodes {
		if t.Comments.PageInfo.HasNextPage {
			applog.Warn("review thread comments truncated",
				"ref", ref.String(), "fetched", len(t.Comments.Nodes), "total", t.Comments.TotalCount)
		}
		thread := ReviewThread{ID: t.ID, IsResolved: t.IsResolved, IsOutdated: t.IsOutdated}
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
				Side:   c.DiffSide,
			})
		}
		threads = append(threads, thread)
	}
	return threads
}

// nullableCursor returns nil for an empty cursor so the GraphQL `after`
// argument serializes as JSON null (fetch the first page) rather than an empty
// string, which GitHub rejects as an invalid cursor.
func nullableCursor(c string) any {
	if strings.TrimSpace(c) == "" {
		return nil
	}
	return c
}

const graphqlReviewThreadsQuery = `query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          isResolved
          isOutdated
          comments(first: 50) {
            pageInfo { hasNextPage }
            totalCount
            nodes {
              body
              path
              line
              originalLine
              diffSide
              author { login }
            }
          }
        }
      }
    }
  }
}`
