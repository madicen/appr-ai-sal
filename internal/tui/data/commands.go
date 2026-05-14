// Package data hosts the async tea.Cmd loaders and the message types they
// emit. Splitting them out of the model package lets every consumer (root
// model + extracted tab packages such as tabs/review) react to the same
// canonical message types without each package re-declaring them.
package data

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// PRListMsg delivers the result of LoadPRsCmd.
type PRListMsg struct{ PRs []gh.PR }

// PRDetailMsg delivers the result of LoadPRDetailCmd: the PR view + unified diff.
type PRDetailMsg struct {
	PR   *gh.PR
	Diff string
}

// ProgressMsg wraps a single review.Progress event coming off the runner channel.
type ProgressMsg review.Progress

// PostDoneMsg signals a successful PostReviewCmd / PostReviewWithVerdictCmd.
type PostDoneMsg struct{}

// ErrMsg is the canonical error envelope emitted by every async cmd.
type ErrMsg struct{ Err error }

// Error satisfies the error interface so callers can treat ErrMsg uniformly.
func (e ErrMsg) Error() string { return e.Err.Error() }

// LoadPRsCmd fetches review-requested PRs, optionally filtered to explicit user requests.
func LoadPRsCmd(explicitReviewerOnly bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		prs, err := gh.ListReviewRequestedPRs(ctx, explicitReviewerOnly)
		if err != nil {
			return ErrMsg{err}
		}
		return PRListMsg{PRs: prs}
	}
}

// LoadPRDetailCmd fetches the PR view + unified diff for the given ref.
func LoadPRDetailCmd(ref gh.Ref) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		pr, err := gh.GetPR(ctx, ref)
		if err != nil {
			return ErrMsg{err}
		}
		diff, err := gh.GetDiff(ctx, ref)
		if err != nil {
			return ErrMsg{err}
		}
		return PRDetailMsg{PR: pr, Diff: diff}
	}
}

// StartReviewCmd starts a review.Run goroutine and emits ReviewStartedMsg with
// the progress channel the caller should poll via WaitForProgressCmd.
func StartReviewCmd(ref gh.Ref, cfg *aiconfig.Config) tea.Cmd {
	snap := cfg.Clone()
	return func() tea.Msg {
		ctx := context.Background()
		ch, err := review.Run(ctx, ref, snap)
		if err != nil {
			return ErrMsg{err}
		}
		return ReviewStartedMsg{Ch: ch}
	}
}

// ReviewStartedMsg carries the progress channel produced by review.Run.
type ReviewStartedMsg struct {
	Ch <-chan review.Progress
}

// WaitForProgressCmd reads one progress event off the channel. When the channel
// closes it emits ReviewClosedMsg so the caller can finalise; otherwise it
// emits ProgressMsg(p).
func WaitForProgressCmd(ch <-chan review.Progress) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return ReviewClosedMsg{}
		}
		return ProgressMsg(p)
	}
}

// ReviewClosedMsg signals that the review goroutine's progress channel closed.
type ReviewClosedMsg struct{}

// ExistingPRCommentsMsg delivers inline comments already on the PR (for
// duplicate detection) along with a summary of any prior appr-ai-sal
// activity on this PR (for the "tool has reviewed this before" banner).
type ExistingPRCommentsMsg struct {
	Comments  []gh.PullReviewComment
	Viewer    string
	Prior     gh.PriorAprrAISalActivity
	ListErr   error
	ViewerErr error
}

// FetchExistingPRCommentsCmd fetches inline comments + viewer login for prior-
// activity detection.
func FetchExistingPRCommentsCmd(ref gh.Ref) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		comments, cerr := gh.ListPullReviewComments(ctx, ref)
		viewer, verr := gh.ViewerLogin(ctx)
		// Reviews are fetched best-effort — failure here just means the
		// "tool has reviewed this before" banner won't include the
		// review-body count, not a blocking error.
		reviews, _ := gh.ListPullReviews(ctx, ref.Owner, ref.Repo, ref.Number, 30)
		prior := gh.DetectPriorAprrAISalActivityFrom(comments, reviews, viewer)
		return ExistingPRCommentsMsg{
			Comments:  comments,
			Viewer:    viewer,
			Prior:     prior,
			ListErr:   cerr,
			ViewerErr: verr,
		}
	}
}

