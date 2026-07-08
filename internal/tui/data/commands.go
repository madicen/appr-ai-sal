// Package data hosts the async tea.Cmd loaders and the message types they
// emit. Splitting them out of the model package lets every consumer (root
// model + extracted tab packages such as tabs/review) react to the same
// canonical message types without each package re-declaring them.
//
// Every command selects a Backend once (see selectBackend) and then talks to
// it uniformly — demo mode and live mode share the exact same interface, so no
// command branches on demoMode internally. The message-producing bodies are
// factored into plain functions taking a Backend so the command flows can be
// unit-tested against a fake Backend without a live gh CLI.
package data

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/demo"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// prRef is the ref of a fetched PR. Commands derive the diff ref from the PR
// returned by Backend.PRDetail (rather than the requested ref) so the demo
// fixture's fallback diff aligns with its fallback PR.
func prRef(pr *gh.PR) gh.Ref {
	return gh.Ref{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number}
}

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

// LoadPRsCmd fetches the PR set for the requested ListMode (review
// queue, explicit-reviewer narrow, or authored-by-me).
//
// When demoMode is true the gh CLI is bypassed entirely and a canned
// set of PRs is returned. The demo fixture is a fixed list, so
// authored-mode just filters that fixture by the viewer's canonical
// "madicen" login — keeping the recording reproducible regardless of the
// host's gh user.
func LoadPRsCmd(mode gh.ListMode, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	return func() tea.Msg { return loadPRsMsg(b, mode) }
}

func loadPRsMsg(b Backend, mode gh.ListMode) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prs, err := b.ListPRs(ctx, mode)
	if err != nil {
		return ErrMsg{err}
	}
	return PRListMsg{PRs: prs}
}

// demoPRsForMode returns the canned PR fixture filtered to match the
// requested mode. The demo viewer is "madicen", so authored-mode
// returns only PRs with Author == "madicen".
func demoPRsForMode(mode gh.ListMode) []gh.PR {
	all := demo.DemoPullRequests()
	switch mode {
	case gh.ListModeAuthored:
		out := make([]gh.PR, 0, len(all))
		for _, pr := range all {
			if pr.Author == "madicen" {
				out = append(out, pr)
			}
		}
		return out
	case gh.ListModeReviewExplicit:
		out := make([]gh.PR, 0, len(all))
		for _, pr := range all {
			if pr.ReviewState.ViewerStillRequested {
				out = append(out, pr)
			}
		}
		return out
	default:
		return all
	}
}

// LoadPRDetailCmd fetches the PR view + unified diff for the given ref.
//
// In demo mode the canned PR fixture is looked up by ref; if the user
// pasted a ref that doesn't match a fixture the backend falls back to the
// first canned PR (and the diff is fetched against that PR's ref) so the
// URL-paste demo still has something to render.
func LoadPRDetailCmd(ref gh.Ref, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	return func() tea.Msg { return loadPRDetailMsg(b, ref) }
}

func loadPRDetailMsg(b Backend, ref gh.Ref) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, err := b.PRDetail(ctx, ref)
	if err != nil {
		return ErrMsg{err}
	}
	diff, err := b.Diff(ctx, prRef(pr))
	if err != nil {
		return ErrMsg{err}
	}
	return PRDetailMsg{PR: pr, Diff: diff}
}

// StartReviewCmd starts a review run and emits ReviewStartedMsg with the
// progress channel the caller should poll via WaitForProgressCmd.
//
// In demo mode the channel comes from demo.SyntheticReviewProgress, which
// emits a scripted sequence of stages with realistic delays so the
// review-overlay UI replays a believable run for VHS recording.
func StartReviewCmd(ref gh.Ref, cfg *aiconfig.Config, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	snap := cfg.Clone()
	return func() tea.Msg { return startReviewMsg(b, ref, snap) }
}

func startReviewMsg(b Backend, ref gh.Ref, cfg *aiconfig.Config) tea.Msg {
	ch, err := b.StartReview(context.Background(), ref, cfg)
	if err != nil {
		return ErrMsg{err}
	}
	return ReviewStartedMsg{Ch: ch}
}

