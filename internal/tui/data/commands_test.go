package data

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// fakeBackend is a test double implementing Backend. It records the arguments
// each command hands it and returns canned results, so the data command flows
// can be exercised without a live gh CLI.
type fakeBackend struct {
	prs       []gh.PR
	prsErr    error
	pr        *gh.PR
	prErr     error
	diff      string
	diffErr   error
	existing  ExistingComments
	checks    *gh.ChecksReport
	checksErr error
	disc      []gh.DiscussionEvent
	discErr   error
	viewer    string
	headSHA   string
	headErr   error

	startCh  chan review.Progress
	startErr error
	startCtx context.Context // ctx the run was started with (Phase 5 item 3)

	// recorded calls
	listMode      gh.ListMode
	diffRef       gh.Ref
	postedReview  *gh.Review
	postReviewErr error
	postedInline  *gh.ReviewComment
	postInlineErr error
	postedFile    *fileLevelPost
	postFileErr   error
	replies       []threadReply
	replyErr      error
}

type threadReply struct {
	threadID string
	body     string
}

type fileLevelPost struct {
	commitID string
	path     string
	body     string
}

func (f *fakeBackend) ListPRs(_ context.Context, mode gh.ListMode) ([]gh.PR, error) {
	f.listMode = mode
	return f.prs, f.prsErr
}
func (f *fakeBackend) PRDetail(_ context.Context, _ gh.Ref) (*gh.PR, error) { return f.pr, f.prErr }
func (f *fakeBackend) Diff(_ context.Context, ref gh.Ref) (string, error) {
	f.diffRef = ref
	return f.diff, f.diffErr
}
func (f *fakeBackend) StartReview(ctx context.Context, _ gh.Ref, _ *aiconfig.Config) (<-chan review.Progress, error) {
	f.startCtx = ctx
	return f.startCh, f.startErr
}
func (f *fakeBackend) ExistingComments(_ context.Context, _ gh.Ref) ExistingComments {
	return f.existing
}
func (f *fakeBackend) Checks(_ context.Context, _ gh.Ref) (*gh.ChecksReport, error) {
	return f.checks, f.checksErr
}
func (f *fakeBackend) Discussion(_ context.Context, _ gh.Ref) ([]gh.DiscussionEvent, error) {
	return f.disc, f.discErr
}
func (f *fakeBackend) ViewerLogin(_ context.Context) string { return f.viewer }
func (f *fakeBackend) HeadSHA(_ context.Context, _ gh.Ref) (string, error) {
	return f.headSHA, f.headErr
}
func (f *fakeBackend) PostReview(_ context.Context, _ gh.Ref, rev gh.Review) error {
	r := rev
	f.postedReview = &r
	return f.postReviewErr
}
func (f *fakeBackend) PostInlineComment(_ context.Context, _ gh.Ref, commitID string, c gh.ReviewComment) error {
	cc := c
	f.postedInline = &cc
	return f.postInlineErr
}
func (f *fakeBackend) PostFileLevelComment(_ context.Context, _ gh.Ref, commitID, path, body string) error {
	f.postedFile = &fileLevelPost{commitID: commitID, path: path, body: body}
	return f.postFileErr
}
func (f *fakeBackend) ReplyToThread(_ context.Context, _ gh.Ref, threadID, body string) error {
	if f.replyErr != nil {
		return f.replyErr
	}
	f.replies = append(f.replies, threadReply{threadID: threadID, body: body})
	return nil
}

var _ Backend = (*fakeBackend)(nil)

func ref() gh.Ref { return gh.Ref{Owner: "o", Repo: "r", Number: 7} }

func TestReplyToThreadMsgCallsBackend(t *testing.T) {
	b := &fakeBackend{}
	msg := replyToThreadMsg(b, ref(), "THREAD_1", "thanks, fixed")
	got, ok := msg.(ThreadReplyPostedMsg)
	if !ok {
		t.Fatalf("got %T want ThreadReplyPostedMsg", msg)
	}
	if got.Err != nil {
		t.Fatalf("unexpected err: %v", got.Err)
	}
	if got.ThreadID != "THREAD_1" {
		t.Errorf("thread id = %q, want THREAD_1", got.ThreadID)
	}
	if len(b.replies) != 1 || b.replies[0].threadID != "THREAD_1" || b.replies[0].body != "thanks, fixed" {
		t.Errorf("backend reply not recorded correctly: %+v", b.replies)
	}
}