// PostReviewCmd posts the full draft review when dryRun is false; otherwise
// emits a preview message.
func PostReviewCmd(ref gh.Ref, draft *review.Draft, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rev := draft.ToReview()
		if dryRun {
			b, _ := json.MarshalIndent(rev, "", "  ")
			return DryRunPayloadMsg{
				Title:   "Dry-run: full review payload (not posted)",
				Payload: string(b),
			}
		}
		// Pre-flight: detect that the PR was force-pushed since the review was
		// generated. Without this we'd let GitHub reject the inline comments
		// with the opaque "pull_request_review_thread.line could not be
		// resolved" error; with it we surface a typed *HeadDriftError that
		// the overlay turns into a clear "[R] Refresh PR" prompt.
		if drift := preflightHeadDrift(ctx, ref, draft.PR); drift != nil {
			return ErrMsg{drift}
		}
		if err := gh.PostReview(ctx, ref, rev); err != nil {
			return ErrMsg{err}
		}
		return PostDoneMsg{}
	}
}

// PostSingleFindingCmd posts one inline comment or dry-run preview.
func PostSingleFindingCmd(ref gh.Ref, pr *gh.PR, specialist string, f review.Finding, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := review.ReviewCommentBody(specialist, f)
		side := f.Side
		if side == "" {
			side = "RIGHT"
		}
		c := gh.ReviewComment{
			Path: f.Path,
			Line: f.Line,
			Side: side,
			Body: body,
		}
		if dryRun {
			preview := fmt.Sprintf("POST %s/%s/pulls/%d/comments\n\n%s",
				ref.Owner, ref.Repo, ref.Number, prettyJSON(struct {
					Body     string `json:"body"`
					CommitID string `json:"commit_id"`
					Path     string `json:"path"`
					Line     int    `json:"line"`
					Side     string `json:"side"`
				}{Body: body, CommitID: pr.HeadSHA, Path: f.Path, Line: f.Line, Side: side}))
			return DryRunPayloadMsg{Title: "Dry-run: single comment (not posted)", Payload: preview}
		}
		if drift := preflightHeadDrift(ctx, ref, pr); drift != nil {
			return ErrMsg{drift}
		}
		if err := gh.CreatePullReviewComment(ctx, ref, pr.HeadSHA, c); err != nil {
			return ErrMsg{err}
		}
		return StagedFindingPostedMsg{}
	}
}

// PostSingleFindingFileLevelCmd posts one finding as a file-level review
// comment (subject_type=file, no line/side). The TUI reaches for this when
// the reviewer presses F on a cardError state — i.e. the finding's
// intended line isn't on a hunk in the current diff AND the AnchorExcerpt
// relocation didn't find a unique fallback line. The body adds a short
// "(intended for line N — anchored to file because that line isn't on a
// hunk in the current diff)" preamble before the usual comment so the
// reader on GitHub still sees the original line the model meant.
func PostSingleFindingFileLevelCmd(ref gh.Ref, pr *gh.PR, specialist string, f review.Finding, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := review.ReviewCommentBodyForFileLevel(specialist, f)
		if dryRun {
			preview := fmt.Sprintf("POST %s/%s/pulls/%d/comments (file-level)\n\n%s",
				ref.Owner, ref.Repo, ref.Number, prettyJSON(struct {
					Body        string `json:"body"`
					CommitID    string `json:"commit_id"`
					Path        string `json:"path"`
					SubjectType string `json:"subject_type"`
				}{Body: body, CommitID: pr.HeadSHA, Path: f.Path, SubjectType: "file"}))
			return DryRunPayloadMsg{Title: "Dry-run: single file-level comment (not posted)", Payload: preview}
		}
		if drift := preflightHeadDrift(ctx, ref, pr); drift != nil {
			return ErrMsg{drift}
		}
		if err := gh.CreatePullReviewFileLevelComment(ctx, ref, pr.HeadSHA, f.Path, body); err != nil {
			return ErrMsg{err}
		}
		return StagedFindingPostedMsg{}
	}
}