// ReviewStartedMsg carries the progress channel produced by the review run.
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
	Comments []gh.PullReviewComment
	// Threads are the PR's inline review threads (with node IDs) for B3 reply
	// routing. Empty on fetch failure / demo mode → all posts top-level.
	Threads   []gh.ReviewThread
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
	b := selectBackend(demoMode)
	return func() tea.Msg { return existingCommentsMsg(b, ref) }
}

func existingCommentsMsg(b Backend, ref gh.Ref) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ec := b.ExistingComments(ctx, ref)
	return ExistingPRCommentsMsg{
		Comments:  ec.Comments,
		Threads:   ec.Threads,
		Viewer:    ec.Viewer,
		Prior:     ec.Prior,
		ListErr:   ec.ListErr,
		ViewerErr: ec.ViewerErr,
	}
}

// PostReviewCmd posts the full draft review when dryRun is false; otherwise
// emits a preview message.
//
// Demo mode forces dry-run behaviour at the CLI seam (see
// cmd/appr-ai-sal/main.go), so the demo path naturally falls through to the
// DryRunPayloadMsg branch and never touches the backend's post methods.
func PostReviewCmd(ref gh.Ref, draft *review.Draft, dryRun, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	return func() tea.Msg { return postReviewMsg(b, ref, draft, dryRun) }
}

func postReviewMsg(b Backend, ref gh.Ref, draft *review.Draft, dryRun bool) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if dryRun {
		p := review.DryRunFullReview(draft)
		return DryRunPayloadMsg{Title: p.Title, Payload: p.Payload}
	}
	// Pre-flight: detect that the PR was force-pushed since the review was
	// generated. Without this we'd let GitHub reject the inline comments
	// with the opaque "pull_request_review_thread.line could not be
	// resolved" error; with it we surface a typed *HeadDriftError that
	// the overlay turns into a clear "[R] Refresh PR" prompt.
	if drift := preflightHeadDrift(ctx, b, ref, draft.PR); drift != nil {
		return ErrMsg{drift}
	}
	if err := b.PostReview(ctx, ref, draft.ToReview()); err != nil {
		return ErrMsg{err}
	}
	return PostDoneMsg{}
}

// PostSingleFindingCmd posts one inline comment or dry-run preview. When
// threadID is non-empty the finding matched an existing unresolved review
// thread (B3), so it is posted as an in-thread reply instead of a duplicate
// top-level comment; an empty threadID keeps the historical top-level post.
func PostSingleFindingCmd(ref gh.Ref, pr *gh.PR, specialist string, f review.Finding, threadID string, dryRun, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	return func() tea.Msg { return postSingleFindingMsg(b, ref, pr, specialist, f, threadID, dryRun) }
}

func postSingleFindingMsg(b Backend, ref gh.Ref, pr *gh.PR, specialist string, f review.Finding, threadID string, dryRun bool) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// B3: reply in-thread when the finding matched an open thread's anchor.
	if strings.TrimSpace(threadID) != "" {
		if dryRun {
			p := review.DryRunThreadReply(ref, threadID, specialist, f)
			return DryRunPayloadMsg{Title: p.Title, Payload: p.Payload}
		}
		body := review.ReviewCommentBody(specialist, f)
		// A failed reply is reported exactly like a failed top-level post
		// (fail-open): the reviewer sees the error and can retry / skip.
		if err := b.ReplyToThread(ctx, ref, threadID, body); err != nil {
			return ErrMsg{err}
		}
		return StagedFindingPostedMsg{}
	}
	if dryRun {
		p := review.DryRunSingleFinding(ref, pr, specialist, f)
		return DryRunPayloadMsg{Title: p.Title, Payload: p.Payload}
	}
	if drift := preflightHeadDrift(ctx, b, ref, pr); drift != nil {
		return ErrMsg{drift}
	}
	c := review.InlineReviewComment(specialist, f)
	if err := b.PostInlineComment(ctx, ref, pr.HeadSHA, c); err != nil {
		return ErrMsg{err}
	}
	return StagedFindingPostedMsg{}
}

