package gh

import "context"

// PRAgentData bundles the three PR-level signals the PR-agents prefetch needs:
// CI checks, inline review threads, and the conversation timeline.
//
// R6.1 fusion: this used to be three separate `gh api graphql` execs
// (GetChecks + GetReviewThreads + GetDiscussion). All three connections hang
// off the same repository.pullRequest node, so GetPRAgentData fetches the
// first page of each in ONE GraphQL document (3 execs → 1). Overflowing
// connections — rare — fall back to the dedicated paginating fetcher for just
// that connection, so the fused fast-path is a single round trip for the
// common case while still never silently truncating.
type PRAgentData struct {
	Checks     *ChecksReport
	Threads    []ReviewThread
	Discussion []DiscussionEvent
}

// fusedPRAgentData mirrors the `data` object of graphqlPRAgentDataQuery. The
// connection node/element types are shared with the standalone queries so the
// conversion helpers (checksReportFromData / threadsFromNodes /
// discussionEventsFrom) work unchanged.
type fusedPRAgentData struct {
	Repository struct {
		PullRequest struct {
			ReviewThreads reviewThreadConnection `json:"reviewThreads"`
			Comments      struct {
				PageInfo pageInfo                `json:"pageInfo"`
				Nodes    []discussionCommentNode `json:"nodes"`
			} `json:"comments"`
			Reviews struct {
				PageInfo pageInfo               `json:"pageInfo"`
				Nodes    []discussionReviewNode `json:"nodes"`
			} `json:"reviews"`
			Commits struct {
				Nodes []checksCommitNode `json:"nodes"`
			} `json:"commits"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// GetPRAgentData fetches checks + review threads + discussion in a single
// GraphQL call. See PRAgentData for the fusion rationale.
func GetPRAgentData(ctx context.Context, ref Ref) (PRAgentData, error) {
	data, err := graphQLQuery[fusedPRAgentData](ctx, graphqlPRAgentDataQuery, map[string]any{
		"owner":  ref.Owner,
		"name":   ref.Repo,
		"number": ref.Number,
	})
	if err != nil {
		return PRAgentData{}, err
	}
	pr := data.Repository.PullRequest
	out := PRAgentData{
		Checks: checksReportFromData(ref, pr.Commits.Nodes),
	}

	// Review threads: use the fused first page unless it overflowed, in which
	// case re-fetch the whole connection with the paginating helper.
	if pr.ReviewThreads.PageInfo.HasNextPage {
		threads, terr := GetReviewThreads(ctx, ref)
		if terr != nil {
			return out, terr
		}
		out.Threads = threads
	} else {
		out.Threads = threadsFromNodes(ref, pr.ReviewThreads.Nodes)
	}

	// Discussion: same strategy; either the fused page suffices for both
	// connections or we fall back to the fully-paginated GetDiscussion.
	if pr.Comments.PageInfo.HasNextPage || pr.Reviews.PageInfo.HasNextPage {
		disc, derr := GetDiscussion(ctx, ref)
		if derr != nil {
			return out, derr
		}
		out.Discussion = disc
	} else {
		out.Discussion = discussionEventsFrom(pr.Comments.Nodes, pr.Reviews.Nodes)
	}
	return out, nil
}

// graphqlPRAgentDataQuery is the single fused document behind GetPRAgentData:
// reviewThreads + comments + reviews + head-commit checks, all under one
// pullRequest node.
const graphqlPRAgentDataQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
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
      comments(first: 100) {
        pageInfo { hasNextPage endCursor }
        nodes {
          body
          createdAt
          url
          author { login }
        }
      }
      reviews(first: 100) {
        pageInfo { hasNextPage endCursor }
        nodes {
          body
          state
          submittedAt
          url
          author { login }
        }
      }
      commits(last: 1) {
        nodes {
          commit {
            oid
            statusCheckRollup {
              state
              contexts(first: 50) {
                pageInfo { hasNextPage }
                totalCount
                nodes {
                  __typename
                  ... on CheckRun {
                    name
                    status
                    conclusion
                    startedAt
                    completedAt
                    detailsUrl
                    title
                    summary
                    checkSuite { app { name } }
                    annotations(first: 10) {
                      pageInfo { hasNextPage }
                      nodes {
                        path
                        location { start { line } }
                        message
                        annotationLevel
                      }
                    }
                  }
                  ... on StatusContext {
                    context
                    state
                    description
                    targetUrl
                    createdAt
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`
