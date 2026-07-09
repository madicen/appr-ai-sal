package gh

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// Review states GitHub assigns to a submitted review. We only branch on a
// small subset of these; the rest pass through as-is in the raw payload.
const (
	ReviewStateApproved         = "APPROVED"
	ReviewStateChangesRequested = "CHANGES_REQUESTED"
	ReviewStateCommented        = "COMMENTED"
	ReviewStateDismissed        = "DISMISSED"
	ReviewStatePending          = "PENDING"
)

// ReviewDecision values GitHub returns for a PR's overall review decision.
// Empty string means "not configured" (no branch protection requiring reviews).
const (
	ReviewDecisionApproved         = "APPROVED"
	ReviewDecisionChangesRequested = "CHANGES_REQUESTED"
	ReviewDecisionReviewRequired   = "REVIEW_REQUIRED"
)

// LatestReview is a per-author summary of the most recent submitted review
// on a PR. We only need who reviewed and what state they left it in for the
// derived flags; richer fields stay on PullReviewRow which is fetched
// separately for the full review-history digest.
type LatestReview struct {
	AuthorLogin string
	State       string
}

// ReviewRequest is a still-pending review request on a PR. The requested
// reviewer is either a user (Login set) or a team (TeamSlug set).
type ReviewRequest struct {
	Login    string // set when requested reviewer is a user
	TeamSlug string // set when requested reviewer is a team (owner/slug or just slug)
}

// IsUser reports whether the request targets an individual user (not a team).
func (r ReviewRequest) IsUser() bool { return r.Login != "" }

// DeriveReviewState computes the per-viewer flags we surface in the UI from
// the raw review payload. viewer is the gh login of the authenticated user;
// when empty the viewer-scoped flags are conservatively false (we can still
// report PR-wide signals like Approvals / ChangesRequested).
func DeriveReviewState(viewer string, decision string, latest []LatestReview, requests []ReviewRequest) ReviewState {
	out := ReviewState{
		Decision: strings.TrimSpace(decision),
	}
	viewer = strings.TrimSpace(viewer)
	for _, lr := range latest {
		switch lr.State {
		case ReviewStateApproved:
			out.Approvals++
		case ReviewStateChangesRequested:
			out.ChangesRequested++
		}
		if viewer != "" && strings.EqualFold(strings.TrimSpace(lr.AuthorLogin), viewer) {
			out.ViewerHasReviewed = true
			if lr.State == ReviewStateApproved {
				out.ViewerHasApproved = true
			}
		}
	}
	if viewer != "" {
		for _, rr := range requests {
			if rr.IsUser() && strings.EqualFold(strings.TrimSpace(rr.Login), viewer) {
				out.ViewerStillRequested = true
				break
			}
		}
	}
	return out
}

// ReviewState bundles the PR-level review summary and viewer-relative flags
// that the UI renders as badges. The fields are designed to degrade gracefully
// when we couldn't fetch one piece of data — zero values render as "nothing
// to report" rather than as a misleading state.
type ReviewState struct {
	// Decision is GitHub's overall reviewDecision: APPROVED, CHANGES_REQUESTED,
	// REVIEW_REQUIRED, or empty (no branch protection / not applicable).
	Decision string
	// Approvals counts users whose most recent submitted review is APPROVED.
	Approvals int
	// ChangesRequested counts users whose most recent submitted review is
	// CHANGES_REQUESTED. (A subsequent APPROVED review from the same user
	// supersedes their changes-requested, so we trust GitHub's "latest" view.)
	ChangesRequested int
	// ViewerHasReviewed reports whether the viewer has submitted any review
	// (regardless of state). Used to mute the "needs you" hint after the
	// user has already commented / approved.
	ViewerHasReviewed bool
	// ViewerHasApproved reports whether the viewer's latest review is APPROVED.
	ViewerHasApproved bool
	// ViewerStillRequested reports whether the viewer's login appears in the
	// PR's reviewRequests (i.e. an individual request still pending for them,
	// not just a team request).
	ViewerStillRequested bool
}