func TestReplyToThreadMsgSurfacesError(t *testing.T) {
	b := &fakeBackend{replyErr: errors.New("boom")}
	msg := replyToThreadMsg(b, ref(), "T", "x")
	got, ok := msg.(ThreadReplyPostedMsg)
	if !ok {
		t.Fatalf("got %T want ThreadReplyPostedMsg", msg)
	}
	if got.Err == nil {
		t.Fatal("expected an error to be surfaced")
	}
}

func TestFetchThreadsMsgReturnsCommentsAndThreads(t *testing.T) {
	b := &fakeBackend{existing: ExistingComments{
		Comments: []gh.PullReviewComment{{Path: "a.go", Line: 3, Body: "hi"}},
		Threads:  []gh.ReviewThread{{ID: "T1", Comments: []gh.ReviewThreadComment{{Body: "hi"}}}},
	}}
	got, ok := threadsLoadedMsg(b, ref()).(ThreadsLoadedMsg)
	if !ok {
		t.Fatalf("want ThreadsLoadedMsg")
	}
	if len(got.Comments) != 1 || len(got.Threads) != 1 {
		t.Fatalf("expected 1 comment + 1 thread, got %d/%d", len(got.Comments), len(got.Threads))
	}
}

func TestLoadPRsMsg(t *testing.T) {
	b := &fakeBackend{prs: []gh.PR{{Number: 1}, {Number: 2}}}
	msg := loadPRsMsg(b, gh.ListModeAuthored)
	got, ok := msg.(PRListMsg)
	if !ok {
		t.Fatalf("got %T want PRListMsg", msg)
	}
	if len(got.PRs) != 2 {
		t.Fatalf("prs: got %d want 2", len(got.PRs))
	}
	if b.listMode != gh.ListModeAuthored {
		t.Fatalf("mode not forwarded: %v", b.listMode)
	}
}

func TestLoadPRsMsgError(t *testing.T) {
	b := &fakeBackend{prsErr: errors.New("boom")}
	if _, ok := loadPRsMsg(b, gh.ListModeReviewTeams).(ErrMsg); !ok {
		t.Fatalf("expected ErrMsg on backend error")
	}
}

// TestLoadPRDetailMsgDerivesDiffRefFromPR pins the behavior that the diff is
// fetched against the ref of the *returned* PR (not the requested ref) so the
// demo fallback diff aligns with its fallback PR.
func TestLoadPRDetailMsgDerivesDiffRefFromPR(t *testing.T) {
	b := &fakeBackend{
		pr:   &gh.PR{Owner: "fallback", Repo: "repo", Number: 99, HeadSHA: "abc"},
		diff: "DIFF",
	}
	msg := loadPRDetailMsg(b, gh.Ref{Owner: "requested", Repo: "x", Number: 1})
	got, ok := msg.(PRDetailMsg)
	if !ok {
		t.Fatalf("got %T want PRDetailMsg", msg)
	}
	if got.Diff != "DIFF" || got.PR.Number != 99 {
		t.Fatalf("unexpected detail: %#v", got)
	}
	if b.diffRef != (gh.Ref{Owner: "fallback", Repo: "repo", Number: 99}) {
		t.Fatalf("diff ref should come from returned PR, got %#v", b.diffRef)
	}
}

func TestLoadPRDetailMsgErrors(t *testing.T) {
	if _, ok := loadPRDetailMsg(&fakeBackend{prErr: errors.New("x")}, ref()).(ErrMsg); !ok {
		t.Fatalf("PRDetail error should surface ErrMsg")
	}
	if _, ok := loadPRDetailMsg(&fakeBackend{pr: &gh.PR{}, diffErr: errors.New("x")}, ref()).(ErrMsg); !ok {
		t.Fatalf("Diff error should surface ErrMsg")
	}
}

