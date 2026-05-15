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
	"github.com/madicen/appr-ai-sal/internal/demo"
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
//
// When demoMode is true the gh CLI is bypassed entirely and a canned set
// of PRs is returned synchronously. This is the path VHS uses to record
// reproducible README GIFs without touching the user's gh credentials.
func LoadPRsCmd(explicitReviewerOnly, demoMode bool) tea.Cmd {
	if demoMode {
		return func() tea.Msg { return PRListMsg{PRs: demo.DemoPullRequests()} }
	}
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
//
// In demo mode the canned PR fixture is looked up by ref; if the user
// pasted a ref that doesn't match a fixture we fall back to the first
// canned PR so the URL-paste demo still has something to render.
func LoadPRDetailCmd(ref gh.Ref, demoMode bool) tea.Cmd {
	if demoMode {
		return func() tea.Msg {
			pr := demo.LookupPR(ref)
			if pr == nil {
				fallback := demo.DemoPullRequests()[0]
				pr = &fallback
				ref = gh.Ref{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number}
			}
			return PRDetailMsg{PR: pr, Diff: demo.DemoDiff(ref)}
		}
	}
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
//
// In demo mode the channel comes from demo.SyntheticReviewProgress, which
// emits a scripted sequence of stages with realistic delays so the
// review-overlay UI replays a believable run for VHS recording.
func StartReviewCmd(ref gh.Ref, cfg *aiconfig.Config, demoMode bool) tea.Cmd {
	snap := cfg.Clone()
	if demoMode {
		return func() tea.Msg {
			ch := demo.SyntheticReviewProgress(context.Background(), ref, snap)
			return ReviewStartedMsg{Ch: ch}
		}
	}
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
//
// In demo mode we return a clean slate (no prior activity) so each
// recording starts with the same banner state regardless of what the
// host's gh user happens to look like.
func FetchExistingPRCommentsCmd(ref gh.Ref, demoMode bool) tea.Cmd {
	if demoMode {
		return func() tea.Msg {
			comments, viewer, prior := demo.DemoExistingComments(ref)
			return ExistingPRCommentsMsg{
				Comments: comments,
				Viewer:   viewer,
				Prior:    prior,
			}
		}
	}
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
//
// Demo mode forces dry-run behaviour at the CLI seam (see
// cmd/appr-ai-sal/main.go), so this function takes the same dryRun
// argument it always did and the demo path naturally falls through to
// the DryRunPayloadMsg branch below — no demo bool needed here.
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
//
// demoMode skips the gh.ViewerLogin shell-out — the demo binary may run
// without gh on PATH, and we know the canned PRs aren't authored by the
// demo viewer, so the self-author downgrade isn't relevant.
func PostReviewWithVerdictCmd(ref gh.Ref, draft *review.Draft, dryRun, demoMode bool, event string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if strings.TrimSpace(event) == "" {
			event = draft.PostEvent()
		}
		var viewer string
		if !demoMode {
			viewer, _ = gh.ViewerLogin(ctx)
		}
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

// PostApproveBareCmd posts a content-free GitHub APPROVE — event=APPROVE with
// an explicit empty body. It is the "Approve only" path from the no-findings
// auto-approve confirmation, where the default would otherwise attach the
// rendered "no issues found by any agent" summary body. The reviewer may want
// to submit the approval without publishing any review text at all; this
// command is that escape hatch.
//
// The self-author downgrade still applies — GitHub rejects APPROVE on your own
// PR, so the post is coerced to event=COMMENT with just an explanatory note as
// the body (no full rendered summary, since the reviewer asked for no body).
func PostApproveBareCmd(ref gh.Ref, draft *review.Draft, dryRun, demoMode bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var viewer string
		if !demoMode {
			viewer, _ = gh.ViewerLogin(ctx)
		}
		event, body, intent := review.EffectiveApproveBareEventAndBody(draft, viewer)
		if dryRun {
			preview := fmt.Sprintf("POST %s/%s/pulls/%d/reviews (verdict event=%s, approve-only)\n",
				ref.Owner, ref.Repo, ref.Number, event)
			if intent != event {
				preview += fmt.Sprintf("NOTE: You are the PR author — GitHub rejects event=%s; posting as %s.\n", intent, event)
			}
			preview += "\n" + prettyJSON(struct {
				Body     string `json:"body"`
				Event    string `json:"event"`
				CommitID string `json:"commit_id"`
			}{Body: body, Event: event, CommitID: draft.PR.HeadSHA})
			title := "Dry-run: " + event + " review · approve only (not posted)"
			if intent != event {
				title = fmt.Sprintf("Dry-run: %s review (own PR: cannot submit %s; note-only comment) (not posted)", event, intent)
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

// ChecksMsg delivers the result of LoadChecksCmd: the head commit's check
// rollup + per-run detail. Err is set when GitHub rejected the request /
// the gh CLI failed; the renderer surfaces a retry chip in that case.
type ChecksMsg struct {
	Ref    gh.Ref
	Report *gh.ChecksReport
	Err    error
}

// LoadChecksCmd fetches the PR's status-check rollup with per-run detail.
// Fired lazily the first time the user lands on the Checks overview row;
// the root model caches the resulting report so subsequent visits are
// instant.
func LoadChecksCmd(ref gh.Ref, demoMode bool) tea.Cmd {
	if demoMode {
		return func() tea.Msg {
			return ChecksMsg{Ref: ref, Report: demo.DemoChecks(ref)}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		report, err := gh.GetChecks(ctx, ref)
		if err != nil {
			return ChecksMsg{Ref: ref, Err: err}
		}
		return ChecksMsg{Ref: ref, Report: report}
	}
}

// DiscussionMsg delivers the result of LoadDiscussionCmd: the merged issue
// comments + review-summary bodies for the PR's Conversation tab equivalent.
type DiscussionMsg struct {
	Ref      gh.Ref
	Timeline []gh.DiscussionEvent
	Err      error
}

// LoadDiscussionCmd fetches the PR's conversation timeline. Like
// LoadChecksCmd it is fired lazily on first visit and cached on the root
// model; the demo path returns the canned timeline synchronously.
func LoadDiscussionCmd(ref gh.Ref, demoMode bool) tea.Cmd {
	if demoMode {
		return func() tea.Msg {
			return DiscussionMsg{Ref: ref, Timeline: demo.DemoDiscussion(ref)}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		timeline, err := gh.GetDiscussion(ctx, ref)
		if err != nil {
			return DiscussionMsg{Ref: ref, Err: err}
		}
		return DiscussionMsg{Ref: ref, Timeline: timeline}
	}
}

// RefreshPRCmd re-fetches the PR view (head SHA in particular) and the unified
// diff. Bound to "R" in the persistent review overlay so the user can recover
// from "PR head moved" or "line could not be resolved" errors without leaving
// the approval flow.
//
// In demo mode the canned PR + diff are returned synchronously so the
// "R" key still has a visible effect during recordings.
func RefreshPRCmd(ref gh.Ref, demoMode bool) tea.Cmd {
	if demoMode {
		return func() tea.Msg {
			pr := demo.LookupPR(ref)
			if pr == nil {
				fallback := demo.DemoPullRequests()[0]
				pr = &fallback
				ref = gh.Ref{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number}
			}
			return PRRefreshedMsg{PR: pr, Diff: demo.DemoDiff(ref)}
		}
	}
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