// NeedsViewerReview is the single high-signal flag the UI uses to put a PR
// at the top of the list and bold the "needs you" badge: the PR isn't yet
// approved overall, and the viewer hasn't reviewed it.
//
// The PR is in our queue because gh search matched review-requested:@me
// (directly or via a team), so we treat "not yet approved and you haven't
// weighed in" as "your review can unblock merge". When ViewerStillRequested
// is true we add visual weight in the renderer, but it isn't required here
// — team-only requests still count.
func (s ReviewState) NeedsViewerReview() bool {
	if s.ViewerHasReviewed {
		return false
	}
	if strings.EqualFold(s.Decision, ReviewDecisionApproved) {
		return false
	}
	return true
}

// graphqlReviewQuery is the single round-trip we use to list review-requested
// PRs along with the review state we need to render badges and sort by
// actionability. Compared to the previous REST search + per-PR
// requested_reviewers fan-out, this drops N+1 calls and adds the review
// decision / per-author state in one go.
const graphqlReviewQuery = `query($q: String!) {
  viewer { login }
  search(query: $q, type: ISSUE, first: 50) {
    pageInfo { hasNextPage }
    nodes {
      ... on PullRequest {
        number
        title
        url
        body
        isDraft
        createdAt
        updatedAt
        additions
        deletions
        changedFiles
        author { login }
        repository { nameWithOwner }
        reviewDecision
        commits(last: 1) {
          nodes {
            commit {
              statusCheckRollup { state }
            }
          }
        }
        latestReviews(first: 50) {
          nodes {
            author { login }
            state
          }
        }
        reviewRequests(first: 30) {
          nodes {
            requestedReviewer {
              __typename
              ... on User { login }
              ... on Team { slug }
            }
          }
        }
      }
    }
  }
}`

// graphqlReviewData mirrors the `data` object of graphqlReviewQuery's output.
// This is the T fed to graphQLQuery[graphqlReviewData] by ListPRs (go-gh
// unmarshals straight into it), and also the inner payload the raw-bytes
// parseReviewSearchResponse test-helper unwraps.
type graphqlReviewData struct {
	Viewer struct {
		Login string `json:"login"`
	} `json:"viewer"`
	Search struct {
		PageInfo pageInfo        `json:"pageInfo"`
		Nodes    []graphqlPRNode `json:"nodes"`
	} `json:"search"`
}

