package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// fakePoster records every post so tests can assert on the F7/B3 posting path
// without touching GitHub.
type fakePoster struct {
	headSHA   string
	threads   []gh.ReviewThread
	viewer    string
	reviews   []gh.Review
	inline    []gh.ReviewComment
	replies   []string // thread IDs replied to
	postErr   error
	inlineErr error
	replyErr  error
}

func (p *fakePoster) HeadSHA(context.Context, gh.Ref) (string, error) { return p.headSHA, nil }
func (p *fakePoster) ReviewThreads(context.Context, gh.Ref) ([]gh.ReviewThread, error) {
	return p.threads, nil
}
func (p *fakePoster) ViewerLogin(context.Context) string { return p.viewer }
func (p *fakePoster) PostReview(_ context.Context, _ gh.Ref, rev gh.Review) error {
	if p.postErr != nil {
		return p.postErr
	}
	p.reviews = append(p.reviews, rev)
	return nil
}
func (p *fakePoster) PostInlineComment(_ context.Context, _ gh.Ref, _ string, c gh.ReviewComment) error {
	if p.inlineErr != nil {
		return p.inlineErr
	}
	p.inline = append(p.inline, c)
	return nil
}
func (p *fakePoster) ReplyToThread(_ context.Context, _ gh.Ref, threadID, _ string) error {
	if p.replyErr != nil {
		return p.replyErr
	}
	p.replies = append(p.replies, threadID)
	return nil
}

// fakeRun returns a review.Run stand-in that streams the given progress events
// then a final "done" event carrying draft.
func fakeRun(events []review.Progress, draft *review.Draft) func(context.Context, gh.Ref, *aiconfig.Config, review.Bootstrap) (<-chan review.Progress, error) {
	return func(context.Context, gh.Ref, *aiconfig.Config, review.Bootstrap) (<-chan review.Progress, error) {
		ch := make(chan review.Progress, len(events)+1)
		for _, e := range events {
			ch <- e
		}
		ch <- review.Progress{Stage: "done", Final: draft}
		close(ch)
		return ch, nil
	}
}

// validConfig returns an ollama profile — it validates without a claude binary
// on PATH, a base URL, or an API key, so the happy path is hermetic.
func validConfig() (*aiconfig.Config, error) {
	c := aiconfig.DefaultConfig()
	c.Provider = aiconfig.ProviderOllama
	c.BaseURL = ""
	c.APIKey = ""
	return c, nil
}

func draftWithVerdict(verdict string, findings []review.Finding) *review.Draft {
	return &review.Draft{
		Ref: gh.Ref{Owner: "o", Repo: "r", Number: 7},
		PR:  &gh.PR{Owner: "o", Repo: "r", Number: 7, HeadSHA: "headsha", Author: "author"},
		VibeCoach: &review.VibeCoachResult{
			Verdict: verdict,
			Summary: "the summary",
		},
		Specialists: []review.SpecialistResult{{
			Specialist: "security",
			Findings:   findings,
		}},
	}
}

func errorFinding() review.Finding {
	return review.Finding{
		Path:     "a.go",
		Line:     10,
		Side:     "RIGHT",
		Severity: review.SeverityError,
		Comment:  "this is unsafe",
	}
}

func baseDeps(draft *review.Draft, poster poster) reviewDeps {
	return reviewDeps{
		loadConfig: validConfig,
		checkAuth:  func() error { return nil },
		run:        fakeRun(nil, draft),
		poster:     poster,
	}
}

// ---------------------------------------------------------------------------
// Happy path: NDJSON progress on stderr, Draft JSON on stdout, exit 0
// ---------------------------------------------------------------------------

func TestReviewJSONHappyPath(t *testing.T) {
	draft := draftWithVerdict(review.VibeVerdictComment, []review.Finding{errorFinding()})
	events := []review.Progress{
		{Stage: "checkout", Detail: "wt"},
		{Stage: "specialist", Result: &review.SpecialistResult{Specialist: "security"}},
		{Stage: "usage", Usage: &review.RunUsage{Calls: 3, InputTokens: 1000, OutputTokens: 200}},
	}
	deps := reviewDeps{
		loadConfig: validConfig,
		checkAuth:  func() error { return nil },
		run:        fakeRun(events, draft),
		poster:     &fakePoster{headSHA: "headsha"},
	}
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--json", "o/r#7"}, &out, &errb, deps)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, ExitOK, errb.String())
	}

	// stdout is a single JSON object (pipeable to jq) — parse it.
	var res reviewResultJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if res.Ref != "o/r#7" {
		t.Errorf("ref = %q, want o/r#7", res.Ref)
	}
	if res.Verdict != review.VibeVerdictComment {
		t.Errorf("verdict = %q, want comment", res.Verdict)
	}
	if len(res.Findings) != 1 || !res.Findings[0].Inline {
		t.Errorf("findings = %+v, want 1 inline finding", res.Findings)
	}

	// stderr is NDJSON: each non-empty line must be a valid progress object,
	// and the stages we emitted must appear.
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(errb.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev progressJSON
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stderr line is not valid JSON: %v\n%q", err, line)
		}
		seen[ev.Stage] = true
	}
	for _, want := range []string{"checkout", "specialist", "usage", "done"} {
		if !seen[want] {
			t.Errorf("missing NDJSON progress stage %q; got %v", want, seen)
		}
	}
}

