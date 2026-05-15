package demo

import "github.com/madicen/appr-ai-sal/internal/gh"

// DemoExistingComments returns empty inline comments + a clean prior-
// activity record so the demo always opens onto a "fresh" PR. We
// deliberately avoid seeding any prior appr-ai-sal markers — the
// "tool has reviewed this before" banner shows up in unstable framing
// (timestamps relative to "now") that doesn't reproduce well across
// recordings.
func DemoExistingComments(ref gh.Ref) (comments []gh.PullReviewComment, viewer string, prior gh.PriorAprrAISalActivity) {
	return nil, "demo-user", gh.PriorAprrAISalActivity{}
}
