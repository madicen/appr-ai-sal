// Package gh is the GitHub integration layer. Auth, host, and tokens resolve
// through the user's `gh` CLI configuration; REST and GraphQL traffic runs
// in-process via go-gh. Git subprocesses (clone/fetch/checkout) remain for
// worktree setup.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madicen/appr-ai-sal/internal/applog"
)

// PR is a flattened view of a GitHub pull request, populated from one or more
// gh CLI calls (search returns less detail than pr view).
type PR struct {
	Number     int
	Title      string
	URL        string
	Repository string // "owner/name"
	Owner      string
	Repo       string
	Author     string
	Body       string
	BaseRef    string
	HeadRef    string
	HeadSHA    string
	IsDraft    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time

	// Additions / Deletions / ChangedFiles are aggregate diff stats for the
	// PR as reported by GitHub (additions and deletions are line counts,
	// changedFiles is the file count). Populated by ListReviewRequestedPRs
	// and GetPR when the underlying call returns them; zero when unavailable.
	Additions    int
	Deletions    int
	ChangedFiles int

	// ChecksState is GitHub's status-check rollup for the PR's head commit.
	// One of "SUCCESS", "FAILURE", "PENDING", "ERROR", "EXPECTED" or empty
	// when no checks have been configured / reported. Used to render the
	// queue's check-rollup chip.
	ChecksState string

	// ReviewState carries the per-PR review summary + viewer-relative flags
	// (approvals, your-review-still-needed). Populated by
	// ListReviewRequestedPRs and GetPR; zero-valued when those weren't able
	// to fetch the review data so callers can render neutrally.
	ReviewState ReviewState
}

// Ref points at a single PR. It's the smallest thing the rest of the app needs
// to identify which PR we're working with.
type Ref struct {
	Owner  string
	Repo   string
	Number int
}

func (r Ref) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

// Review is what we POST to /repos/{owner}/{repo}/pulls/{number}/reviews.
type Review struct {
	CommitID string          `json:"commit_id,omitempty"`
	Body     string          `json:"body"`
	Event    string          `json:"event"` // COMMENT, REQUEST_CHANGES, APPROVE
	Comments []ReviewComment `json:"comments,omitempty"`
}

// ReviewComment is a single inline review comment. StartLine/StartSide are
// set only for multi-line comments (a suggestion spanning StartLine..Line);
// GitHub requires start_line < line and both on the same side. They are
// omitted for the single-line default so existing single-line posts are byte
// identical.
type ReviewComment struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side,omitempty"`       // LEFT (old) or RIGHT (new); default RIGHT
	StartLine int    `json:"start_line,omitempty"` // multi-line: first line of the range
	StartSide string `json:"start_side,omitempty"` // multi-line: side of StartLine (default RIGHT)
	Body      string `json:"body"`
}

// CheckAuth returns nil if gh is installed, recent enough (see MinGHVersion),
// and the user is logged in.
func CheckAuth() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh not found on PATH; install from https://cli.github.com")
	}
	// R6.5: reject a too-old gh CLI with a clear message before the auth
	// check. Credential validity is verified in-process via the GraphQL viewer
	// query (see checkAuthViaAPI).
	if err := checkGHVersion(); err != nil {
		return err
	}
	return checkAuthViaAPI(context.Background())
}

// viewerLoginCache holds the authenticated viewer's login for the process
// lifetime. The login can't change under a running session, so we resolve it
// once — either from the ListPRs GraphQL response (which already returns
// viewer{login}) or a single `gh api user` call — and reuse it, sparing an
// extra gh exec on every GetPR.
var (
	viewerLoginMu    sync.RWMutex
	viewerLoginCache string
)

// cacheViewerLogin stores a resolved viewer login for reuse this session.
// Empty logins are ignored so a failed lookup never poisons the cache.
func cacheViewerLogin(login string) {
	login = strings.TrimSpace(login)
	if login == "" {
		return
	}
	viewerLoginMu.Lock()
	viewerLoginCache = login
	viewerLoginMu.Unlock()
}

// cachedViewerLogin returns the cached viewer login, or "" when unset.
func cachedViewerLogin() string {
	viewerLoginMu.RLock()
	defer viewerLoginMu.RUnlock()
	return viewerLoginCache
}