// ---------------------------------------------------------------------------
// --fail-on exit-code mapping
// ---------------------------------------------------------------------------

func TestReviewFailOnTriggersNonZero(t *testing.T) {
	// request_changes verdict + a surviving error finding = blocking, so the
	// reconciled verdict stays request_changes and the gate fires.
	draft := draftWithVerdict(review.VibeVerdictRequestChanges, []review.Finding{errorFinding()})
	deps := baseDeps(draft, &fakePoster{headSHA: "headsha"})
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--json", "--fail-on", "request_changes", "o/r#7"}, &out, &errb, deps)
	if code != ExitFailOn {
		t.Fatalf("exit code = %d, want ExitFailOn(%d); stderr=%s", code, ExitFailOn, errb.String())
	}
}

func TestReviewFailOnUnderThresholdExitsZero(t *testing.T) {
	draft := draftWithVerdict(review.VibeVerdictApprove, nil)
	deps := baseDeps(draft, &fakePoster{headSHA: "headsha"})
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--json", "--fail-on", "request_changes", "o/r#7"}, &out, &errb, deps)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK; stderr=%s", code, errb.String())
	}
}

func TestReviewFailOnCommentGatesComment(t *testing.T) {
	draft := draftWithVerdict(review.VibeVerdictComment, []review.Finding{errorFinding()})
	deps := baseDeps(draft, &fakePoster{headSHA: "headsha"})
	var out, errb bytes.Buffer
	// comment rank(1) >= comment threshold rank(1) → gate fires.
	code := runReview(context.Background(), []string{"--fail-on", "comment", "o/r#7"}, &out, &errb, deps)
	if code != ExitFailOn {
		t.Fatalf("exit code = %d, want ExitFailOn; stderr=%s", code, errb.String())
	}
}

// ---------------------------------------------------------------------------
// --dry-run vs --post
// ---------------------------------------------------------------------------

func TestReviewDryRunPreviewsWithoutPosting(t *testing.T) {
	draft := draftWithVerdict(review.VibeVerdictComment, []review.Finding{errorFinding()})
	fp := &fakePoster{headSHA: "headsha"}
	deps := baseDeps(draft, fp)
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--json", "--dry-run", "o/r#7"}, &out, &errb, deps)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK; stderr=%s", code, errb.String())
	}
	if len(fp.reviews) != 0 || len(fp.inline) != 0 || len(fp.replies) != 0 {
		t.Fatalf("dry-run must not post anything: reviews=%d inline=%d replies=%d", len(fp.reviews), len(fp.inline), len(fp.replies))
	}
	var res reviewResultJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("bad stdout json: %v", err)
	}
	if res.Post == nil || !res.Post.DryRun || len(res.Post.Previews) == 0 {
		t.Fatalf("expected dry-run previews, got %+v", res.Post)
	}
}

func TestReviewPostSubmitsReviewAndInline(t *testing.T) {
	draft := draftWithVerdict(review.VibeVerdictRequestChanges, []review.Finding{errorFinding()})
	fp := &fakePoster{headSHA: "headsha", viewer: "reviewer"}
	deps := baseDeps(draft, fp)
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--json", "--post", "o/r#7"}, &out, &errb, deps)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK; stderr=%s", code, errb.String())
	}
	if len(fp.inline) != 1 {
		t.Errorf("want 1 inline comment posted, got %d", len(fp.inline))
	}
	if len(fp.reviews) != 1 || fp.reviews[0].Event != "REQUEST_CHANGES" {
		t.Errorf("want one REQUEST_CHANGES review, got %+v", fp.reviews)
	}
	if len(fp.replies) != 0 {
		t.Errorf("no threads present, want 0 replies, got %d", len(fp.replies))
	}
}

