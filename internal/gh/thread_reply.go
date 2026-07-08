package gh

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// graphqlAddThreadReplyMutation posts a reply into an existing inline review
// thread. It is the GraphQL counterpart to the REST
// POST /pulls/{n}/comments/{comment_id}/replies endpoint, but keyed on the
// thread's node id rather than a comment database id — which is exactly what
// GetReviewThreads/GetPRAgentData already surface on ReviewThread.ID, so B3's
// thread-aware posting reuses that id with no extra fetch.
const graphqlAddThreadReplyMutation = `mutation($threadId: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $threadId, body: $body}) {
    comment { id url }
  }
}`

// addThreadReplyData mirrors the mutation's returned payload. We only read the
// created comment's id/url for logging; the caller treats a nil error as
// success.
type addThreadReplyData struct {
	AddPullRequestReviewThreadReply struct {
		Comment struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"comment"`
	} `json:"addPullRequestReviewThreadReply"`
}

// ReplyToReviewThread posts body as an in-thread reply to the existing
// unresolved review thread identified by threadID (a PullRequestReviewThread
// node id, as carried on ReviewThread.ID). This is B3's alternative to filing
// a duplicate top-level inline comment when a new finding lands on the same
// anchor as an open thread, and the vehicle for the re-run status replies the
// tool leaves on its own prior threads.
//
// Errors are returned verbatim (wrapped with context) rather than swallowed:
// the posting orchestration treats a failed reply exactly like a failed
// top-level post so the reviewer sees the same actionable error, and the run
// never crashes. threadID and body are validated up front so an obviously
// malformed call fails locally instead of round-tripping.
func ReplyToReviewThread(ctx context.Context, ref Ref, threadID, body string) error {
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("empty review thread id")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("empty reply body")
	}
	c, err := newGraphQLClient()
	if err != nil {
		return fmt.Errorf("gh graphql client: %w", err)
	}
	var out addThreadReplyData
	start := time.Now()
	derr := c.DoWithContext(ctx, graphqlAddThreadReplyMutation, map[string]any{
		"threadId": threadID,
		"body":     body,
	}, &out)
	applog.GHInvocation([]string{"api", "graphql", "addPullRequestReviewThreadReply"}, time.Since(start), derr)
	applog.Info("reply to review thread", "ref", ref.String(), "thread", threadID, "ok", derr == nil)
	if derr != nil {
		return fmt.Errorf("reply to review thread %s: %w", threadID, derr)
	}
	return nil
}
