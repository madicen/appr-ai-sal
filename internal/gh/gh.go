// Package gh wraps the gh CLI to avoid a separate auth surface. Anything that
// needs the GitHub API runs through `gh` so we inherit the user's gh login.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
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

// ReviewComment is a single inline review comment.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side,omitempty"` // LEFT (old) or RIGHT (new); default RIGHT
	Body string `json:"body"`
}

// CheckAuth returns nil if gh is installed and the user is logged in.
func CheckAuth() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh not found on PATH; install from https://cli.github.com")
	}
	cmd := exec.Command("gh", "auth", "status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh auth status: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ViewerLogin returns the GitHub login for the authenticated gh user.
func ViewerLogin(ctx context.Context) (string, error) {
	out, err := run(ctx, []string{"api", "user", "-q", ".login"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// IsUserExplicitlyRequested returns true if login appears in the PR's
// requested_reviewers (not only via a requested team).
func IsUserExplicitlyRequested(ctx context.Context, pr PR, login string) (bool, error) {
	if login == "" {
		return false, nil
	}
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number)
	out, err := run(ctx, []string{"api", path, "-q", ".requested_reviewers[].login"})
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == login {
			return true, nil
		}
	}
	return false, nil
}

// ListReviewRequestedPRs returns open PRs where the authenticated user has
// been requested as a reviewer (including via team membership in GitHub search).
// When explicitReviewerOnly is true, results are restricted to PRs where the
// viewer's login appears directly in reviewRequests (not only via a team).
//
// In addition to the basic PR metadata, each returned PR carries a populated
// ReviewState (overall reviewDecision, approval counts, and viewer-relative
// flags) used by the TUI to render badges and sort by actionability.
func ListReviewRequestedPRs(ctx context.Context, explicitReviewerOnly bool) ([]PR, error) {
	out, err := runGraphQL(ctx, graphqlReviewQuery, map[string]string{
		"q": "is:pr is:open review-requested:@me archived:false",
	})
	if err != nil {
		return nil, err
	}
	prs, _, err := parseReviewSearchResponse(out)
	if err != nil {
		return nil, err
	}
	if !explicitReviewerOnly {
		return prs, nil
	}
	filtered := make([]PR, 0, len(prs))
	for _, pr := range prs {
		if pr.ReviewState.ViewerStillRequested {
			filtered = append(filtered, pr)
		}
	}
	return filtered, nil
}

// GetPR fetches a richer PR view (head SHA, base/head refs, review state)
// for a single PR. Use after a Ref has been obtained from search or URL
// parsing. The returned PR's ReviewState is populated when gh's pr view
// returns the review fields and ViewerLogin succeeds; if the viewer lookup
// fails the PR-wide counters are still filled (only viewer-scoped flags
// drop to zero).
func GetPR(ctx context.Context, ref Ref) (*PR, error) {
	args := []string{
		"pr", "view", strconv.Itoa(ref.Number),
		"--repo", ref.Owner + "/" + ref.Repo,
		"--json", "number,title,url,body,author,headRefName,headRefOid,baseRefName,isDraft,createdAt,updatedAt,reviewDecision,latestReviews,reviewRequests",
	}
	out, err := runJSON(ctx, args)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Number      int       `json:"number"`
		Title       string    `json:"title"`
		URL         string    `json:"url"`
		Body        string    `json:"body"`
		HeadRefName string    `json:"headRefName"`
		HeadRefOid  string    `json:"headRefOid"`
		BaseRefName string    `json:"baseRefName"`
		IsDraft     bool      `json:"isDraft"`
		CreatedAt   time.Time `json:"createdAt"`
		UpdatedAt   time.Time `json:"updatedAt"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
		ReviewDecision string `json:"reviewDecision"`
		LatestReviews  []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			State string `json:"state"`
		} `json:"latestReviews"`
		ReviewRequests []struct {
			Typename string `json:"__typename"`
			Login    string `json:"login"`
			Slug     string `json:"slug"`
		} `json:"reviewRequests"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse pr view output: %w", err)
	}
	// Best-effort viewer lookup; failure leaves viewer-scoped flags zeroed.
	viewer, _ := ViewerLogin(ctx)
	latest := make([]LatestReview, 0, len(raw.LatestReviews))
	for _, lr := range raw.LatestReviews {
		latest = append(latest, LatestReview{
			AuthorLogin: lr.Author.Login,
			State:       lr.State,
		})
	}
	requests := make([]ReviewRequest, 0, len(raw.ReviewRequests))
	for _, rr := range raw.ReviewRequests {
		switch rr.Typename {
		case "User":
			requests = append(requests, ReviewRequest{Login: rr.Login})
		case "Team":
			requests = append(requests, ReviewRequest{TeamSlug: rr.Slug})
		}
	}
	return &PR{
		Number:      raw.Number,
		Title:       raw.Title,
		URL:         raw.URL,
		Body:        raw.Body,
		Repository:  ref.Owner + "/" + ref.Repo,
		Owner:       ref.Owner,
		Repo:        ref.Repo,
		Author:      raw.Author.Login,
		BaseRef:     raw.BaseRefName,
		HeadRef:     raw.HeadRefName,
		HeadSHA:     raw.HeadRefOid,
		IsDraft:     raw.IsDraft,
		CreatedAt:   raw.CreatedAt,
		UpdatedAt:   raw.UpdatedAt,
		ReviewState: DeriveReviewState(viewer, raw.ReviewDecision, latest, requests),
	}, nil
}

// GetDiff returns the unified diff for a PR, exactly as `gh pr diff` produces.
func GetDiff(ctx context.Context, ref Ref) (string, error) {
	args := []string{"pr", "diff", strconv.Itoa(ref.Number), "--repo", ref.Owner + "/" + ref.Repo}
	out, err := run(ctx, args)
	if err != nil {
		return "", err
	}
	return string(out), nil
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
	cmd := exec.CommandContext(ctx, "gh", "api", path, "--method", "POST", "--input", "-")
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		ae := parseGHError(out, path)
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
	Body     string `json:"body"`
	CommitID string `json:"commit_id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Side     string `json:"side,omitempty"`
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
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal comment: %w", err)
	}
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", ref.Owner, ref.Repo, ref.Number)
	cmd := exec.CommandContext(ctx, "gh", "api", apiPath, "--method", "POST", "--input", "-")
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		ae := parseGHError(out, apiPath)
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
	cmd := exec.CommandContext(ctx, "gh", "api", apiPath, "--method", "POST", "--input", "-")
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		ae := parseGHError(out, apiPath)
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
	out, err := run(ctx, []string{"api", path, "-q", ".head.sha"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

// runJSON runs `gh <args...>`, returns stdout. Used for --json-flagged calls.
func runJSON(ctx context.Context, args []string) ([]byte, error) {
	return run(ctx, args)
}

// run executes gh with the given args and returns stdout, or an error
// containing stderr on failure.
func run(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
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