// PostSingleFindingFileLevelCmd posts one finding as a file-level review
// comment (subject_type=file, no line/side). The TUI reaches for this when
// the reviewer presses F on a cardError state — i.e. the finding's
// intended line isn't on a hunk in the current diff AND the AnchorExcerpt
// relocation didn't find a unique fallback line. The body adds a short
// "(intended for line N — anchored to file because that line isn't on a
// hunk in the current diff)" preamble before the usual comment so the
// reader on GitHub still sees the original line the model meant.
func PostSingleFindingFileLevelCmd(ref gh.Ref, pr *gh.PR, specialist string, f review.Finding, dryRun, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	return func() tea.Msg { return postSingleFindingFileLevelMsg(b, ref, pr, specialist, f, dryRun) }
}

func postSingleFindingFileLevelMsg(b Backend, ref gh.Ref, pr *gh.PR, specialist string, f review.Finding, dryRun bool) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if dryRun {
		p := review.DryRunFileLevelFinding(ref, pr, specialist, f)
		return DryRunPayloadMsg{Title: p.Title, Payload: p.Payload}
	}
	if drift := preflightHeadDrift(ctx, b, ref, pr); drift != nil {
		return ErrMsg{drift}
	}
	body := review.ReviewCommentBodyForFileLevel(specialist, f)
	if err := b.PostFileLevelComment(ctx, ref, pr.HeadSHA, f.Path, body); err != nil {
		return ErrMsg{err}
	}
	return StagedFindingPostedMsg{}
}

// preflightHeadDrift returns a *gh.HeadDriftError when the PR's current head
// SHA on GitHub doesn't match the SHA we cached on draft/pr (force-push,
// new commit). It returns nil if the SHAs match, if pr is nil / has no SHA,
// or if the head-SHA lookup itself fails — we'd rather attempt the post and
// report a real GitHub error than refuse to post on a transient pre-flight
// failure. The SHA comparison itself lives in gh.HeadDrift so it can be
// unit-tested without a live PR.
func preflightHeadDrift(ctx context.Context, b Backend, ref gh.Ref, pr *gh.PR) *gh.HeadDriftError {
	if pr == nil || strings.TrimSpace(pr.HeadSHA) == "" {
		return nil
	}
	cur, err := b.HeadSHA(ctx, ref)
	if err != nil {
		return nil
	}
	return gh.HeadDrift(pr.HeadSHA, cur)
}

// PostReviewWithVerdictCmd posts a body-only review using the GitHub review
// event. If event is empty, uses draft.PostEvent(). When the gh viewer is the
// PR author, verdict events are downgraded to COMMENT with a full summary body
// (GitHub rejects APPROVE / REQUEST_CHANGES on your own PR).
//
// demoMode routes through the demo backend whose ViewerLogin returns "" — the
// demo binary may run without gh on PATH, and we know the canned PRs aren't
// authored by the demo viewer, so the self-author downgrade isn't relevant.
func PostReviewWithVerdictCmd(ref gh.Ref, draft *review.Draft, dryRun, demoMode bool, event string) tea.Cmd {
	b := selectBackend(demoMode)
	return func() tea.Msg { return postReviewWithVerdictMsg(b, ref, draft, dryRun, event) }
}

func postReviewWithVerdictMsg(b Backend, ref gh.Ref, draft *review.Draft, dryRun bool, event string) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if strings.TrimSpace(event) == "" {
		event = draft.PostEvent()
	}
	viewer := b.ViewerLogin(ctx)
	event, body, intent := review.EffectiveReviewEventAndBody(draft, event, viewer)
	if dryRun {
		p := review.DryRunVerdictReview(ref, draft.PR.HeadSHA, event, intent, body)
		return DryRunPayloadMsg{Title: p.Title, Payload: p.Payload}
	}
	if drift := preflightHeadDrift(ctx, b, ref, draft.PR); drift != nil {
		return ErrMsg{drift}
	}
	rev := gh.Review{
		CommitID: draft.PR.HeadSHA,
		Body:     body,
		Event:    event,
	}
	if err := b.PostReview(ctx, ref, rev); err != nil {
		return ErrMsg{err}
	}
	return PostDoneMsg{}
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
	b := selectBackend(demoMode)
	return func() tea.Msg { return postApproveBareMsg(b, ref, draft, dryRun) }
}

