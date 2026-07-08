package data

import (
	"context"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/demo"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// Backend is the single seam every data command talks to for PR data and
// posting. It replaces the per-command `if demoMode { demo.X() } else { gh.Y() }`
// branch: each command selects a Backend once (see selectBackend) and demo mode
// passes through the exact same interface. This also lets the data package's
// command flows be unit-tested against a fake Backend, and lines the posting
// operations up for reuse by the future headless CLI.
type Backend interface {
	// ListPRs returns the PR set for a list mode (review queue / authored / …).
	ListPRs(ctx context.Context, mode gh.ListMode) ([]gh.PR, error)
	// PRDetail returns the rich PR view for a ref.
	PRDetail(ctx context.Context, ref gh.Ref) (*gh.PR, error)
	// Diff returns the PR's unified diff. Callers pass the ref of the PR
	// returned by PRDetail so the demo fixture's fallback diff aligns.
	Diff(ctx context.Context, ref gh.Ref) (string, error)
	// StartReview kicks off a review run and returns its progress channel.
	StartReview(ctx context.Context, ref gh.Ref, cfg *aiconfig.Config) (<-chan review.Progress, error)
	// ExistingComments returns inline comments already on the PR plus the
	// viewer login and prior-activity summary (errors are packed into the
	// result so a partial fetch still yields a usable banner).
	ExistingComments(ctx context.Context, ref gh.Ref) ExistingComments
	// Checks returns the head commit's status-check rollup.
	Checks(ctx context.Context, ref gh.Ref) (*gh.ChecksReport, error)
	// Discussion returns the PR's conversation timeline.
	Discussion(ctx context.Context, ref gh.Ref) ([]gh.DiscussionEvent, error)
	// ViewerLogin returns the authenticated login (best effort; "" when
	// unavailable or not relevant, as in demo mode).
	ViewerLogin(ctx context.Context) string
	// HeadSHA returns the PR's current head SHA for the posting pre-flight.
	HeadSHA(ctx context.Context, ref gh.Ref) (string, error)
	// PostReview posts a full or body-only review.
	PostReview(ctx context.Context, ref gh.Ref, rev gh.Review) error
	// PostInlineComment posts one inline review comment.
	PostInlineComment(ctx context.Context, ref gh.Ref, commitID string, c gh.ReviewComment) error
	// PostFileLevelComment posts one file-level (subject_type=file) comment.
	PostFileLevelComment(ctx context.Context, ref gh.Ref, commitID, path, body string) error
	// ReplyToThread posts an in-thread reply to an existing review thread
	// (B3: reply instead of duplicating an open thread on the same anchor,
	// plus the re-run status replies on the tool's own threads).
	ReplyToThread(ctx context.Context, ref gh.Ref, threadID, body string) error
}

// ExistingComments bundles the inline comments already on a PR with the viewer
// login and prior-appr-ai-sal-activity summary. Errors are carried inline
// (ListErr / ViewerErr) rather than returned so a partial fetch still renders.
type ExistingComments struct {
	Comments []gh.PullReviewComment
	// Threads are the PR's inline review threads (with node IDs) used by B3 to
	// route a matching finding to an in-thread reply instead of a duplicate
	// top-level comment. Empty when the fetch failed or in demo mode — the
	// posting path then falls back to top-level everywhere (pre-B3 behaviour).
	Threads   []gh.ReviewThread
	Viewer    string
	Prior     gh.PriorAprrAISalActivity
	ListErr   error
	ViewerErr error
}

// selectBackend is the single demo/live selection point. Every command calls
// this once; no command branches on demoMode internally.
func selectBackend(demoMode bool) Backend {
	if demoMode {
		return demoBackend{}
	}
	return ghBackend{}
}

// ghBackend wraps the real internal/gh + internal/review calls.
type ghBackend struct{}

func (ghBackend) ListPRs(ctx context.Context, mode gh.ListMode) ([]gh.PR, error) {
	return gh.ListPRs(ctx, mode)
}

func (ghBackend) PRDetail(ctx context.Context, ref gh.Ref) (*gh.PR, error) {
	// R6.4: GetPRCached reuses a prior fetch when the PR's head SHA is
	// unchanged (cheap head-SHA revalidation), so refreshing an already-current
	// PR avoids a full `gh pr view`.
	return gh.GetPRCached(ctx, ref)
}

func (ghBackend) Diff(ctx context.Context, ref gh.Ref) (string, error) {
	return gh.GetDiff(ctx, ref)
}

func (ghBackend) StartReview(ctx context.Context, ref gh.Ref, cfg *aiconfig.Config) (<-chan review.Progress, error) {
	return review.Run(ctx, ref, cfg)
}

func (ghBackend) ExistingComments(ctx context.Context, ref gh.Ref) ExistingComments {
	comments, cerr := gh.ListPullReviewComments(ctx, ref)
	viewer, verr := gh.ViewerLogin(ctx)
	// Reviews are fetched best-effort — failure here just means the
	// "tool has reviewed this before" banner won't include the
	// review-body count, not a blocking error.
	reviews, _ := gh.ListPullReviews(ctx, ref.Owner, ref.Repo, ref.Number, 30)
	prior := gh.DetectPriorAprrAISalActivityFrom(comments, reviews, viewer)
	// Review threads (with node IDs) power B3's reply routing. Best-effort:
	// a failure here just means every finding posts top-level as before.
	threads, _ := gh.GetReviewThreads(ctx, ref)
	return ExistingComments{
		Comments:  comments,
		Threads:   threads,
		Viewer:    viewer,
		Prior:     prior,
		ListErr:   cerr,
		ViewerErr: verr,
	}
}

func (ghBackend) Checks(ctx context.Context, ref gh.Ref) (*gh.ChecksReport, error) {
	return gh.GetChecks(ctx, ref)
}

func (ghBackend) Discussion(ctx context.Context, ref gh.Ref) ([]gh.DiscussionEvent, error) {
	return gh.GetDiscussion(ctx, ref)
}

func (ghBackend) ViewerLogin(ctx context.Context) string {
	viewer, _ := gh.ViewerLogin(ctx)
	return viewer
}

func (ghBackend) HeadSHA(ctx context.Context, ref gh.Ref) (string, error) {
	return gh.GetPRHeadSHA(ctx, ref)
}

func (ghBackend) PostReview(ctx context.Context, ref gh.Ref, rev gh.Review) error {
	return gh.PostReview(ctx, ref, rev)
}

func (ghBackend) PostInlineComment(ctx context.Context, ref gh.Ref, commitID string, c gh.ReviewComment) error {
	return gh.CreatePullReviewComment(ctx, ref, commitID, c)
}

func (ghBackend) PostFileLevelComment(ctx context.Context, ref gh.Ref, commitID, path, body string) error {
	return gh.CreatePullReviewFileLevelComment(ctx, ref, commitID, path, body)
}

func (ghBackend) ReplyToThread(ctx context.Context, ref gh.Ref, threadID, body string) error {
	return gh.ReplyToReviewThread(ctx, ref, threadID, body)
}

// demoBackend wraps the internal/demo fixtures. It passes through the same
// Backend interface as ghBackend so no command needs a demo branch. The demo
// binary forces dry-run at the CLI seam, so the Post* methods here are never
// reached during a recording; they succeed as no-ops for completeness.
type demoBackend struct{}

func (demoBackend) ListPRs(ctx context.Context, mode gh.ListMode) ([]gh.PR, error) {
	return demoPRsForMode(mode), nil
}

func (demoBackend) PRDetail(ctx context.Context, ref gh.Ref) (*gh.PR, error) {
	pr := demo.LookupPR(ref)
	if pr == nil {
		// URL-paste of a ref that doesn't match a fixture: fall back to the
		// first canned PR so the demo still has something to render. The
		// caller derives the diff ref from this PR, so the fallback diff
		// lines up with the fallback PR.
		fallback := demo.DemoPullRequests()[0]
		pr = &fallback
	}
	return pr, nil
}

func (demoBackend) Diff(ctx context.Context, ref gh.Ref) (string, error) {
	return demo.DemoDiff(ref), nil
}

func (demoBackend) StartReview(ctx context.Context, ref gh.Ref, cfg *aiconfig.Config) (<-chan review.Progress, error) {
	return demo.SyntheticReviewProgress(ctx, ref, cfg), nil
}

func (demoBackend) ExistingComments(ctx context.Context, ref gh.Ref) ExistingComments {
	comments, viewer, prior := demo.DemoExistingComments(ref)
	return ExistingComments{
		Comments: comments,
		Viewer:   viewer,
		Prior:    prior,
	}
}

func (demoBackend) Checks(ctx context.Context, ref gh.Ref) (*gh.ChecksReport, error) {
	return demo.DemoChecks(ref), nil
}

func (demoBackend) Discussion(ctx context.Context, ref gh.Ref) ([]gh.DiscussionEvent, error) {
	return demo.DemoDiscussion(ref), nil
}

// ViewerLogin returns "" in demo mode: the demo binary may run without gh on
// PATH, and the canned PRs aren't authored by the demo viewer, so the
// self-author verdict downgrade never applies.
func (demoBackend) ViewerLogin(ctx context.Context) string { return "" }

func (demoBackend) HeadSHA(ctx context.Context, ref gh.Ref) (string, error) { return "", nil }

func (demoBackend) PostReview(ctx context.Context, ref gh.Ref, rev gh.Review) error { return nil }

func (demoBackend) PostInlineComment(ctx context.Context, ref gh.Ref, commitID string, c gh.ReviewComment) error {
	return nil
}

func (demoBackend) PostFileLevelComment(ctx context.Context, ref gh.Ref, commitID, path, body string) error {
	return nil
}

func (demoBackend) ReplyToThread(ctx context.Context, ref gh.Ref, threadID, body string) error {
	return nil
}

var (
	_ Backend = ghBackend{}
	_ Backend = demoBackend{}
)
