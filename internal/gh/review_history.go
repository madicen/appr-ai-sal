package gh

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PullReviewRow is one submitted PR review (top-level body + state).
type PullReviewRow struct {
	PRNumber    int
	PRTitle     string
	Author      string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	Body        string
	SubmittedAt time.Time
}

// pullReviewAPIRow is one entry from the REST pulls/{n}/reviews list.
type pullReviewAPIRow struct {
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	State       string `json:"state"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
}

// ListPullReviews returns up to limit submitted reviews for a pull request (in
// API order). PENDING/DISMISSED reviews are dropped and never count against
// limit.
//
// R6.3: this used to fetch a single small page (min(limit*3, 30)) and cap the
// result, which under-filled — a PR whose first page was dominated by
// PENDING/DISMISSED entries could return far fewer than limit valid reviews
// even though more existed on later pages. We now page through the endpoint
// (per_page=100) until we've collected limit valid rows or run out of pages,
// so the returned set is correctly filled.
func ListPullReviews(ctx context.Context, owner, repo string, prNumber int, limit int) ([]PullReviewRow, error) {
	if limit < 1 {
		limit = 15
	}
	const perPage = 100
	var rows []PullReviewRow
	for page := 1; page <= 20; page++ {
		path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews?per_page=%d&page=%d", owner, repo, prNumber, perPage, page)
		var raw []pullReviewAPIRow
		if err := ghAPIGet(ctx, path, &raw); err != nil {
			return nil, fmt.Errorf("list reviews for #%d: %w", prNumber, err)
		}
		if len(raw) == 0 {
			break
		}
		for _, r := range raw {
			if r.State == "PENDING" || r.State == "DISMISSED" {
				continue
			}
			author := ""
			if r.User != nil {
				author = r.User.Login
			}
			ts, _ := time.Parse(time.RFC3339, r.SubmittedAt)
			rows = append(rows, PullReviewRow{
				PRNumber:    prNumber,
				Author:      author,
				State:       r.State,
				Body:        strings.TrimSpace(r.Body),
				SubmittedAt: ts,
			})
			if len(rows) >= limit {
				return rows, nil
			}
		}
		if len(raw) < perPage {
			break
		}
	}
	return rows, nil
}

// BuildReviewHistoryDigest fetches reviews for up to prLimit merged PRs and formats markdown capped at maxBytes.
func BuildReviewHistoryDigest(ctx context.Context, owner, repo string, prLimit int, maxBytes int) (string, error) {
	if prLimit < 1 {
		prLimit = 8
	}
	if maxBytes < 512 {
		maxBytes = 12000
	}
	merged, err := ListMergedPRs(ctx, owner, repo, prLimit)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	perPRReviews := 5
	for _, m := range merged {
		if b.Len() >= maxBytes-256 {
			break
		}
		reviews, err := ListPullReviews(ctx, owner, repo, m.Number, perPRReviews)
		if err != nil {
			continue
		}
		b.WriteString(fmt.Sprintf("### Merged PR #%d %s\n\n", m.Number, m.Title))
		if len(reviews) == 0 {
			b.WriteString("_(no submitted review bodies retrieved)_\n\n")
			continue
		}
		for _, rv := range reviews {
			if rv.Body == "" {
				continue
			}
			snippet := rv.Body
			if len(snippet) > 800 {
				snippet = snippet[:800] + "…"
			}
			b.WriteString(fmt.Sprintf("- **@%s** (%s): %s\n", rv.Author, rv.State, strings.ReplaceAll(snippet, "\n", " ")))
		}
		b.WriteString("\n")
		if b.Len() >= maxBytes {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > maxBytes {
		out = out[:maxBytes] + "\n…(truncated)\n"
	}
	return out, nil
}

// ReviewHistoryCachePath returns a stable cache path for the digest (not PR-specific).
func ReviewHistoryCachePath(owner, repo string, prLimit, maxBytes int) string {
	slug := strings.ReplaceAll(strings.ToLower(owner+"_"+repo), "/", "_")
	return fmt.Sprintf("review-history_%s_prs%d_max%d.md", slug, prLimit, maxBytes)
}