func postApproveBareMsg(b Backend, ref gh.Ref, draft *review.Draft, dryRun bool) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	viewer := b.ViewerLogin(ctx)
	event, body, intent := review.EffectiveApproveBareEventAndBody(draft, viewer)
	if dryRun {
		p := review.DryRunApproveBare(ref, draft.PR.HeadSHA, event, intent, body)
		return DryRunPayloadMsg{Title: p.Title, Payload: p.Payload}
	}
	if drift := preflightHeadDrift(ctx, b, ref, draft.PR); drift != nil {
		return ErrMsg{drift}
	}
	rev := gh.Review{
		CommitID: draft.PR.HeadSHA,
		Body:     body,
		Event:    event,
	}
	if err := b.PostReview(ctx, ref, rev); err != nil {
		return ErrMsg{err}
	}
	return PostDoneMsg{}
}

// StatusRepliesPostedMsg reports the outcome of the B3 re-run status replies:
// how many of the tool's own prior review threads got a "resolved" /
// "still present" status update and how many failed. Fail-open — a failed
// reply is counted, never fatal.
type StatusRepliesPostedMsg struct {
	Posted int
	Failed int
}

// PostStatusRepliesCmd posts the re-run status replies (B3.2) on the tool's own
// prior review threads. It is a no-op (empty StatusRepliesPostedMsg) unless the
// draft carries a prior cached review (i.e. this is a re-review) — so on a
// first review nothing is posted. Callers gate this behind a REAL post
// (not dry-run / not demo); the demo backend's ReplyToThread is a no-op anyway.
func PostStatusRepliesCmd(ref gh.Ref, draft *review.Draft, threads []gh.ReviewThread, viewer string, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	snap := append([]gh.ReviewThread(nil), threads...)
	return func() tea.Msg { return postStatusRepliesMsg(b, ref, draft, snap, viewer) }
}

func postStatusRepliesMsg(b Backend, ref gh.Ref, draft *review.Draft, threads []gh.ReviewThread, viewer string) tea.Msg {
	if draft == nil || draft.PriorReview == nil || draft.PR == nil {
		return StatusRepliesPostedMsg{}
	}
	replies := review.BuildStatusReplies(draft.PriorReview, draft.Diff, threads, viewer, draft.PR.HeadSHA)
	if len(replies) == 0 {
		return StatusRepliesPostedMsg{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var posted, failed int
	for _, r := range replies {
		if err := b.ReplyToThread(ctx, ref, r.ThreadID, r.Body); err != nil {
			failed++
			continue
		}
		posted++
	}
	return StatusRepliesPostedMsg{Posted: posted, Failed: failed}
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
	b := selectBackend(demoMode)
	return func() tea.Msg { return loadChecksMsg(b, ref) }
}

func loadChecksMsg(b Backend, ref gh.Ref) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, err := b.Checks(ctx, ref)
	if err != nil {
		return ChecksMsg{Ref: ref, Err: err}
	}
	return ChecksMsg{Ref: ref, Report: report}
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
	b := selectBackend(demoMode)
	return func() tea.Msg { return loadDiscussionMsg(b, ref) }
}

func loadDiscussionMsg(b Backend, ref gh.Ref) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timeline, err := b.Discussion(ctx, ref)
	if err != nil {
		return DiscussionMsg{Ref: ref, Err: err}
	}
	return DiscussionMsg{Ref: ref, Timeline: timeline}
}

// RefreshPRCmd re-fetches the PR view (head SHA in particular) and the unified
// diff. Bound to "R" in the persistent review overlay so the user can recover
// from "PR head moved" or "line could not be resolved" errors without leaving
// the approval flow.
//
// In demo mode the canned PR + diff are returned so the "R" key still has a
// visible effect during recordings.
func RefreshPRCmd(ref gh.Ref, demoMode bool) tea.Cmd {
	b := selectBackend(demoMode)
	return func() tea.Msg { return refreshPRMsg(b, ref) }
}

func refreshPRMsg(b Backend, ref gh.Ref) tea.Msg {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, err := b.PRDetail(ctx, ref)
	if err != nil {
		return ErrMsg{fmt.Errorf("refresh PR: %w", err)}
	}
	diff, err := b.Diff(ctx, prRef(pr))
	if err != nil {
		return ErrMsg{fmt.Errorf("refresh PR diff: %w", err)}
	}
	return PRRefreshedMsg{PR: pr, Diff: diff}
}
