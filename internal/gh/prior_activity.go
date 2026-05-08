package gh

import (
	"context"
	"strings"
	"time"
)

// AprrAISalInlineMarker is the leading text every inline review comment
// posted by appr-ai-sal carries (see internal/review/types.go aiCommentLead).
// We treat any inline comment whose body contains this substring as one
// previously posted by the tool.
const AprrAISalInlineMarker = "tool: **appr-ai-sal**"

// AprrAISalReviewBodyMarker is the substring that the tool's review-body
// disclosure always contains (see internal/review/types.go AI disclosure
// boilerplate). We use a substring check rather than an exact prefix so
// changes in the surrounding wording don't silently break detection.
const AprrAISalReviewBodyMarker = "produced by **appr-ai-sal**"

// PriorAprrAISalActivity summarises whether the appr-ai-sal tool has
// already reviewed this PR. The TUI uses this to render an
// acknowledgement banner like "Note: appr-ai-sal has reviewed this PR
// before (last review: 2h ago, 3 inline comment(s), 1 review body)" so
// the human knows the new run is a refresh, not the first pass.
//
// Counts only entries authored by viewer (the local gh user). When viewer
// is empty we conservatively count every entry whose body matches the
// markers, since "someone using this tool" is still useful signal even if
// we can't pin it to the current user.
type PriorAprrAISalActivity struct {
	// InlineCount is the number of inline review comments we recognise as
	// having been posted by appr-ai-sal.
	InlineCount int
	// ReviewCount is the number of submitted reviews (top-level bodies)
	// whose disclosure marker matches.
	ReviewCount int
	// LastAt is the most recent timestamp across the matching entries.
	// Zero when nothing matched.
	LastAt time.Time
	// LastSummarySnippet is a short prefix of the most recent matching
	// review body, useful for showing context in the banner. Empty when
	// no review-body match was found.
	LastSummarySnippet string
}

// Found reports whether any previous appr-ai-sal activity was detected.
func (p PriorAprrAISalActivity) Found() bool {
	return p.InlineCount > 0 || p.ReviewCount > 0
}

// DetectPriorAprrAISalActivityFrom analyses already-fetched comments and
// reviews and returns the PriorAprrAISalActivity summary. Used by the TUI
// to avoid duplicate API round-trips when ListPullReviewComments was
// already called for duplicate detection.
//
// viewer scopes the match to the local gh user when non-empty; an empty
// viewer matches any author so the caller can still surface "the tool ran
// here before" even when we can't resolve the gh login.
func DetectPriorAprrAISalActivityFrom(comments []PullReviewComment, reviews []PullReviewRow, viewer string) PriorAprrAISalActivity {
	out := PriorAprrAISalActivity{}
	for _, c := range comments {
		if !matchesViewer(viewer, c.AuthorLogin) {
			continue
		}
		if !strings.Contains(c.Body, AprrAISalInlineMarker) {
			continue
		}
		out.InlineCount++
		if c.CreatedAt.After(out.LastAt) {
			out.LastAt = c.CreatedAt
		}
	}
	for _, r := range reviews {
		if !matchesViewer(viewer, r.Author) {
			continue
		}
		if !strings.Contains(r.Body, AprrAISalReviewBodyMarker) {
			continue
		}
		out.ReviewCount++
		if r.SubmittedAt.After(out.LastAt) {
			out.LastAt = r.SubmittedAt
			out.LastSummarySnippet = snippetForBanner(r.Body)
		}
	}
	return out
}

// DetectPriorAprrAISalActivity counts inline + top-level reviews on the PR
// whose body matches the appr-ai-sal markers. The inline list and the
// review list are fetched independently, and either failing returns the
// partial PriorAprrAISalActivity from the other plus the error. Callers
// can surface the partial signal even when one side failed.
func DetectPriorAprrAISalActivity(ctx context.Context, ref Ref, viewer string) (PriorAprrAISalActivity, error) {
	comments, ierr := ListPullReviewComments(ctx, ref)
	reviews, rerr := ListPullReviews(ctx, ref.Owner, ref.Repo, ref.Number, 30)
	out := DetectPriorAprrAISalActivityFrom(comments, reviews, viewer)
	switch {
	case ierr != nil:
		return out, ierr
	case rerr != nil:
		return out, rerr
	}
	return out, nil
}

// matchesViewer returns true when author equals viewer (case-insensitive)
// or when viewer is empty (caller couldn't resolve the local user; we
// fall back to "any author whose body matches the marker").
func matchesViewer(viewer, author string) bool {
	v := strings.TrimSpace(viewer)
	if v == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(author), v)
}

// snippetForBanner returns the first line (or up to 160 chars) of the
// review body, with the disclosure quote-line stripped. Used to give the
// banner a hint of what the previous review actually said.
func snippetForBanner(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, ">") {
			continue
		}
		if strings.Contains(l, AprrAISalReviewBodyMarker) {
			continue
		}
		if len(l) > 160 {
			l = l[:160] + "…"
		}
		return l
	}
	return ""
}
