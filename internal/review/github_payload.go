package review

import (
	"strings"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// ToReview assembles the draft into a GitHub review payload. Only findings
// with a path and line > 0 become inline comments; general findings appear in
// RenderBody. Every inline body states that appr-ai-sal generated it and
// names the specialist agent.
//
// The default event is COMMENT — the safe choice for the legacy bulk-post
// path (P key) where the user only confirmed "post the whole review", not
// "submit my Approve / Request changes verdict". The persistent overlay's
// confirm-approve phase uses ToReviewForEvent to post with a verdict-driven
// event after explicit user confirmation.
func (d *Draft) ToReview() gh.Review {
	return d.ToReviewForEvent("COMMENT")
}

// ToReviewForEvent is the explicit-event variant of ToReview. event must be
// "COMMENT", "REQUEST_CHANGES", or "APPROVE". When event is "APPROVE" the
// body is intentionally empty per RenderBodyForEvent (no summary text).
func (d *Draft) ToReviewForEvent(event string) gh.Review {
	if event == "" {
		event = "COMMENT"
	}
	var comments []gh.ReviewComment
	for _, ff := range d.FlatPostableFindingsForPost() {
		// InlineReviewComment is the single source of truth for turning a
		// (specialist, finding) pair into the wire payload, including the
		// multi-line StartLine/StartSide fields (Q6.1).
		comments = append(comments, InlineReviewComment(ff.Specialist, ff.Finding))
	}
	return gh.Review{
		CommitID: d.PR.HeadSHA,
		Body:     d.RenderBodyForEvent(event),
		Event:    event,
		Comments: comments,
	}
}

// PostEvent maps the reconciled merge verdict to a GitHub review event
// ("APPROVE" | "REQUEST_CHANGES" | "COMMENT"). Defaults to COMMENT when no
// verdict has been resolved.
//
// PostEvent uses ReconciledMergeVerdict (not EffectiveMergeVerdict) so that
// when the user skips every inline finding backing a request_changes verdict
// and no other blockers remain, the GitHub event we send drops to COMMENT
// rather than asking the author to address objections we no longer make.
func (d *Draft) PostEvent() string {
	if d == nil {
		return "COMMENT"
	}
	switch NormalizeVibeVerdict(d.ReconciledMergeVerdict()) {
	case VibeVerdictApprove:
		return "APPROVE"
	case VibeVerdictRequestChanges:
		return "REQUEST_CHANGES"
	default:
		return "COMMENT"
	}
}

// RenderBodyForEvent returns the markdown body to attach to the GitHub review
// for the given event. APPROVE normally posts an empty body — the user only
// confirmed approval, there is no summary to read. The exception is the
// "no issues found" auto-approve path: when every agent came back clean we
// post the rendered body anyway so the GitHub review explains why we
// approved (instead of looking like a content-free thumbs-up). Other events
// always post the full RenderBody.
func (d *Draft) RenderBodyForEvent(event string) string {
	if event == "APPROVE" {
		if d != nil && d.HasNoFindings() {
			return d.RenderBody()
		}
		return ""
	}
	return d.RenderBody()
}

func normalizeGitHubLogin(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	return strings.TrimSpace(s)
}

// EffectiveReviewEventAndBody returns the GitHub pull request review event and
// body for POST .../pulls/{n}/reviews. If the authenticated viewer is the PR
// author, GitHub rejects event=APPROVE and event=REQUEST_CHANGES (HTTP 422); in
// that case this returns event COMMENT and the full rendered summary with a
// short preamble so the content still lands on the PR.
//
// intendedEvent is the resolved event before that downgrade (after defaulting an
// empty requestedEvent to draft.PostEvent()), and matches event when nothing was
// coerced.
func EffectiveReviewEventAndBody(d *Draft, requestedEvent, viewerLogin string) (event string, body string, intendedEvent string) {
	requestedEvent = strings.TrimSpace(strings.ToUpper(requestedEvent))
	if requestedEvent == "" && d != nil {
		requestedEvent = d.PostEvent()
	}
	if requestedEvent == "" {
		requestedEvent = "COMMENT"
	}
	intendedEvent = requestedEvent
	if d == nil || d.PR == nil {
		return requestedEvent, "", intendedEvent
	}
	body = d.RenderBodyForEvent(requestedEvent)
	event = requestedEvent
	author := normalizeGitHubLogin(d.PR.Author)
	viewer := normalizeGitHubLogin(viewerLogin)
	if author == "" || viewer == "" || !strings.EqualFold(author, viewer) {
		return event, body, intendedEvent
	}
	if event != "APPROVE" && event != "REQUEST_CHANGES" {
		return event, body, intendedEvent
	}
	const note = "_GitHub does not allow **approve** or **request changes** reviews on your own pull request. Posted as a **comment** review._\n\n"
	full := strings.TrimSpace(d.RenderBody())
	if full == "" {
		full = "_No rendered summary body was available._"
	}
	return "COMMENT", note + full, intendedEvent
}

// EffectiveApproveBareEventAndBody is the "Approve only" variant of
// EffectiveReviewEventAndBody — it posts event=APPROVE with an explicit empty
// body regardless of what RenderBodyForEvent would otherwise pick. Used by the
// "Approve only" button in the no-findings auto-approve flow so the reviewer
// can submit a content-free thumbs-up instead of the default summary body that
// explains every agent ran clean.
//
// The self-author downgrade still applies — GitHub rejects APPROVE on your own
// PR, so we coerce to event=COMMENT with just the explanatory note as the body
// (no rendered summary appended, since the reviewer asked for no body). intendedEvent is always "APPROVE" so callers can surface the requested action
// when describing the downgrade.
func EffectiveApproveBareEventAndBody(d *Draft, viewerLogin string) (event string, body string, intendedEvent string) {
	intendedEvent = "APPROVE"
	if d == nil || d.PR == nil {
		return "APPROVE", "", intendedEvent
	}
	author := normalizeGitHubLogin(d.PR.Author)
	viewer := normalizeGitHubLogin(viewerLogin)
	if author == "" || viewer == "" || !strings.EqualFold(author, viewer) {
		return "APPROVE", "", intendedEvent
	}
	const note = "_GitHub does not allow **approve** reviews on your own pull request. Posted as a **comment** review with no body._"
	return "COMMENT", note, intendedEvent
}
