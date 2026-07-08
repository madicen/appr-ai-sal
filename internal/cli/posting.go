package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// postResult captures what --post / --dry-run did (or would do), for the
// stdout JSON and the human summary.
type postResult struct {
	DryRun   bool     `json:"dry_run"`
	Event    string   `json:"event"`
	Posted   int      `json:"posted_comments"`    // real top-level inline comments posted
	Replies  int      `json:"posted_replies"`     // real in-thread replies posted
	BodyPost bool     `json:"posted_body"`        // whether the verdict/body review was posted
	Failed   int      `json:"failed"`             // fail-open comment/reply failures
	Previews []string `json:"previews,omitempty"` // dry-run payload previews
}

// handlePosting performs (or previews) the GitHub post when --post / --dry-run
// is set. It reuses the F7 payload builders (EffectiveReviewEventAndBody,
// InlineReviewComment, the DryRun* previews) and B3 thread-aware routing
// (UnresolvedThreadRefs + RouteFinding) so a finding on an existing unresolved
// thread's anchor becomes an in-thread reply instead of a duplicate top-level
// comment. Returns nil postResult when neither flag is set.
func handlePosting(ctx context.Context, fl *reviewFlags, draft *review.Draft, stderr io.Writer, deps reviewDeps) (*postResult, int) {
	if !fl.post && !fl.dryRun {
		return nil, ExitOK
	}
	if draft == nil || draft.PR == nil {
		fmt.Fprintf(stderr, "appr-ai-sal: cannot post: review produced no PR context\n")
		return nil, ExitError
	}

	viewer := deps.poster.ViewerLogin(ctx)
	event, body, intended := review.EffectiveReviewEventAndBody(draft, "", viewer)

	// Thread refs power B3 reply routing. Best-effort: a failure just means
	// every finding posts top-level, exactly as before B3.
	ref := draft.Ref
	threads, _ := deps.poster.ReviewThreads(ctx, ref)
	refs := review.UnresolvedThreadRefs(threads, viewer)

	res := &postResult{DryRun: fl.dryRun, Event: event}

	if fl.dryRun {
		buildDryRunPreviews(res, draft, event, intended, body, refs)
		return res, ExitOK
	}

	// Real post. Pre-flight the head-SHA drift check first (F7): refuse to
	// post stale inline comments GitHub would reject with an opaque error.
	if cur, err := deps.poster.HeadSHA(ctx, ref); err == nil {
		if drift := gh.HeadDrift(draft.PR.HeadSHA, cur); drift != nil {
			fmt.Fprintf(stderr, "appr-ai-sal: %v\n", drift)
			return nil, ExitError
		}
	}

	for _, ff := range draft.FlatPostableFindingsForPost() {
		route := review.RouteFinding(ff.Finding, refs)
		if route.Kind == review.RouteReply {
			body := review.ReviewCommentBody(ff.Specialist, ff.Finding)
			if err := deps.poster.ReplyToThread(ctx, ref, route.ThreadID, body); err != nil {
				fmt.Fprintf(stderr, "appr-ai-sal: reply failed for %s %s:%d: %v\n", ff.Specialist, ff.Finding.Path, ff.Finding.Line, err)
				res.Failed++
				continue
			}
			res.Replies++
			continue
		}
		c := review.InlineReviewComment(ff.Specialist, ff.Finding)
		if err := deps.poster.PostInlineComment(ctx, ref, draft.PR.HeadSHA, c); err != nil {
			fmt.Fprintf(stderr, "appr-ai-sal: inline post failed for %s %s:%d: %v\n", ff.Specialist, ff.Finding.Path, ff.Finding.Line, err)
			res.Failed++
			continue
		}
		res.Posted++
	}

	// Finally submit the body-only review carrying the verdict event.
	rev := gh.Review{CommitID: draft.PR.HeadSHA, Body: body, Event: event}
	if err := deps.poster.PostReview(ctx, ref, rev); err != nil {
		fmt.Fprintf(stderr, "appr-ai-sal: post review failed: %v\n", err)
		return nil, ExitError
	}
	res.BodyPost = true
	return res, ExitOK
}

// buildDryRunPreviews fills res.Previews with the payloads a real --post would
// send, using the same routing decision so a reply-bound finding previews as a
// reply. No network writes happen.
func buildDryRunPreviews(res *postResult, draft *review.Draft, event, intended, body string, refs []review.ThreadRef) {
	ref := draft.Ref
	verdict := review.DryRunVerdictReview(ref, draft.PR.HeadSHA, event, intended, body)
	res.Previews = append(res.Previews, verdict.Title+"\n"+verdict.Payload)
	for _, ff := range draft.FlatPostableFindingsForPost() {
		route := review.RouteFinding(ff.Finding, refs)
		var p review.DryRunPayload
		if route.Kind == review.RouteReply {
			p = review.DryRunThreadReply(ref, route.ThreadID, ff.Specialist, ff.Finding)
		} else {
			p = review.DryRunSingleFinding(ref, draft.PR, ff.Specialist, ff.Finding)
		}
		res.Previews = append(res.Previews, p.Title+"\n"+p.Payload)
	}
}
