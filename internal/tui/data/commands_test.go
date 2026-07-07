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

	// recorded calls
	listMode      gh.ListMode
	diffRef       gh.Ref
	postedReview  *gh.Review
	postReviewErr error
	postedInline  *gh.ReviewComment
	postInlineErr error
	postedFile    *fileLevelPost
	postFileErr   error
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
func (f *fakeBackend) StartReview(_ context.Context, _ gh.Ref, _ *aiconfig.Config) (<-chan review.Progress, error) {
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

var _ Backend = (*fakeBackend)(nil)

func ref() gh.Ref { return gh.Ref{Owner: "o", Repo: "r", Number: 7} }

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
	msg := startReviewMsg(b, ref(), nil)
	if _, ok := msg.(ReviewStartedMsg); !ok {
		t.Fatalf("got %T want ReviewStartedMsg", msg)
	}

	if _, ok := startReviewMsg(&fakeBackend{startErr: errors.New("x")}, ref(), nil).(ErrMsg); !ok {
		t.Fatalf("start error should surface ErrMsg")
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
	if _, ok := postSingleFindingMsg(b, ref(), draftForPost().PR, review.SpecDocs, f, false).(StagedFindingPostedMsg); !ok {
		t.Fatalf("real inline post should return StagedFindingPostedMsg")
	}
	if b.postedInline == nil || b.postedInline.Side != "RIGHT" || b.postedInline.Path != "a.go" {
		t.Fatalf("inline comment not posted as expected: %#v", b.postedInline)
	}

	dry := postSingleFindingMsg(&fakeBackend{}, ref(), draftForPost().PR, review.SpecDocs, f, true)
	if _, ok := dry.(DryRunPayloadMsg); !ok {
		t.Fatalf("dry-run inline should return DryRunPayloadMsg, got %T", dry)
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