func TestExistingCommentsMsg(t *testing.T) {
	b := &fakeBackend{existing: ExistingComments{
		Viewer:  "octocat",
		ListErr: errors.New("le"),
	}}
	msg := existingCommentsMsg(b, ref())
	got, ok := msg.(ExistingPRCommentsMsg)
	if !ok {
		t.Fatalf("got %T want ExistingPRCommentsMsg", msg)
	}
	if got.Viewer != "octocat" || got.ListErr == nil {
		t.Fatalf("existing comments not mapped through: %#v", got)
	}
}

func TestStartReviewMsg(t *testing.T) {
	ch := make(chan review.Progress)
	b := &fakeBackend{startCh: ch}
	msg := startReviewMsg(context.Background(), b, ref(), nil)
	if _, ok := msg.(ReviewStartedMsg); !ok {
		t.Fatalf("got %T want ReviewStartedMsg", msg)
	}

	if _, ok := startReviewMsg(context.Background(), &fakeBackend{startErr: errors.New("x")}, ref(), nil).(ErrMsg); !ok {
		t.Fatalf("start error should surface ErrMsg")
	}
}

// Phase 5 item 3: the caller-owned context is threaded into the runner so
// cancelling it (on overlay close / cancel key) actually stops the run. The
// fake backend records the ctx it was started with; cancelling the parent must
// fire that ctx's Done so the runner observes the cancellation.
func TestStartReviewThreadsCancellableContext(t *testing.T) {
	ch := make(chan review.Progress)
	b := &fakeBackend{startCh: ch}
	ctx, cancel := context.WithCancel(context.Background())

	if _, ok := startReviewMsg(ctx, b, ref(), nil).(ReviewStartedMsg); !ok {
		t.Fatal("expected ReviewStartedMsg")
	}
	if b.startCtx == nil {
		t.Fatal("the run should have captured the caller's context")
	}
	select {
	case <-b.startCtx.Done():
		t.Fatal("ctx should not be cancelled before the caller cancels it")
	default:
	}

	cancel()
	select {
	case <-b.startCtx.Done():
		if b.startCtx.Err() == nil {
			t.Fatal("cancelled ctx should report an error")
		}
	default:
		t.Fatal("cancelling the parent must cancel the run's context (no leaked runner)")
	}
}

// StartQueueReviewCmd's message body emits a QueueReviewStartedMsg (distinct
// from the interactive ReviewStartedMsg) carrying the ref + channel, and a
// QueueReviewErrMsg (not a fatal ErrMsg) when a run fails to start so the queue
// can advance rather than abort the batch.
func TestStartQueueReviewMsg(t *testing.T) {
	ch := make(chan review.Progress)
	b := &fakeBackend{startCh: ch}
	msg := startQueueReviewMsg(context.Background(), b, ref(), nil)
	started, ok := msg.(QueueReviewStartedMsg)
	if !ok {
		t.Fatalf("got %T want QueueReviewStartedMsg", msg)
	}
	if started.Ref != ref() || started.Ch == nil {
		t.Fatalf("queue-started msg should carry the ref + channel: %#v", started)
	}

	errMsg := startQueueReviewMsg(context.Background(), &fakeBackend{startErr: errors.New("boom")}, ref(), nil)
	qerr, ok := errMsg.(QueueReviewErrMsg)
	if !ok {
		t.Fatalf("a failed queue start should be a QueueReviewErrMsg (not ErrMsg), got %T", errMsg)
	}
	if qerr.Ref != ref() || qerr.Err == nil {
		t.Fatalf("queue-err msg should carry the ref + error: %#v", qerr)
	}
}

// WaitForQueueProgressCmd relays one progress event, then emits
// QueueReviewClosedMsg when the run's channel closes (so the queue advances).
func TestWaitForQueueProgress(t *testing.T) {
	ch := make(chan review.Progress, 1)
	ch <- review.Progress{Stage: "specialists", Detail: "docs:start"}
	if _, ok := WaitForQueueProgressCmd(ch)().(QueueProgressMsg); !ok {
		t.Fatal("a buffered progress event should surface as QueueProgressMsg")
	}
	close(ch)
	if _, ok := WaitForQueueProgressCmd(ch)().(QueueReviewClosedMsg); !ok {
		t.Fatal("a closed channel should surface as QueueReviewClosedMsg")
	}
}