// ViewerLogin returns the GitHub login for the authenticated gh user. The
// result is cached for the process lifetime (see viewerLoginCache), so
// repeated callers — notably GetPR — don't each re-exec `gh api user`.
func ViewerLogin(ctx context.Context) (string, error) {
	if v := cachedViewerLogin(); v != "" {
		return v, nil
	}
	var resp struct {
		Login string `json:"login"`
	}
	if err := ghAPIGet(ctx, "user", &resp); err != nil {
		return "", err
	}
	login := strings.TrimSpace(resp.Login)
	cacheViewerLogin(login)
	return login, nil
}

// IsUserExplicitlyRequested returns true if login appears in the PR's
// requested_reviewers (not only via a requested team).
func IsUserExplicitlyRequested(ctx context.Context, pr PR, login string) (bool, error) {
	if login == "" {
		return false, nil
	}
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number)
	var resp struct {
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
	}
	if err := ghAPIGet(ctx, path, &resp); err != nil {
		return false, err
	}
	for _, r := range resp.RequestedReviewers {
		if r.Login == login {
			return true, nil
		}
	}
	return false, nil
}

// ListMode selects which slice of PRs ListPRs returns. The TUI's top-panel
// filter chips map one-to-one onto these values; expanding the set means
// adding a chip + a query-string branch below.
type ListMode int

const (
	// ListModeReviewTeams returns the legacy "review-requested:@me" set
	// (the user is requested directly or via a team). Same GitHub query
	// as the explicit variant; just no client-side narrowing.
	ListModeReviewTeams ListMode = iota
	// ListModeReviewExplicit narrows ListModeReviewTeams to PRs where
	// the viewer's login appears directly in reviewRequests (drops the
	// team-only requests).
	ListModeReviewExplicit
	// ListModeAuthored returns PRs the viewer has authored
	// (author:@me). Lets the user pivot from "PRs I need to review" to
	// "my own PRs" without leaving the queue.
	ListModeAuthored
)

// ListPRs returns the set of PRs matching mode. All branches share the
// same parseReviewSearchResponse path so the resulting PRs carry the
// usual ReviewState / ChecksState the TUI's row delegate renders.
//
// New filter modes plug in here: add a const above, a switch arm below,
// and (optionally) a client-side narrow.
func ListPRs(ctx context.Context, mode ListMode) ([]PR, error) {
	q := listModeQuery(mode)
	data, err := graphQLQuery[graphqlReviewData](ctx, graphqlReviewQuery, map[string]any{"q": q})
	if err != nil {
		return nil, err
	}
	prs, viewer := reviewDataToPRs(data)
	// The GraphQL query already returns viewer{login}; cache it so GetPR
	// and other viewer-scoped lookups don't re-hit the API for it.
	cacheViewerLogin(viewer)
	if mode == ListModeReviewExplicit {
		filtered := make([]PR, 0, len(prs))
		for _, pr := range prs {
			if pr.ReviewState.ViewerStillRequested {
				filtered = append(filtered, pr)
			}
		}
		return filtered, nil
	}
	return prs, nil
}

// listModeQuery is the GitHub search query string for each mode. Kept
// in its own function so tests can assert the wire-level query without
// touching the gh CLI.
func listModeQuery(mode ListMode) string {
	switch mode {
	case ListModeAuthored:
		return "is:pr is:open author:@me archived:false"
	default:
		return "is:pr is:open review-requested:@me archived:false"
	}
}

// ListReviewRequestedPRs is the legacy entry point retained for any
// external consumer; new code should call ListPRs directly. The
// boolean maps onto the two review-requested branches of ListMode.
func ListReviewRequestedPRs(ctx context.Context, explicitReviewerOnly bool) ([]PR, error) {
	mode := ListModeReviewTeams
	if explicitReviewerOnly {
		mode = ListModeReviewExplicit
	}
	return ListPRs(ctx, mode)
}

// GetPR fetches a richer PR view (head SHA, base/head refs, review state)
// for a single PR. Use after a Ref has been obtained from search or URL
// parsing. The returned PR's ReviewState is populated when gh's pr view
// returns the review fields and ViewerLogin succeeds; if the viewer lookup
// fails the PR-wide counters are still filled (only viewer-scoped flags
// drop to zero).
func GetPR(ctx context.Context, ref Ref) (*PR, error) {
	return getPRViaGraphQL(ctx, ref)
}