// graphqlReviewResponse is the full `{data, errors}` envelope. Retained so the
// pure-function parseReviewSearchResponse (exercised directly by tests with
// canned bytes) keeps working; the live path goes through graphQLQuery which
// lets go-gh handle the envelope.
type graphqlReviewResponse struct {
	Data   graphqlReviewData `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type graphqlPRNode struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	Body         string `json:"body"`
	IsDraft      bool   `json:"isDraft"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changedFiles"`
	Author       struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	ReviewDecision string `json:"reviewDecision"`
	// Commits exposes the head commit's statusCheckRollup. We ask for the
	// last commit only — the rollup on the head is what GitHub uses for
	// the merge gate. An empty `commits.nodes` (e.g. on a freshly opened
	// PR before the first push lands) is benign: we drop to ChecksState="".
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	LatestReviews struct {
		Nodes []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Typename string `json:"__typename"`
				Login    string `json:"login"`
				Slug     string `json:"slug"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
}

// CollapseChecksRollup reduces a list of per-check states / conclusions to a
// single PR-wide rollup state, using the same severity ladder GitHub does:
// any FAILURE wins; otherwise any ERROR / TIMED_OUT / CANCELLED / ACTION_REQUIRED;
// otherwise PENDING / IN_PROGRESS / QUEUED / EXPECTED; otherwise SUCCESS or
// NEUTRAL / SKIPPED if every entry was a no-op. Returns "" when the input
// is empty so callers can render "no checks".
func CollapseChecksRollup(states []string) string {
	if len(states) == 0 {
		return ""
	}
	var sawSuccess, sawNeutral, sawPending, sawError, sawFailure bool
	for _, s := range states {
		switch strings.ToUpper(strings.TrimSpace(s)) {
		case "":
			continue
		case "SUCCESS":
			sawSuccess = true
		case "NEUTRAL", "SKIPPED":
			sawNeutral = true
		case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED", "WAITING", "REQUESTED":
			sawPending = true
		case "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STALE":
			sawError = true
		case "FAILURE":
			sawFailure = true
		default:
			// Unknown state — don't pretend it's a pass.
			sawPending = true
		}
	}
	switch {
	case sawFailure:
		return "FAILURE"
	case sawError:
		return "ERROR"
	case sawPending:
		return "PENDING"
	case sawSuccess:
		return "SUCCESS"
	case sawNeutral:
		return "SUCCESS"
	}
	return ""
}

// parseReviewSearchResponse turns the GraphQL payload into the PR list plus
// the viewer login. It is the seam used by tests so we can exercise edge
// cases (empty arrays, missing author, team-only requests) without hitting
// the gh CLI.
func parseReviewSearchResponse(raw []byte) ([]PR, string, error) {
	var resp graphqlReviewResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, "", fmt.Errorf("parse graphql search: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, "", fmt.Errorf("graphql search: %s", strings.Join(msgs, "; "))
	}
	prs, viewer := reviewDataToPRs(resp.Data)
	return prs, viewer, nil
}

// reviewDataToPRs converts the parsed `data` object into the flattened PR list
// plus the viewer login. Shared by the live graphQLQuery path (ListPRs) and
// the raw-bytes parseReviewSearchResponse test seam so the node→PR mapping is
// defined exactly once.
func reviewDataToPRs(data graphqlReviewData) ([]PR, string) {
	viewer := strings.TrimSpace(data.Viewer.Login)
	// R6.3: the review queue is intentionally capped at the first 50 matches
	// (nobody reviews a 50+ PR backlog in one sitting); a full cursor loop is
	// overkill here, so we log an explicit overflow warning rather than
	// silently hiding that more PRs matched.
	if data.Search.PageInfo.HasNextPage {
		applog.Warn("review queue truncated at 50 PRs", "viewer", viewer)
	}
	prs := make([]PR, 0, len(data.Search.Nodes))
	for _, n := range data.Search.Nodes {
		owner, repoName := splitRepo(n.Repository.NameWithOwner)
		createdAt, _ := time.Parse(time.RFC3339, n.CreatedAt)
		updatedAt, _ := time.Parse(time.RFC3339, n.UpdatedAt)
		latest := make([]LatestReview, 0, len(n.LatestReviews.Nodes))
		for _, lr := range n.LatestReviews.Nodes {
			latest = append(latest, LatestReview{
				AuthorLogin: lr.Author.Login,
				State:       lr.State,
			})
		}
		reqs := make([]ReviewRequest, 0, len(n.ReviewRequests.Nodes))
		for _, rr := range n.ReviewRequests.Nodes {
			switch rr.RequestedReviewer.Typename {
			case "User":
				reqs = append(reqs, ReviewRequest{Login: rr.RequestedReviewer.Login})
			case "Team":
				reqs = append(reqs, ReviewRequest{TeamSlug: rr.RequestedReviewer.Slug})
			}
		}
		var checksState string
		if len(n.Commits.Nodes) > 0 {
			r := n.Commits.Nodes[len(n.Commits.Nodes)-1].Commit.StatusCheckRollup
			if r != nil {
				checksState = CollapseChecksRollup([]string{r.State})
			}
		}
		pr := PR{
			Number:       n.Number,
			Title:        n.Title,
			URL:          n.URL,
			Body:         n.Body,
			Repository:   n.Repository.NameWithOwner,
			Owner:        owner,
			Repo:         repoName,
			Author:       n.Author.Login,
			IsDraft:      n.IsDraft,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			Additions:    n.Additions,
			Deletions:    n.Deletions,
			ChangedFiles: n.ChangedFiles,
			ChecksState:  checksState,
			ReviewState:  DeriveReviewState(viewer, n.ReviewDecision, latest, reqs),
		}
		prs = append(prs, pr)
	}
	return prs, viewer
}