func TestLoadChecksMsg(t *testing.T) {
	b := &fakeBackend{checks: &gh.ChecksReport{}}
	got, ok := loadChecksMsg(b, ref()).(ChecksMsg)
	if !ok || got.Report == nil || got.Err != nil {
		t.Fatalf("unexpected checks msg: %#v ok=%v", got, ok)
	}
	errGot, ok := loadChecksMsg(&fakeBackend{checksErr: errors.New("x")}, ref()).(ChecksMsg)
	if !ok || errGot.Err == nil {
		t.Fatalf("checks error should populate Err: %#v", errGot)
	}
}

func TestLoadDiscussionMsg(t *testing.T) {
	got, ok := loadDiscussionMsg(&fakeBackend{disc: []gh.DiscussionEvent{{}}}, ref()).(DiscussionMsg)
	if !ok || len(got.Timeline) != 1 || got.Err != nil {
		t.Fatalf("unexpected discussion msg: %#v ok=%v", got, ok)
	}
	errGot, ok := loadDiscussionMsg(&fakeBackend{discErr: errors.New("x")}, ref()).(DiscussionMsg)
	if !ok || errGot.Err == nil {
		t.Fatalf("discussion error should populate Err: %#v", errGot)
	}
}

func draftForPost() *review.Draft {
	return &review.Draft{
		PR: &gh.PR{Owner: "o", Repo: "r", Number: 7, HeadSHA: "headsha"},
		Specialists: []review.SpecialistResult{{
			Specialist: review.SpecDocs,
			Findings:   []review.Finding{{Path: "a.go", Line: 3, Comment: "c", Severity: review.SeverityWarning}},
		}},
		VibeCoach: &review.VibeCoachResult{Verdict: review.VibeVerdictRequestChanges},
	}
}

func TestPostReviewMsgDryRunSkipsBackend(t *testing.T) {
	b := &fakeBackend{}
	msg := postReviewMsg(b, ref(), draftForPost(), true)
	if _, ok := msg.(DryRunPayloadMsg); !ok {
		t.Fatalf("dry-run should return DryRunPayloadMsg, got %T", msg)
	}
	if b.postedReview != nil {
		t.Fatalf("dry-run must not post to the backend")
	}
}

func TestPostReviewMsgRealPost(t *testing.T) {
	b := &fakeBackend{headSHA: "headsha"} // matches draft head → no drift
	msg := postReviewMsg(b, ref(), draftForPost(), false)
	if _, ok := msg.(PostDoneMsg); !ok {
		t.Fatalf("real post should return PostDoneMsg, got %T", msg)
	}
	if b.postedReview == nil {
		t.Fatalf("real post should reach the backend")
	}
}

func TestPostReviewMsgHeadDriftBlocksPost(t *testing.T) {
	b := &fakeBackend{headSHA: "different"} // != draft head → drift
	msg := postReviewMsg(b, ref(), draftForPost(), false)
	em, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("drift should surface ErrMsg, got %T", msg)
	}
	if _, isDrift := gh.IsHeadDrift(em.Err); !isDrift {
		t.Fatalf("expected *gh.HeadDriftError, got %v", em.Err)
	}
	if b.postedReview != nil {
		t.Fatalf("drift must block the post")
	}
}

func TestPostReviewMsgHeadSHALookupFailureStillPosts(t *testing.T) {
	// A failed head-SHA lookup must not refuse the post — we'd rather attempt
	// it and report a real GitHub error.
	b := &fakeBackend{headErr: errors.New("offline")}
	if _, ok := postReviewMsg(b, ref(), draftForPost(), false).(PostDoneMsg); !ok {
		t.Fatalf("head-SHA lookup failure should not block the post")
	}
	if b.postedReview == nil {
		t.Fatalf("post should still reach the backend")
	}
}