// GetDiff returns the unified diff for a PR via the REST API (same bytes as
// `gh pr diff`).
func GetDiff(ctx context.Context, ref Ref) (string, error) {
	return getPullDiff(ctx, ref)
}

// CheckoutPR clones (shallow) and checks out the PR's head into dir. dir must
// not exist; CheckoutPR creates it.
func CheckoutPR(ctx context.Context, ref Ref, dir string) error {
	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", ref.Owner, ref.Repo)
	if err := runPlain(ctx, "git", "clone", "--depth", "1", "--no-tags", cloneURL, dir); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	headRef := fmt.Sprintf("pull/%d/head", ref.Number)
	if err := runPlainIn(ctx, dir, "git", "fetch", "origin", headRef); err != nil {
		return fmt.Errorf("git fetch %s: %w", headRef, err)
	}
	if err := runPlainIn(ctx, dir, "git", "checkout", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("git checkout: %w", err)
	}
	return nil
}

// PostReview posts a review (top-level body + inline comments) to GitHub.
// event should be "COMMENT" for normal review, "REQUEST_CHANGES" to block, or
// "APPROVE" to approve.
//
// On a non-2xx response the returned error is *APIError (parsed from the gh
// CLI's combined output). The reviews endpoint posts atomically: a single
// invalid inline comment causes the whole review to be rejected, so the
// caller is expected to treat the error as "nothing was posted".
func PostReview(ctx context.Context, ref Ref, review Review) error {
	if review.Event == "" {
		review.Event = "COMMENT"
	}
	body, err := json.Marshal(review)
	if err != nil {
		return fmt.Errorf("marshal review: %w", err)
	}
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", ref.Owner, ref.Repo, ref.Number)
	start := time.Now()
	err = ghAPIPost(ctx, path, body)
	applog.Info("post review", "ref", ref.String(), "event", review.Event, "inline_comments", len(review.Comments), "duration_ms", time.Since(start).Milliseconds(), "ok", err == nil)
	if err != nil {
		ae := apiErrorFrom(err, path)
		ae.CommitID = review.CommitID
		// When the review carried exactly one inline comment, attach it so
		// the diagnostic can name the failing comment. With multiple
		// comments we don't know which one the API rejected, so we leave
		// Comment nil and let the caller render gh's per-field detail.
		if len(review.Comments) == 1 {
			c := review.Comments[0]
			ae.Comment = &c
		}
		return ae
	}
	return nil
}

