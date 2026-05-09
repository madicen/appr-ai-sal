package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// PullReviewComment is one inline review comment on a pull request (REST:
// /repos/{owner}/{repo}/pulls/{pull_number}/comments).
type PullReviewComment struct {
	Body        string
	Path        string
	Line        int
	Side        string
	AuthorLogin string
	CreatedAt   time.Time
}

// ListPullReviewComments returns all inline review comments on the PR (paginated).
func ListPullReviewComments(ctx context.Context, ref Ref) ([]PullReviewComment, error) {
	const perPage = 100
	var all []PullReviewComment
	for page := 1; page <= 25; page++ {
		path := fmt.Sprintf("repos/%s/%s/pulls/%d/comments?per_page=%d&page=%d",
			ref.Owner, ref.Repo, ref.Number, perPage, page)
		out, err := runJSON(ctx, []string{"api", path})
		if err != nil {
			return nil, err
		}
		var raw []struct {
			Body string `json:"body"`
			Path string `json:"path"`
			Line *int   `json:"line"`
			Side string `json:"side"`
			User *struct {
				Login string `json:"login"`
			} `json:"user"`
			CreatedAt string `json:"created_at"`
		}
		if err := json.Unmarshal(out, &raw); err != nil {
			return nil, fmt.Errorf("parse pull comments for #%d: %w", ref.Number, err)
		}
		if len(raw) == 0 {
			break
		}
		for _, r := range raw {
			line := 0
			if r.Line != nil {
				line = *r.Line
			}
			author := ""
			if r.User != nil {
				author = r.User.Login
			}
			ts, _ := time.Parse(time.RFC3339, r.CreatedAt)
			all = append(all, PullReviewComment{
				Body:        r.Body,
				Path:        r.Path,
				Line:        line,
				Side:        r.Side,
				AuthorLogin: author,
				CreatedAt:   ts,
			})
		}
		if len(raw) < perPage {
			break
		}
	}
	return all, nil
}

func normalizeSide(side string) string {
	s := strings.ToUpper(strings.TrimSpace(side))
	if s == "" {
		return "RIGHT"
	}
	return s
}

func normalizeCommentBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(s)
}

func normalizePathKey(p string) string {
	return filepath.ToSlash(strings.TrimSpace(p))
}

// ViewerHasMatchingComment reports whether the viewer already posted an inline
// comment at the same path, line, side with the same body text.
func ViewerHasMatchingComment(viewer string, path string, line int, side, expectedBody string, existing []PullReviewComment) bool {
	if strings.TrimSpace(viewer) == "" || line <= 0 || strings.TrimSpace(path) == "" {
		return false
	}
	wantSide := normalizeSide(side)
	wantPath := normalizePathKey(path)
	wantBody := normalizeCommentBody(expectedBody)
	for _, c := range existing {
		if !strings.EqualFold(strings.TrimSpace(c.AuthorLogin), viewer) {
			continue
		}
		if normalizePathKey(c.Path) != wantPath {
			continue
		}
		if c.Line != line {
			continue
		}
		if normalizeSide(c.Side) != wantSide {
			continue
		}
		if normalizeCommentBody(c.Body) == wantBody {
			return true
		}
	}
	return false
}