func TestPostSingleFindingMsg(t *testing.T) {
	b := &fakeBackend{headSHA: "headsha"}
	f := review.Finding{Path: "a.go", Line: 3, Comment: "c"}
	if _, ok := postSingleFindingMsg(b, ref(), draftForPost().PR, review.SpecDocs, f, "", false).(StagedFindingPostedMsg); !ok {
		t.Fatalf("real inline post should return StagedFindingPostedMsg")
	}
	if b.postedInline == nil || b.postedInline.Side != "RIGHT" || b.postedInline.Path != "a.go" {
		t.Fatalf("inline comment not posted as expected: %#v", b.postedInline)
	}
	if len(b.replies) != 0 {
		t.Fatalf("no-thread finding must not reply, got %#v", b.replies)
	}

	dry := postSingleFindingMsg(&fakeBackend{}, ref(), draftForPost().PR, review.SpecDocs, f, "", true)
	if _, ok := dry.(DryRunPayloadMsg); !ok {
		t.Fatalf("dry-run inline should return DryRunPayloadMsg, got %T", dry)
	}
}

// TestPostSingleFindingMsgRoutesToReply confirms a non-empty threadID routes
// the finding to an in-thread reply instead of a top-level inline comment.
func TestPostSingleFindingMsgRoutesToReply(t *testing.T) {
	b := &fakeBackend{headSHA: "headsha"}
	f := review.Finding{Path: "a.go", Line: 3, Comment: "still broken"}
	if _, ok := postSingleFindingMsg(b, ref(), draftForPost().PR, review.SpecDocs, f, "PRRT_1", false).(StagedFindingPostedMsg); !ok {
		t.Fatalf("reply post should return StagedFindingPostedMsg")
	}
	if b.postedInline != nil {
		t.Fatalf("reply must not post a top-level inline comment: %#v", b.postedInline)
	}
	if len(b.replies) != 1 || b.replies[0].threadID != "PRRT_1" {
		t.Fatalf("expected one reply to PRRT_1, got %#v", b.replies)
	}
	if !strings.Contains(b.replies[0].body, "still broken") {
		t.Fatalf("reply body should carry the finding comment: %q", b.replies[0].body)
	}
}

// TestPostSingleFindingMsgReplyDryRun confirms the dry-run preview discloses
// the reply routing rather than a fresh top-level comment.
func TestPostSingleFindingMsgReplyDryRun(t *testing.T) {
	f := review.Finding{Path: "a.go", Line: 3, Comment: "c"}
	msg := postSingleFindingMsg(&fakeBackend{}, ref(), draftForPost().PR, review.SpecDocs, f, "PRRT_1", true)
	dr, ok := msg.(DryRunPayloadMsg)
	if !ok {
		t.Fatalf("dry-run reply should return DryRunPayloadMsg, got %T", msg)
	}
	if !strings.Contains(dr.Title, "reply to existing thread") {
		t.Fatalf("dry-run title should disclose reply routing: %q", dr.Title)
	}
}

// TestPostSingleFindingMsgReplyFailOpen confirms a failed reply surfaces as an
// ErrMsg (reported like a normal post failure), never a crash.
func TestPostSingleFindingMsgReplyFailOpen(t *testing.T) {
	b := &fakeBackend{replyErr: errors.New("thread resolved")}
	f := review.Finding{Path: "a.go", Line: 3, Comment: "c"}
	if _, ok := postSingleFindingMsg(b, ref(), draftForPost().PR, review.SpecDocs, f, "PRRT_1", false).(ErrMsg); !ok {
		t.Fatalf("failed reply should surface ErrMsg")
	}
}

