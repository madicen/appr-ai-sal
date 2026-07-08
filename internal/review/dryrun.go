package review

import (
	"encoding/json"
	"fmt"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// DryRunPayload is a rendered preview of a GitHub post that would be sent when
// dry-run is disabled, split into a modal title and a human-readable payload
// body. The TUI wraps it in a modal; the headless CLI can print it verbatim.
// Extracting these builders out of the tea.Cmd closures makes the payload
// assembly unit-testable and shared between the two front-ends.
type DryRunPayload struct {
	Title   string
	Payload string
}

// InlineReviewComment assembles the GitHub inline review comment for a single
// finding: the disclosed comment body plus a side defaulting to RIGHT. It is
// the single source of truth for turning a (specialist, finding) pair into the
// wire payload, used by both the real post and the dry-run preview.
func InlineReviewComment(specialist string, f Finding) gh.ReviewComment {
	side := f.Side
	if side == "" {
		side = "RIGHT"
	}
	c := gh.ReviewComment{
		Path: f.Path,
		Line: f.Line,
		Side: side,
		Body: ReviewCommentBody(specialist, f),
	}
	// Multi-line finding (Q6.1): carry the range start so GitHub renders a
	// multi-line comment and applies the suggestion across StartLine..Line.
	// The gate (validateMultiLineSuggestionRange) guarantees StartLine < Line
	// and both anchorable when it survives to here.
	if f.StartLine > 0 && f.StartLine < f.Line {
		c.StartLine = f.StartLine
		c.StartSide = side
	}
	return c
}

// DryRunFullReview renders the preview for posting the entire draft review
// (the legacy "post the whole review" path). It mirrors PostReviewCmd's
// dry-run branch: the indented JSON of draft.ToReview().
func DryRunFullReview(d *Draft) DryRunPayload {
	rev := d.ToReview()
	b, _ := json.MarshalIndent(rev, "", "  ")
	return DryRunPayload{
		Title:   "Dry-run: full review payload (not posted)",
		Payload: string(b),
	}
}

// DryRunSingleFinding renders the preview for posting one finding as an inline
// review comment.
func DryRunSingleFinding(ref gh.Ref, pr *gh.PR, specialist string, f Finding) DryRunPayload {
	c := InlineReviewComment(specialist, f)
	preview := fmt.Sprintf("POST %s/%s/pulls/%d/comments\n\n%s",
		ref.Owner, ref.Repo, ref.Number, prettyJSON(struct {
			Body      string `json:"body"`
			CommitID  string `json:"commit_id"`
			Path      string `json:"path"`
			Line      int    `json:"line"`
			Side      string `json:"side"`
			StartLine int    `json:"start_line,omitempty"`
			StartSide string `json:"start_side,omitempty"`
		}{Body: c.Body, CommitID: pr.HeadSHA, Path: c.Path, Line: c.Line, Side: c.Side, StartLine: c.StartLine, StartSide: c.StartSide}))
	return DryRunPayload{Title: "Dry-run: single comment (not posted)", Payload: preview}
}

// DryRunThreadReply renders the preview for posting one finding as an
// in-thread reply to an existing unresolved review thread (B3) instead of a
// new top-level inline comment. The title and payload disclose the "↳ reply to
// existing thread" routing so the dry-run reader sees the difference from a
// fresh comment. The body is the same disclosed comment body a top-level post
// would carry.
func DryRunThreadReply(ref gh.Ref, threadID, specialist string, f Finding) DryRunPayload {
	body := ReviewCommentBody(specialist, f)
	loc := f.Path
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
	}
	preview := fmt.Sprintf("↳ reply to existing review thread %s (anchor %s)\naddPullRequestReviewThreadReply on %s/%s#%d\n\n%s",
		threadID, loc, ref.Owner, ref.Repo, ref.Number, prettyJSON(struct {
			ThreadID string `json:"pullRequestReviewThreadId"`
			Body     string `json:"body"`
		}{ThreadID: threadID, Body: body}))
	return DryRunPayload{Title: "Dry-run: ↳ reply to existing thread (not posted)", Payload: preview}
}

