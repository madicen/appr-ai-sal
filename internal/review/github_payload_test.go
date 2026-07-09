package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestToReviewOnlyInlineFindings(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "design",
			Findings: []Finding{
				{Path: "a.go", Line: 10, Comment: "inline", Severity: SeverityWarning},
				{Path: "", Line: 0, Comment: "general only", Severity: SeverityInfo},
			},
		}},
	}
	rev := d.ToReview()
	if len(rev.Comments) != 1 {
		t.Fatalf("comments: got %d want 1", len(rev.Comments))
	}
	if rev.Comments[0].Path != "a.go" || rev.Comments[0].Line != 10 {
		t.Fatalf("unexpected inline: %#v", rev.Comments[0])
	}
	if !strings.Contains(rev.Body, "### PR-wide notes") {
		t.Fatalf("body should have consolidated PR-wide section: %s", rev.Body)
	}
	if !strings.Contains(rev.Body, "general only") {
		t.Fatalf("body should include general finding: %s", rev.Body)
	}
	if !strings.Contains(rev.Body, "info · design:") {
		t.Fatalf("body should tag specialist on PR-wide bullet: %s", rev.Body)
	}
}

func TestEffectiveReviewEventAndBodySelfAuthorDowngradesVerdict(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "octocat", HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Fix validation.",
		},
	}
	ev, body, intent := EffectiveReviewEventAndBody(d, "REQUEST_CHANGES", "@OctoCat")
	if ev != "COMMENT" || intent != "REQUEST_CHANGES" {
		t.Fatalf("event/intent: got %q / %q want COMMENT / REQUEST_CHANGES", ev, intent)
	}
	if !strings.Contains(body, "does not allow") || !strings.Contains(body, "Fix validation.") {
		t.Fatalf("expected preamble + summary: %s", body)
	}

	ev2, body2, intent2 := EffectiveReviewEventAndBody(d, "APPROVE", "octocat")
	if ev2 != "COMMENT" || intent2 != "APPROVE" {
		t.Fatalf("approve event/intent: got %q / %q want COMMENT / APPROVE", ev2, intent2)
	}
	if !strings.Contains(body2, "does not allow") || !strings.Contains(body2, "## appr-ai-sal summary") {
		t.Fatalf("expected preamble + full summary for self-approve: %s", body2)
	}
}

func TestEffectiveReviewEventAndBodyOtherReviewerUnchanged(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "alice", HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Fix validation.",
		},
	}
	ev, body, intent := EffectiveReviewEventAndBody(d, "REQUEST_CHANGES", "bob")
	if ev != "REQUEST_CHANGES" || intent != "REQUEST_CHANGES" {
		t.Fatalf("event/intent: got %q / %q want REQUEST_CHANGES / REQUEST_CHANGES", ev, intent)
	}
	if strings.Contains(body, "does not allow") {
		t.Fatalf("should not add self-PR preamble: %s", body)
	}
	if !strings.Contains(body, "Fix validation.") {
		t.Fatalf("expected summary in body: %s", body)
	}
}

func TestEffectiveReviewEventAndBodySelfCommentUnchanged(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "alice", HeadSHA: "abc"},
	}
	ev, body, intent := EffectiveReviewEventAndBody(d, "COMMENT", "alice")
	if ev != "COMMENT" || intent != "COMMENT" {
		t.Fatalf("event/intent: got %q / %q want COMMENT / COMMENT", ev, intent)
	}
	if strings.Contains(body, "does not allow") {
		t.Fatalf("COMMENT should not get self-PR preamble: %s", body)
	}
}

// EffectiveApproveBareEventAndBody must produce an APPROVE event with an
// explicit empty body for an arbitrary reviewer — regardless of what would
// normally be appended (e.g. the "no issues found" recap RenderBodyForEvent
// attaches when HasNoFindings is true). This is the contract the "Approve
// only" path relies on so the GitHub review goes through with no AI-authored
// text.
func TestEffectiveApproveBareEventAndBodyOtherReviewerEmptyBody(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "alice", HeadSHA: "abc"},
		// Populate the draft enough that RenderBodyForEvent("APPROVE")
		// would otherwise return non-empty content via HasNoFindings —
		// the bare path must override that.
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictApprove},
	}
	ev, body, intent := EffectiveApproveBareEventAndBody(d, "bob")
	if ev != "APPROVE" || intent != "APPROVE" {
		t.Fatalf("event/intent: got %q / %q want APPROVE / APPROVE", ev, intent)
	}
	if body != "" {
		t.Fatalf("approve-only body must be empty for non-author reviewer, got %q", body)
	}
}

// EffectiveApproveBareEventAndBody must downgrade to COMMENT when the viewer
// is the PR author (GitHub rejects APPROVE on your own PR) — but unlike the
// regular self-author downgrade path, it must NOT attach the full rendered
// summary as the body. The reviewer asked for "no body"; the only content we
// add is a short note explaining why the event was coerced.
func TestEffectiveApproveBareEventAndBodySelfAuthorDowngradesToNoteOnly(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{Author: "octocat", HeadSHA: "abc"},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictApprove,
			Summary: "Looks fine.",
		},
	}
	ev, body, intent := EffectiveApproveBareEventAndBody(d, "@OctoCat")
	if ev != "COMMENT" || intent != "APPROVE" {
		t.Fatalf("event/intent: got %q / %q want COMMENT / APPROVE", ev, intent)
	}
	if !strings.Contains(body, "does not allow") {
		t.Fatalf("self-author downgrade must include the explanatory note: %s", body)
	}
	if strings.Contains(body, "Looks fine.") {
		t.Fatalf("approve-only self-author downgrade must NOT attach the rendered summary, got: %s", body)
	}
	if strings.Contains(body, "## appr-ai-sal summary") {
		t.Fatalf("approve-only self-author downgrade must NOT attach the rendered summary heading, got: %s", body)
	}
}

// Approve-only on a Draft missing a PR (defensive case) must still return
// APPROVE/empty-body without panicking.
func TestEffectiveApproveBareEventAndBodyNilPR(t *testing.T) {
	ev, body, intent := EffectiveApproveBareEventAndBody(&Draft{}, "bob")
	if ev != "APPROVE" || intent != "APPROVE" || body != "" {
		t.Fatalf("nil PR: got %q / %q / body=%q want APPROVE / APPROVE / empty", ev, intent, body)
	}
}

func TestRenderBodyForEventApproveNoFindingsPostsBody(t *testing.T) {
	d := &Draft{PR: &gh.PR{HeadSHA: "abc"}}
	body := d.RenderBodyForEvent("APPROVE")
	if body == "" {
		t.Fatal("expected non-empty body for APPROVE when no findings")
	}
	if !strings.Contains(body, "No issues found by any agent") {
		t.Fatalf("expected no-findings notice in APPROVE body: %s", body)
	}
}

func TestRenderBodyForEventApproveWithFindingsEmpty(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: SpecDocs, Findings: []Finding{
				{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo},
			}},
		},
	}
	if body := d.RenderBodyForEvent("APPROVE"); body != "" {
		t.Fatalf("APPROVE with findings should still post empty body: %q", body)
	}
}