// TestPostStatusRepliesMsg confirms the re-run status replies post to the
// tool's own prior threads (resolved / still present) via ReplyToThread.
func TestPostStatusRepliesMsg(t *testing.T) {
	priorDiff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n package a\n+var leak = openResourceWithoutClosing() // status-marker-unique-line\n func x() {}\n"
	newDiff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,2 @@\n package a\n func x() {}\n"
	draft := &review.Draft{
		PR:   &gh.PR{Owner: "o", Repo: "r", Number: 7, HeadSHA: "newsha1234"},
		Diff: newDiff,
		PriorReview: &review.CachedDraft{
			Diff: priorDiff,
			Specialists: []review.SpecialistResult{{
				Specialist: review.SpecSecurity,
				Findings: []review.Finding{{
					Path: "a.go", Line: 2, Side: "RIGHT", Severity: review.SeverityError,
					Comment: "resource leak", AnchorExcerpt: "var leak = openResourceWithoutClosing() // status-marker-unique-line",
				}},
			}},
		},
	}
	own := gh.ReviewThread{
		ID: "PRRT_own",
		Comments: []gh.ReviewThreadComment{{
			Author: "octocat",
			Body:   "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **security**\n\nresource leak",
			Path:   "a.go", Line: 2, Side: "RIGHT",
		}},
	}
	b := &fakeBackend{}
	msg := postStatusRepliesMsg(b, ref(), draft, []gh.ReviewThread{own}, "octocat")
	got, ok := msg.(StatusRepliesPostedMsg)
	if !ok {
		t.Fatalf("expected StatusRepliesPostedMsg, got %T", msg)
	}
	if got.Posted != 1 || got.Failed != 0 {
		t.Fatalf("expected 1 posted status reply, got %+v", got)
	}
	if len(b.replies) != 1 || b.replies[0].threadID != "PRRT_own" {
		t.Fatalf("status reply should target own thread, got %#v", b.replies)
	}
	if !strings.Contains(b.replies[0].body, "resolved") {
		t.Fatalf("status reply should report resolved (code gone): %q", b.replies[0].body)
	}
}

// TestPostStatusRepliesMsgFirstReviewNoop confirms a first review (no prior
// cache) posts nothing.
func TestPostStatusRepliesMsgFirstReviewNoop(t *testing.T) {
	draft := &review.Draft{PR: &gh.PR{Owner: "o", Repo: "r", Number: 7, HeadSHA: "s"}}
	b := &fakeBackend{}
	msg := postStatusRepliesMsg(b, ref(), draft, nil, "octocat")
	if got, ok := msg.(StatusRepliesPostedMsg); !ok || got.Posted != 0 {
		t.Fatalf("first review should post no status replies, got %#v", msg)
	}
	if len(b.replies) != 0 {
		t.Fatalf("first review must not reply, got %#v", b.replies)
	}
}

func TestPostSingleFindingFileLevelMsg(t *testing.T) {
	b := &fakeBackend{headSHA: "headsha"}
	f := review.Finding{Path: "a.go", Line: 42, Comment: "c"}
	if _, ok := postSingleFindingFileLevelMsg(b, ref(), draftForPost().PR, review.SpecDocs, f, false).(StagedFindingPostedMsg); !ok {
		t.Fatalf("real file-level post should return StagedFindingPostedMsg")
	}
	if b.postedFile == nil || b.postedFile.path != "a.go" || !strings.Contains(b.postedFile.body, "Intended for line 42") {
		t.Fatalf("file-level comment not posted as expected: %#v", b.postedFile)
	}
}

// TestPostReviewWithVerdictMsgSelfAuthorDowngrade verifies the viewer login
// from the backend drives the self-author downgrade: an author-viewer match
// coerces REQUEST_CHANGES to a COMMENT event on the posted review.
func TestPostReviewWithVerdictMsgSelfAuthorDowngrade(t *testing.T) {
	d := draftForPost()
	d.PR.Author = "octocat"
	b := &fakeBackend{viewer: "octocat", headSHA: "headsha"}
	if _, ok := postReviewWithVerdictMsg(b, ref(), d, false, "REQUEST_CHANGES").(PostDoneMsg); !ok {
		t.Fatalf("expected PostDoneMsg")
	}
	if b.postedReview == nil || b.postedReview.Event != "COMMENT" {
		t.Fatalf("self-author should downgrade to COMMENT event, got %#v", b.postedReview)
	}
	if !strings.Contains(b.postedReview.Body, "does not allow") {
		t.Fatalf("downgrade body should carry the explanatory note: %q", b.postedReview.Body)
	}
}