func TestReviewPostRoutesToThreadReply(t *testing.T) {
	// A thread anchored at the finding's location, opened by the tool → the
	// finding must reply in-thread (B3) instead of a fresh inline comment.
	draft := draftWithVerdict(review.VibeVerdictRequestChanges, []review.Finding{errorFinding()})
	fp := &fakePoster{
		headSHA: "headsha",
		viewer:  "reviewer",
		threads: []gh.ReviewThread{{
			ID:         "THREAD1",
			IsResolved: false,
			Comments: []gh.ReviewThreadComment{{
				Author: "reviewer",
				Body:   "prior note " + gh.AprrAISalInlineMarker,
				Path:   "a.go",
				Line:   10,
				Side:   "RIGHT",
			}},
		}},
	}
	deps := baseDeps(draft, fp)
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--post", "o/r#7"}, &out, &errb, deps)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK; stderr=%s", code, errb.String())
	}
	if len(fp.replies) != 1 || fp.replies[0] != "THREAD1" {
		t.Errorf("want one reply to THREAD1, got %v", fp.replies)
	}
	if len(fp.inline) != 0 {
		t.Errorf("thread match must reroute; want 0 fresh inline comments, got %d", len(fp.inline))
	}
}

func TestReviewPostAndDryRunMutuallyExclusive(t *testing.T) {
	draft := draftWithVerdict(review.VibeVerdictComment, nil)
	deps := baseDeps(draft, &fakePoster{})
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--post", "--dry-run", "o/r#7"}, &out, &errb, deps)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage", code)
	}
}

// ---------------------------------------------------------------------------
// Config validation error path
// ---------------------------------------------------------------------------

func TestReviewConfigValidationError(t *testing.T) {
	badConfig := func() (*aiconfig.Config, error) {
		c := aiconfig.DefaultConfig()
		c.Provider = aiconfig.ProviderOpenAICompatible
		c.BaseURL = "" // openai_compatible requires a base URL → ValidateForProvider fails
		return c, nil
	}
	deps := reviewDeps{
		loadConfig: badConfig,
		checkAuth:  func() error { return nil },
		run:        fakeRun(nil, draftWithVerdict(review.VibeVerdictComment, nil)),
		poster:     &fakePoster{},
	}
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"--json", "o/r#7"}, &out, &errb, deps)
	if code != ExitConfig {
		t.Fatalf("exit code = %d, want ExitConfig(%d); stderr=%s", code, ExitConfig, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("config failure must not write to stdout, got %q", out.String())
	}
}

// ---------------------------------------------------------------------------
// Usage / argument errors
// ---------------------------------------------------------------------------

func TestReviewUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"no ref", []string{"--json"}},
		{"too many refs", []string{"o/r#7", "o/r#8"}},
		{"bad ref", []string{"not-a-ref"}},
		{"bad fail-on", []string{"--fail-on", "nonsense", "o/r#7"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := baseDeps(draftWithVerdict(review.VibeVerdictComment, nil), &fakePoster{})
			var out, errb bytes.Buffer
			code := runReview(context.Background(), tc.argv, &out, &errb, deps)
			if code != ExitUsage {
				t.Fatalf("exit code = %d, want ExitUsage(%d)", code, ExitUsage)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Auth + operational failure
// ---------------------------------------------------------------------------

func TestReviewAuthFailure(t *testing.T) {
	deps := baseDeps(draftWithVerdict(review.VibeVerdictComment, nil), &fakePoster{})
	deps.checkAuth = func() error { return errFake("no auth") }
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"o/r#7"}, &out, &errb, deps)
	if code != ExitError {
		t.Fatalf("exit code = %d, want ExitError(%d)", code, ExitError)
	}
}

func TestReviewNoFinalDraftIsOperationalError(t *testing.T) {
	runNoFinal := func(context.Context, gh.Ref, *aiconfig.Config, review.Bootstrap) (<-chan review.Progress, error) {
		ch := make(chan review.Progress, 1)
		ch <- review.Progress{Stage: "fetch-pr", Err: errFake("boom")}
		close(ch)
		return ch, nil
	}
	deps := reviewDeps{
		loadConfig: validConfig,
		checkAuth:  func() error { return nil },
		run:        runNoFinal,
		poster:     &fakePoster{},
	}
	var out, errb bytes.Buffer
	code := runReview(context.Background(), []string{"o/r#7"}, &out, &errb, deps)
	if code != ExitError {
		t.Fatalf("exit code = %d, want ExitError; stderr=%s", code, errb.String())
	}
	// The fatal stage error must still surface as NDJSON on stderr.
	if !strings.Contains(errb.String(), "\"stage\":\"fetch-pr\"") {
		t.Errorf("expected fetch-pr NDJSON error on stderr, got %q", errb.String())
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