// DryRunStatusReply renders the preview for one re-run status reply (B3.2) —
// the "resolved" / "still present" note the tool leaves on its own prior
// thread. Used by the headless CLI / dry-run so the reviewer sees which
// threads would get a status update.
func DryRunStatusReply(ref gh.Ref, r StatusReply) DryRunPayload {
	status := "still present"
	if r.Status == StatusResolved {
		status = "resolved"
	}
	loc := r.Path
	if r.Line > 0 {
		loc = fmt.Sprintf("%s:%d", r.Path, r.Line)
	}
	preview := fmt.Sprintf("↳ status reply (%s) to own thread %s (anchor %s)\naddPullRequestReviewThreadReply on %s/%s#%d\n\n%s",
		status, r.ThreadID, loc, ref.Owner, ref.Repo, ref.Number, prettyJSON(struct {
			ThreadID string `json:"pullRequestReviewThreadId"`
			Body     string `json:"body"`
		}{ThreadID: r.ThreadID, Body: r.Body}))
	return DryRunPayload{Title: "Dry-run: ↳ status reply to prior thread (not posted)", Payload: preview}
}

// DryRunFileLevelFinding renders the preview for posting one finding as a
// file-level (subject_type=file) review comment — the fallback the reviewer
// reaches for when a finding's line isn't on a hunk in the current diff.
func DryRunFileLevelFinding(ref gh.Ref, pr *gh.PR, specialist string, f Finding) DryRunPayload {
	body := ReviewCommentBodyForFileLevel(specialist, f)
	preview := fmt.Sprintf("POST %s/%s/pulls/%d/comments (file-level)\n\n%s",
		ref.Owner, ref.Repo, ref.Number, prettyJSON(struct {
			Body        string `json:"body"`
			CommitID    string `json:"commit_id"`
			Path        string `json:"path"`
			SubjectType string `json:"subject_type"`
		}{Body: body, CommitID: pr.HeadSHA, Path: f.Path, SubjectType: "file"}))
	return DryRunPayload{Title: "Dry-run: single file-level comment (not posted)", Payload: preview}
}

// DryRunVerdictReview renders the preview for posting a body-only review with a
// verdict event (REQUEST_CHANGES / COMMENT / APPROVE). event/intendedEvent/body
// come from EffectiveReviewEventAndBody so the self-author downgrade is already
// reflected; when intendedEvent != event the preview and title disclose the
// coercion.
func DryRunVerdictReview(ref gh.Ref, headSHA, event, intendedEvent, body string) DryRunPayload {
	preview := fmt.Sprintf("POST %s/%s/pulls/%d/reviews (verdict event=%s)\n",
		ref.Owner, ref.Repo, ref.Number, event)
	if intendedEvent != event {
		preview += fmt.Sprintf("NOTE: You are the PR author — GitHub rejects event=%s; posting as %s.\n", intendedEvent, event)
	}
	preview += "\n" + prettyJSON(struct {
		Body     string `json:"body"`
		Event    string `json:"event"`
		CommitID string `json:"commit_id"`
	}{Body: body, Event: event, CommitID: headSHA})
	title := "Dry-run: " + event + " review (not posted)"
	if intendedEvent != event {
		title = fmt.Sprintf("Dry-run: %s review (own PR: cannot submit %s; summary as comment) (not posted)", event, intendedEvent)
	}
	return DryRunPayload{Title: title, Payload: preview}
}

// DryRunApproveBare renders the preview for the "Approve only" path — a
// content-free APPROVE. event/intendedEvent/body come from
// EffectiveApproveBareEventAndBody so the self-author downgrade (to a note-only
// COMMENT) is already reflected.
func DryRunApproveBare(ref gh.Ref, headSHA, event, intendedEvent, body string) DryRunPayload {
	preview := fmt.Sprintf("POST %s/%s/pulls/%d/reviews (verdict event=%s, approve-only)\n",
		ref.Owner, ref.Repo, ref.Number, event)
	if intendedEvent != event {
		preview += fmt.Sprintf("NOTE: You are the PR author — GitHub rejects event=%s; posting as %s.\n", intendedEvent, event)
	}
	preview += "\n" + prettyJSON(struct {
		Body     string `json:"body"`
		Event    string `json:"event"`
		CommitID string `json:"commit_id"`
	}{Body: body, Event: event, CommitID: headSHA})
	title := "Dry-run: " + event + " review · approve only (not posted)"
	if intendedEvent != event {
		title = fmt.Sprintf("Dry-run: %s review (own PR: cannot submit %s; note-only comment) (not posted)", event, intendedEvent)
	}
	return DryRunPayload{Title: title, Payload: preview}
}

// prettyJSON marshals v as indented JSON, falling back to fmt.Sprint on error.
func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