// preflightHeadDrift returns a *gh.HeadDriftError when the PR's current head
// SHA on GitHub doesn't match the SHA we cached on draft/pr (force-push,
// new commit). It returns nil if the SHAs match, if pr is nil, or if the
// preflight call itself fails — we'd rather attempt the post and report a
// real GitHub error than refuse to post on a transient pre-flight failure.
func preflightHeadDrift(ctx context.Context, ref gh.Ref, pr *gh.PR) error {
	if pr == nil || strings.TrimSpace(pr.HeadSHA) == "" {
		return nil
	}
	cur, err := gh.GetPRHeadSHA(ctx, ref)
	if err != nil || cur == "" {
		return nil
	}
	if cur == pr.HeadSHA {
		return nil
	}
	return &gh.HeadDriftError{Was: pr.HeadSHA, Now: cur}
}

// PostReviewWithVerdictCmd posts a body-only review using the GitHub review
// event. If event is empty, uses draft.PostEvent(). When the gh viewer is the
// PR author, verdict events are downgraded to COMMENT with a full summary body
// (GitHub rejects APPROVE / REQUEST_CHANGES on your own PR).
func PostReviewWithVerdictCmd(ref gh.Ref, draft *review.Draft, dryRun bool, event string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if strings.TrimSpace(event) == "" {
			event = draft.PostEvent()
		}
		viewer, _ := gh.ViewerLogin(ctx)
		event, body, intent := review.EffectiveReviewEventAndBody(draft, event, viewer)
		if dryRun {
			preview := fmt.Sprintf("POST %s/%s/pulls/%d/reviews (verdict event=%s)\n",
				ref.Owner, ref.Repo, ref.Number, event)
			if intent != event {
				preview += fmt.Sprintf("NOTE: You are the PR author — GitHub rejects event=%s; posting as %s.\n", intent, event)
			}
			preview += "\n" + prettyJSON(struct {
				Body     string `json:"body"`
				Event    string `json:"event"`
				CommitID string `json:"commit_id"`
			}{Body: body, Event: event, CommitID: draft.PR.HeadSHA})
			title := "Dry-run: " + event + " review (not posted)"
			if intent != event {
				title = fmt.Sprintf("Dry-run: %s review (own PR: cannot submit %s; summary as comment) (not posted)", event, intent)
			}
			return DryRunPayloadMsg{Title: title, Payload: preview}
		}
		rev := gh.Review{
			CommitID: draft.PR.HeadSHA,
			Body:     body,
			Event:    event,
		}
		if drift := preflightHeadDrift(ctx, ref, draft.PR); drift != nil {
			return ErrMsg{drift}
		}
		if err := gh.PostReview(ctx, ref, rev); err != nil {
			return ErrMsg{err}
		}
		return PostDoneMsg{}
	}
}

// DryRunPayloadMsg is emitted by every Post*Cmd path when dry-run is enabled,
// carrying a human-readable preview the caller can render in a modal.
type DryRunPayloadMsg struct {
	Title   string
	Payload string
}

// StagedFindingPostedMsg signals that a single inline finding was posted
// (real or dry-run), letting the approval flow advance to the next card.
type StagedFindingPostedMsg struct{}

// PRRefreshedMsg is emitted by RefreshPRCmd when a fresh PR view + diff has
// been fetched from GitHub. The root model uses it to update m.currentPR /
// m.diff / m.parsedDiff and forwards it to the persistent review overlay so
// approval cards can re-anchor against the new diff before the user retries
// a post.
type PRRefreshedMsg struct {
	PR   *gh.PR
	Diff string
}

// RefreshPRCmd re-fetches the PR view (head SHA in particular) and the unified
// diff. Bound to "R" in the persistent review overlay so the user can recover
// from "PR head moved" or "line could not be resolved" errors without leaving
// the approval flow.
func RefreshPRCmd(ref gh.Ref) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pr, err := gh.GetPR(ctx, ref)
		if err != nil {
			return ErrMsg{fmt.Errorf("refresh PR: %w", err)}
		}
		diff, err := gh.GetDiff(ctx, ref)
		if err != nil {
			return ErrMsg{fmt.Errorf("refresh PR diff: %w", err)}
		}
		return PRRefreshedMsg{PR: pr, Diff: diff}
	}
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
