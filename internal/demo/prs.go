// Package demo provides canned data and synthetic event generators that
// drive the TUI when --demo is passed on the CLI. The package exists so
// VHS can record reproducible README GIFs without touching the user's
// gh credentials, real cache, or any AI provider.
//
// All exported functions are safe to call without prior setup; they
// allocate and return fresh values on each call so a recording can be
// re-run idempotently.
package demo

import (
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// DemoPullRequests returns the canned PR list rendered in the demo's
// "PRs awaiting your review" tab. Spans two repos so demo navigation
// covers the (owner, repo, number) tuple the rest of the app keys on.
func DemoPullRequests() []gh.PR {
	now := time.Date(2026, time.May, 14, 15, 30, 0, 0, time.UTC)
	prs := []gh.PR{
		{
			Number:     742,
			Title:      "Stream review progress events through the persistent overlay",
			URL:        "https://github.com/madicen/appr-ai-sal/pull/742",
			Repository: "madicen/appr-ai-sal",
			Owner:      "madicen",
			Repo:       "appr-ai-sal",
			Author:     "madicen",
			Body:       demoPRBodyAppr,
			BaseRef:    "main",
			HeadRef:    "feat/streaming-overlay",
			HeadSHA:    "a1b2c3d4e5f607182930",
			CreatedAt:  now.Add(-26 * time.Hour),
			UpdatedAt:  now.Add(-2 * time.Hour),
			ReviewState: gh.ReviewState{
				Decision:             "REVIEW_REQUIRED",
				ViewerStillRequested: true,
			},
		},
		{
			Number:     318,
			Title:      "Replace flat file list with hierarchical tree + line-number gutter",
			URL:        "https://github.com/madicen/appr-ai-sal/pull/318",
			Repository: "madicen/appr-ai-sal",
			Owner:      "madicen",
			Repo:       "appr-ai-sal",
			Author:     "madicen",
			Body:       demoPRBodyTree,
			BaseRef:    "main",
			HeadRef:    "feat/diff-tree-view",
			HeadSHA:    "f2e1d0c9b8a796857463",
			CreatedAt:  now.Add(-72 * time.Hour),
			UpdatedAt:  now.Add(-30 * time.Minute),
			ReviewState: gh.ReviewState{
				Decision:             "REVIEW_REQUIRED",
				ViewerStillRequested: true,
			},
		},
		{
			Number:     109,
			Title:      "Add convention-witness pass between specialists and the repo arbiter",
			URL:        "https://github.com/madicen/appr-ai-sal/pull/109",
			Repository: "madicen/appr-ai-sal",
			Owner:      "madicen",
			Repo:       "appr-ai-sal",
			Author:     "alex-r",
			Body:       "Adds the witness pipeline described in the design doc; gates testing/docs findings on whether the repo's static evidence agrees.",
			BaseRef:    "main",
			HeadRef:    "convention-witness",
			HeadSHA:    "11223344556677889900",
			CreatedAt:  now.Add(-7 * 24 * time.Hour),
			UpdatedAt:  now.Add(-4 * time.Hour),
			ReviewState: gh.ReviewState{
				Decision:             "CHANGES_REQUESTED",
				Approvals:            1,
				ChangesRequested:     1,
				ViewerHasReviewed:    true,
				ViewerStillRequested: false,
			},
		},
		{
			Number:     56,
			Title:      "[plumbing-svc] retry 429 with jittered backoff in publish path",
			URL:        "https://github.com/madicen/plumbing-svc/pull/56",
			Repository: "madicen/plumbing-svc",
			Owner:      "madicen",
			Repo:       "plumbing-svc",
			Author:     "lin-q",
			Body:       "Adds capped exponential backoff with jitter to the publish loop so a thundering 429 storm doesn't cascade through the rest of the queue.",
			BaseRef:    "main",
			HeadRef:    "publish-backoff",
			HeadSHA:    "deadbeefcafe11223344",
			CreatedAt:  now.Add(-32 * time.Hour),
			UpdatedAt:  now.Add(-1 * time.Hour),
			ReviewState: gh.ReviewState{
				Decision:             "REVIEW_REQUIRED",
				ViewerStillRequested: true,
			},
		},
		{
			Number:     22,
			Title:      "[plumbing-svc] doc: clarify retention semantics for archived topics",
			URL:        "https://github.com/madicen/plumbing-svc/pull/22",
			Repository: "madicen/plumbing-svc",
			Owner:      "madicen",
			Repo:       "plumbing-svc",
			Author:     "kim-d",
			Body:       "Tightens the retention wording so the API contract is unambiguous about what \"archived\" means for downstream consumers.",
			BaseRef:    "main",
			HeadRef:    "docs/retention",
			HeadSHA:    "9988776655443322110a",
			CreatedAt:  now.Add(-5 * 24 * time.Hour),
			UpdatedAt:  now.Add(-3 * time.Hour),
			ReviewState: gh.ReviewState{
				Decision:  "APPROVED",
				Approvals: 2,
			},
		},
	}
	return prs
}

// LookupPR returns the canned PR matching ref or nil if none does.
// Used by LoadPRDetailCmd's demo branch so a URL paste / list click
// resolves to the same fixture the list tab seeded.
func LookupPR(ref gh.Ref) *gh.PR {
	for _, pr := range DemoPullRequests() {
		if strings.EqualFold(pr.Owner, ref.Owner) &&
			strings.EqualFold(pr.Repo, ref.Repo) &&
			pr.Number == ref.Number {
			out := pr
			return &out
		}
	}
	return nil
}

// demoPRBody* are the rendered descriptions shown in the PR description
// overlay (g key). Kept in this file so tweaking the demo's storytelling
// doesn't pull a reader into mock-data plumbing.
const demoPRBodyAppr = `## Summary

Replaces the polling loop that drives the review-overlay refresh with the
runner's existing progress channel, so the user sees stage transitions in
real time instead of in 200ms batched ticks.

## Why

Long stages (security specialist on a multi-thousand-line diff, repo
arbiter on a fanout) felt unresponsive — there was no visual signal until
the next poll fired. Streaming events also makes it trivial to add per-
stage retry copy without coupling the overlay's refresh interval to the
runner's pacing.

## Notes

- No payload changes; the overlay reads the same Progress shape.
- The runner contract is unchanged for non-TUI consumers.
- See the convention-witness rollout doc for the bigger pipeline picture.
`

const demoPRBodyTree = `## Summary

Replaces the flat ` + "`Files`" + ` pane with a collapsible tree (jj-tui-style)
and adds an old/new line-number gutter to the diff viewport.

## Why

The flat list got long and noisy on multi-package PRs (you couldn't see
which directories the changes clustered into). The line-number gutter
makes the inline finding tags self-anchoring so reviewers don't need to
flip back to GitHub to confirm where a comment will land.

## Out of scope

- Word-level diff highlighting (filed as a follow-up).
- Image / binary diff rendering.
`