func TestPostReviewWithVerdictMsgOtherReviewer(t *testing.T) {
	d := draftForPost()
	d.PR.Author = "alice"
	b := &fakeBackend{viewer: "bob", headSHA: "headsha"}
	if _, ok := postReviewWithVerdictMsg(b, ref(), d, false, "REQUEST_CHANGES").(PostDoneMsg); !ok {
		t.Fatalf("expected PostDoneMsg")
	}
	if b.postedReview == nil || b.postedReview.Event != "REQUEST_CHANGES" {
		t.Fatalf("non-author verdict should stay REQUEST_CHANGES, got %#v", b.postedReview)
	}
}

func TestPostReviewWithVerdictMsgDryRun(t *testing.T) {
	msg := postReviewWithVerdictMsg(&fakeBackend{viewer: "bob"}, ref(), draftForPost(), true, "REQUEST_CHANGES")
	dr, ok := msg.(DryRunPayloadMsg)
	if !ok {
		t.Fatalf("dry-run should return DryRunPayloadMsg, got %T", msg)
	}
	if !strings.Contains(dr.Title, "REQUEST_CHANGES") {
		t.Fatalf("dry-run title should name the event: %q", dr.Title)
	}
}

func TestPostApproveBareMsg(t *testing.T) {
	d := draftForPost()
	d.VibeCoach = &review.VibeCoachResult{Verdict: review.VibeVerdictApprove}
	b := &fakeBackend{viewer: "bob", headSHA: "headsha"}
	if _, ok := postApproveBareMsg(b, ref(), d, false).(PostDoneMsg); !ok {
		t.Fatalf("expected PostDoneMsg")
	}
	if b.postedReview == nil || b.postedReview.Event != "APPROVE" || b.postedReview.Body != "" {
		t.Fatalf("approve-only should post APPROVE with empty body, got %#v", b.postedReview)
	}
}

func TestRefreshPRMsg(t *testing.T) {
	b := &fakeBackend{pr: &gh.PR{Owner: "o", Repo: "r", Number: 7, HeadSHA: "new"}, diff: "D"}
	got, ok := refreshPRMsg(b, ref()).(PRRefreshedMsg)
	if !ok || got.PR.HeadSHA != "new" || got.Diff != "D" {
		t.Fatalf("unexpected refresh msg: %#v ok=%v", got, ok)
	}
}

func TestRefreshPRMsgWrapsErrors(t *testing.T) {
	prErr := refreshPRMsg(&fakeBackend{prErr: errors.New("x")}, ref())
	em, ok := prErr.(ErrMsg)
	if !ok || !strings.Contains(em.Err.Error(), "refresh PR:") {
		t.Fatalf("PR error should be wrapped 'refresh PR:', got %#v", prErr)
	}
	diffErr := refreshPRMsg(&fakeBackend{pr: &gh.PR{}, diffErr: errors.New("x")}, ref())
	em2, ok := diffErr.(ErrMsg)
	if !ok || !strings.Contains(em2.Err.Error(), "refresh PR diff:") {
		t.Fatalf("diff error should be wrapped 'refresh PR diff:', got %#v", diffErr)
	}
}

// TestSelectBackendDemoPassesThroughInterface confirms demo mode routes through
// the same Backend interface — the demo backend returns the canned fixture via
// the identical command flow, with no demo-specific branch in the command.
func TestSelectBackendDemoPassesThroughInterface(t *testing.T) {
	b := selectBackend(true)
	if _, ok := b.(demoBackend); !ok {
		t.Fatalf("selectBackend(true) should be demoBackend, got %T", b)
	}
	msg := loadPRsMsg(b, gh.ListModeReviewTeams)
	got, ok := msg.(PRListMsg)
	if !ok || len(got.PRs) == 0 {
		t.Fatalf("demo backend should yield canned PRs through the same flow, got %#v", msg)
	}

	if _, ok := selectBackend(false).(ghBackend); !ok {
		t.Fatalf("selectBackend(false) should be ghBackend")
	}
}
