package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// R6 follow-up: migrate the remaining `gh pr view/diff/list` sugar shell-outs
// onto go-gh's in-process REST/GraphQL transport. git subprocesses stay as-is.

// CheckAuthViaAPI verifies gh is installed, recent enough, and the stored
// credentials work by resolving the viewer login in-process (no `gh auth
// status` subprocess).
func checkAuthViaAPI(ctx context.Context) error {
	if _, err := ViewerLogin(ctx); err != nil {
		return fmt.Errorf("gh auth: not logged in or token invalid — run `gh auth login`: %w", err)
	}
	return nil
}

// getPullDiff fetches the unified diff for a PR via the REST API with the
// diff Accept header — same role as `gh pr diff`.
func getPullDiff(ctx context.Context, ref Ref) (string, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", ref.Owner, ref.Repo, ref.Number)
	out, err := ghAPIGetAccept(ctx, path, "application/vnd.github.v3.diff")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// getPRViaGraphQL loads the rich PR view GetPR needs, including
// statusCheckRollup, without shelling out to `gh pr view`.
func getPRViaGraphQL(ctx context.Context, ref Ref) (*PR, error) {
	const q = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      number title url body isDraft createdAt updatedAt
      additions deletions changedFiles
      baseRefName headRefName headRefOid
      author { login }
      reviewDecision
      latestReviews(first: 20) {
        nodes { author { login } state }
      }
      reviewRequests(first: 20) {
        nodes {
          __typename
          ... on User { login }
          ... on Team { slug }
        }
      }
      commits(last: 1) {
        nodes {
          commit {
            statusCheckRollup {
              contexts(first: 100) {
                pageInfo { hasNextPage }
                nodes {
                  __typename
                  ... on CheckRun { status conclusion name detailsUrl }
                  ... on StatusContext { state context targetUrl }
                }
              }
            }
          }
        }
      }
    }
  }
  viewer { login }
}`
	var data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		Repository struct {
			PullRequest struct {
				Number       int    `json:"number"`
				Title        string `json:"title"`
				URL          string `json:"url"`
				Body         string `json:"body"`
				BaseRefName  string `json:"baseRefName"`
				HeadRefName  string `json:"headRefName"`
				HeadRefOid   string `json:"headRefOid"`
				IsDraft      bool   `json:"isDraft"`
				CreatedAt    string `json:"createdAt"`
				UpdatedAt    string `json:"updatedAt"`
				Additions    int    `json:"additions"`
				Deletions    int    `json:"deletions"`
				ChangedFiles int    `json:"changedFiles"`
				Author       struct {
					Login string `json:"login"`
				} `json:"author"`
				ReviewDecision string `json:"reviewDecision"`
				LatestReviews  struct {
					Nodes []struct {
						Author struct {
							Login string `json:"login"`
						} `json:"author"`
						State string `json:"state"`
					} `json:"nodes"`
				} `json:"latestReviews"`
				ReviewRequests struct {
					Nodes []struct {
						Typename string `json:"__typename"`
						Login    string `json:"login"`
						Slug     string `json:"slug"`
					} `json:"nodes"`
				} `json:"reviewRequests"`
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup struct {
								Contexts struct {
									PageInfo pageInfo          `json:"pageInfo"`
									Nodes    []json.RawMessage `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{
		"owner":  ref.Owner,
		"name":   ref.Repo,
		"number": ref.Number,
	}
	if err := graphQLQueryInto(ctx, q, vars, &data); err != nil {
		return nil, err
	}
	raw := data.Repository.PullRequest
	if raw.Number == 0 {
		return nil, fmt.Errorf("pull request #%d not found in %s/%s", ref.Number, ref.Owner, ref.Repo)
	}
	viewer := strings.TrimSpace(data.Viewer.Login)
	cacheViewerLogin(viewer)

	latest := make([]LatestReview, 0, len(raw.LatestReviews.Nodes))
	for _, lr := range raw.LatestReviews.Nodes {
		latest = append(latest, LatestReview{
			AuthorLogin: lr.Author.Login,
			State:       lr.State,
		})
	}
	requests := make([]ReviewRequest, 0, len(raw.ReviewRequests.Nodes))
	for _, rr := range raw.ReviewRequests.Nodes {
		switch rr.Typename {
		case "User":
			requests = append(requests, ReviewRequest{Login: rr.Login})
		case "Team":
			requests = append(requests, ReviewRequest{TeamSlug: rr.Slug})
		}
	}

	rollupStates := collapseGraphQLCheckContexts(raw.Commits.Nodes)
	createdAt, _ := time.Parse(time.RFC3339, raw.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, raw.UpdatedAt)

	return &PR{
		Number:       raw.Number,
		Title:        raw.Title,
		URL:          raw.URL,
		Body:         raw.Body,
		Repository:   ref.Owner + "/" + ref.Repo,
		Owner:        ref.Owner,
		Repo:         ref.Repo,
		Author:       raw.Author.Login,
		BaseRef:      raw.BaseRefName,
		HeadRef:      raw.HeadRefName,
		HeadSHA:      raw.HeadRefOid,
		IsDraft:      raw.IsDraft,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		Additions:    raw.Additions,
		Deletions:    raw.Deletions,
		ChangedFiles: raw.ChangedFiles,
		ChecksState:  CollapseChecksRollup(rollupStates),
		ReviewState:  DeriveReviewState(viewer, raw.ReviewDecision, latest, requests),
	}, nil
}