// pullReviewCommentInput is the JSON body for POST .../pulls/{n}/comments.
type pullReviewCommentInput struct {
	Body      string `json:"body"`
	CommitID  string `json:"commit_id"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	StartSide string `json:"start_side,omitempty"`
}

// CreatePullReviewComment posts a single inline review comment on the PR diff.
func CreatePullReviewComment(ctx context.Context, ref Ref, commitID string, c ReviewComment) error {
	if commitID == "" {
		return fmt.Errorf("empty commit id")
	}
	if c.Path == "" || c.Line == 0 {
		return fmt.Errorf("path and line are required for inline comments")
	}
	side := c.Side
	if side == "" {
		side = "RIGHT"
	}
	payload := pullReviewCommentInput{
		Body:     c.Body,
		CommitID: commitID,
		Path:     c.Path,
		Line:     c.Line,
		Side:     side,
	}
	// Multi-line comment: carry the start of the range. GitHub requires
	// start_line < line and defaults start_side to the comment's side.
	if c.StartLine > 0 && c.StartLine < c.Line {
		payload.StartLine = c.StartLine
		startSide := c.StartSide
		if startSide == "" {
			startSide = side
		}
		payload.StartSide = startSide
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal comment: %w", err)
	}
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", ref.Owner, ref.Repo, ref.Number)
	if err := ghAPIPost(ctx, apiPath, body); err != nil {
		ae := apiErrorFrom(err, apiPath)
		ae.CommitID = commitID
		echo := ReviewComment{Path: c.Path, Line: c.Line, Side: side, Body: c.Body}
		ae.Comment = &echo
		return ae
	}
	return nil
}

// pullReviewFileCommentInput is the JSON body for the file-level variant of
// POST .../pulls/{n}/comments. The "subject_type" field switches the API
// from "comment is anchored at this line in the diff" to "comment is
// attached to this file as a whole" — no line or side required, and the
// comment renders on the file header in the Files-changed tab instead of
// on a specific diff row. The enum value MUST be lowercase ("file"); the
// API rejects "FILE" despite older docs spelling it that way.
type pullReviewFileCommentInput struct {
	Body        string `json:"body"`
	CommitID    string `json:"commit_id"`
	Path        string `json:"path"`
	SubjectType string `json:"subject_type"`
}

// CreatePullReviewFileLevelComment posts a single file-level review comment
// on the PR — anchored to the file as a whole instead of to a particular
// line in the diff. This is the escape hatch the TUI offers when a
// finding's line no longer lands on any hunk (e.g. a force-push moved the
// surrounding code and the model's anchor_excerpt didn't uniquely match a
// new line either): the comment still attaches to the right file, just
// without a precise diff anchor.
//
// Failure handling mirrors CreatePullReviewComment: on a non-2xx response
// we return an *APIError parsed from gh's combined output with the failing
// comment attached as a synthetic ReviewComment (line 0, no side) so the
// caller's diagnostic-render path can name the file even though there was
// no inline anchor.
func CreatePullReviewFileLevelComment(ctx context.Context, ref Ref, commitID, path, body string) error {
	if commitID == "" {
		return fmt.Errorf("empty commit id")
	}
	if path == "" {
		return fmt.Errorf("path is required for file-level comments")
	}
	payload := pullReviewFileCommentInput{
		Body:        body,
		CommitID:    commitID,
		Path:        path,
		SubjectType: "file",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal file-level comment: %w", err)
	}
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", ref.Owner, ref.Repo, ref.Number)
	if err := ghAPIPost(ctx, apiPath, raw); err != nil {
		ae := apiErrorFrom(err, apiPath)
		ae.CommitID = commitID
		echo := ReviewComment{Path: path, Body: body}
		ae.Comment = &echo
		return ae
	}
	return nil
}

// GetPRHeadSHA returns just the current .head.sha for the PR. Used as a
// lightweight pre-flight check before posting an inline comment, so we can
// detect that the PR was force-pushed since the review was generated and
// surface a "[R] refresh PR" hint instead of letting GitHub reject the post
// with the opaque "could not be resolved" error.
func GetPRHeadSHA(ctx context.Context, ref Ref) (string, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", ref.Owner, ref.Repo, ref.Number)
	var resp struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := ghAPIGet(ctx, path, &resp); err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Head.SHA), nil
}

// PostReviewBodyOnly submits a pull request review with only the top-level body
// (no inline comments).
func PostReviewBodyOnly(ctx context.Context, ref Ref, commitID, body string) error {
	rev := Review{
		CommitID: commitID,
		Body:     body,
		Event:    "COMMENT",
	}
	return PostReview(ctx, ref, rev)
}

// ParsePRURL accepts both full URLs and the gh "owner/repo#num" shorthand.
func ParsePRURL(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("empty input")
	}

	// Shorthand: owner/repo#123
	if shortRe.MatchString(s) {
		m := shortRe.FindStringSubmatch(s)
		num, _ := strconv.Atoi(m[3])
		return Ref{Owner: m[1], Repo: m[2], Number: num}, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, fmt.Errorf("not a URL or shorthand: %w", err)
	}
	if u.Host != "github.com" && !strings.HasSuffix(u.Host, ".github.com") {
		return Ref{}, fmt.Errorf("not a github.com URL: %s", u.Host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// expected: [owner, repo, "pull", "<num>"]
	if len(parts) < 4 || parts[2] != "pull" {
		return Ref{}, fmt.Errorf("unexpected URL path: %s", u.Path)
	}
	num, err := strconv.Atoi(parts[3])
	if err != nil {
		return Ref{}, fmt.Errorf("PR number not numeric: %s", parts[3])
	}
	return Ref{Owner: parts[0], Repo: parts[1], Number: num}, nil
}

var shortRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#(\d+)$`)

func splitRepo(nameWithOwner string) (string, string) {
	parts := strings.SplitN(nameWithOwner, "/", 2)
	if len(parts) != 2 {
		return "", nameWithOwner
	}
	return parts[0], parts[1]
}

func runPlain(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

func runPlainIn(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s in %s: %s", name, dir, strings.TrimSpace(string(out)))
	}
	return nil
}
