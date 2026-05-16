package demo

import (
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// DemoDiscussion returns the canned conversation timeline for ref. Most demo
// PRs have a small back-and-forth between the author and a reviewer; PRs we
// haven't scripted return an empty timeline so the renderer falls through to
// "no discussion yet."
func DemoDiscussion(ref gh.Ref) []gh.DiscussionEvent {
	pr := LookupPR(ref)
	if pr == nil {
		return nil
	}
	now := time.Date(2026, time.May, 14, 15, 30, 0, 0, time.UTC)
	switch {
	case strings.EqualFold(pr.Owner, "madicen") && strings.EqualFold(pr.Repo, "appr-ai-sal") && pr.Number == 318:
		return []gh.DiscussionEvent{
			{
				Kind:   gh.DiscussionComment,
				Author: "alex-r",
				When:   now.Add(-22 * time.Hour),
				Body:   "Tree feels great in my terminal. One thing — long folder names wrap onto a phantom blank line. Repro: any node_modules-style depth.",
				URL:    "https://github.com/madicen/appr-ai-sal/pull/318#issuecomment-1",
			},
			{
				Kind:    gh.DiscussionReview,
				Author:  "alex-r",
				When:    now.Add(-21 * time.Hour),
				Verdict: "CHANGES_REQUESTED",
				Body:    "Blocking on the wrap bug above; the gutter looks lovely though.",
				URL:     "https://github.com/madicen/appr-ai-sal/pull/318#pullrequestreview-1",
			},
			{
				Kind:   gh.DiscussionComment,
				Author: "madicen",
				When:   now.Add(-2 * time.Hour),
				Body:   "Pushed a fix — folder rows now pre-truncate to fit the pane width. Mind taking another look?",
				URL:    "https://github.com/madicen/appr-ai-sal/pull/318#issuecomment-2",
			},
		}
	case strings.EqualFold(pr.Owner, "madicen") && strings.EqualFold(pr.Repo, "appr-ai-sal") && pr.Number == 742:
		return []gh.DiscussionEvent{
			{
				Kind:   gh.DiscussionComment,
				Author: "kim-d",
				When:   now.Add(-25 * time.Hour),
				Body:   "Streaming events should make this feel night-and-day on long runs. Mind adding a quick screenshot of the chip transitions for the README?",
				URL:    "https://github.com/madicen/appr-ai-sal/pull/742#issuecomment-1",
			},
			{
				Kind:   gh.DiscussionComment,
				Author: "madicen",
				When:   now.Add(-3 * time.Hour),
				Body:   "Recorded a vhs gif and dropped it in `screenshots/review-run.gif`. The chip transitions are visible at ~12s.",
				URL:    "https://github.com/madicen/appr-ai-sal/pull/742#issuecomment-2",
			},
		}
	case strings.EqualFold(pr.Owner, "madicen") && strings.EqualFold(pr.Repo, "appr-ai-sal") && pr.Number == 109:
		return []gh.DiscussionEvent{
			{
				Kind:    gh.DiscussionReview,
				Author:  "madicen",
				When:    now.Add(-6 * 24 * time.Hour),
				Verdict: "COMMENTED",
				Body:    "Witness pass reads well. One nit on the demote ladder ordering — see inline.",
				URL:     "https://github.com/madicen/appr-ai-sal/pull/109#pullrequestreview-1",
			},
			{
				Kind:    gh.DiscussionReview,
				Author:  "lin-q",
				When:    now.Add(-5 * 24 * time.Hour),
				Verdict: "CHANGES_REQUESTED",
				Body:    "I'd like to see a test that fails when the witness ladder regresses. Otherwise looks great.",
				URL:     "https://github.com/madicen/appr-ai-sal/pull/109#pullrequestreview-2",
			},
		}
	default:
		return nil
	}
}