func graphQLQueryInto(ctx context.Context, query string, vars map[string]any, out any) error {
	c, err := newGraphQLClient()
	if err != nil {
		return fmt.Errorf("gh graphql client: %w", err)
	}
	start := time.Now()
	derr := c.DoWithContext(ctx, query, vars, out)
	applog.GHInvocation([]string{"api", "graphql"}, time.Since(start), derr)
	return derr
}

// collapseGraphQLCheckContexts maps the GraphQL statusCheckRollup contexts
// nodes into the flat state strings CollapseChecksRollup expects.
func collapseGraphQLCheckContexts(commits []struct {
	Commit struct {
		StatusCheckRollup struct {
			Contexts struct {
				PageInfo pageInfo          `json:"pageInfo"`
				Nodes    []json.RawMessage `json:"nodes"`
			} `json:"contexts"`
		} `json:"statusCheckRollup"`
	} `json:"commit"`
}) []string {
	if len(commits) == 0 {
		return nil
	}
	ctxs := commits[0].Commit.StatusCheckRollup.Contexts
	if ctxs.PageInfo.HasNextPage {
		applog.Warn("statusCheckRollup contexts truncated (first 100 only)")
	}
	var states []string
	for _, raw := range ctxs.Nodes {
		var probe struct {
			Typename   string `json:"__typename"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			continue
		}
		switch probe.Typename {
		case "CheckRun":
			switch {
			case probe.Conclusion != "":
				states = append(states, probe.Conclusion)
			case probe.Status != "" && !strings.EqualFold(probe.Status, "COMPLETED"):
				states = append(states, "PENDING")
			}
		case "StatusContext":
			if probe.State != "" {
				states = append(states, probe.State)
			}
		}
	}
	return states
}

// listMergedPRsGraphQL returns recently merged PRs without `gh pr list`.
func listMergedPRsGraphQL(ctx context.Context, owner, repo string, limit int) ([]MergedPRDigestRow, error) {
	if limit < 1 {
		limit = 30
	}
	const q = `query($owner: String!, $name: String!, $first: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequests(states: [MERGED], first: $first, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes { number title url body updatedAt }
    }
  }
}`
	var data struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
					Number    int    `json:"number"`
					Title     string `json:"title"`
					URL       string `json:"url"`
					Body      string `json:"body"`
					UpdatedAt string `json:"updatedAt"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := graphQLQueryInto(ctx, q, map[string]any{
		"owner": owner, "name": repo, "first": limit,
	}, &data); err != nil {
		return nil, fmt.Errorf("list merged PRs: %w", err)
	}
	rows := make([]MergedPRDigestRow, 0, len(data.Repository.PullRequests.Nodes))
	for _, r := range data.Repository.PullRequests.Nodes {
		updatedAt, _ := time.Parse(time.RFC3339, r.UpdatedAt)
		rows = append(rows, MergedPRDigestRow{
			Number:        r.Number,
			Title:         strings.TrimSpace(r.Title),
			URL:           strings.TrimSpace(r.URL),
			BodyFirstLine: firstMeaningfulLine(r.Body),
			UpdatedAt:     updatedAt,
		})
	}
	return rows, nil
}

// prFilesGraphQL returns changed files for one PR without `gh pr view --json files`.
func prFilesGraphQL(ctx context.Context, owner, repo string, prNumber int) ([]PRFile, error) {
	const q = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      files(first: 100) {
        pageInfo { hasNextPage }
        nodes { path additions deletions }
      }
    }
  }
}`
	var data struct {
		Repository struct {
			PullRequest struct {
				Files struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []PRFile `json:"nodes"`
				} `json:"files"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := graphQLQueryInto(ctx, q, map[string]any{
		"owner": owner, "name": repo, "number": prNumber,
	}, &data); err != nil {
		return nil, fmt.Errorf("list files for #%d: %w", prNumber, err)
	}
	if data.Repository.PullRequest.Files.PageInfo.HasNextPage {
		applog.Warn("PR files truncated (first 100 only)", "repo", owner+"/"+repo, "pr", prNumber)
	}
	return data.Repository.PullRequest.Files.Nodes, nil
}
