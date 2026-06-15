package review

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// codeownersThread models the screenshot: a reviewer asks to update CODEOWNERS
// on an inline thread, and the PR author replies that it already exists. The
// thread is unresolved on GitHub.
func codeownersThread() gh.ReviewThread {
	return gh.ReviewThread{
		IsResolved: false,
		Comments: []gh.ReviewThreadComment{
			{Author: "devonpmack", Body: "Can you update CODEOWNERS as well", Path: "prometheus/alerts/asset-handler.yml", Line: 4},
			{Author: "patyouss", Body: "https://github.com/StackAdapt/alerts/blob/.../CODEOWNERS#L123 Already there I think", Path: "prometheus/alerts/asset-handler.yml", Line: 4},
		},
	}
}

func prWith(author string) *gh.PR { return &gh.PR{Author: author} }

// TestDownrankAuthorRebuttedThreads_LocationMatch demotes a PR-wide finding
// that references the rebutted thread by its "path:line" location.
func TestDownrankAuthorRebuttedThreads_LocationMatch(t *testing.T) {
	findings := []Finding{{
		Severity: SeverityWarning,
		Comment:  "@devonpmack requested an update to CODEOWNERS in the discussion thread (prometheus/alerts/asset-handler.yml:4), but the diff does not show any changes to the CODEOWNERS file.",
	}}
	out := downrankAuthorRebuttedThreads(prWith("patyouss"), findings, []gh.ReviewThread{codeownersThread()})
	if out[0].Severity != SeverityInfo {
		t.Fatalf("expected demotion to info, got %q", out[0].Severity)
	}
	if out[0].ActionabilityNote == "" {
		t.Fatal("expected an actionability note explaining the demotion")
	}
}

// TestDownrankAuthorRebuttedThreads_OpenerMention demotes when the finding only
// @-mentions the reviewer who opened the rebutted thread.
func TestDownrankAuthorRebuttedThreads_OpenerMention(t *testing.T) {
	findings := []Finding{{
		Severity: SeverityError,
		Comment:  "@devonpmack asked for a CODEOWNERS update that is still missing.",
	}}
	out := downrankAuthorRebuttedThreads(prWith("patyouss"), findings, []gh.ReviewThread{codeownersThread()})
	if out[0].Severity != SeverityInfo {
		t.Fatalf("expected demotion to info, got %q", out[0].Severity)
	}
}

// TestDownrankAuthorRebuttedThreads_InlineAnchor demotes an inline finding
// anchored to the rebutted thread's line.
func TestDownrankAuthorRebuttedThreads_InlineAnchor(t *testing.T) {
	findings := []Finding{{
		Path: "prometheus/alerts/asset-handler.yml", Line: 4, Side: "RIGHT",
		Severity: SeverityWarning,
		Comment:  "Reviewer's CODEOWNERS request is unaddressed.",
	}}
	out := downrankAuthorRebuttedThreads(prWith("patyouss"), findings, []gh.ReviewThread{codeownersThread()})
	if out[0].Severity != SeverityInfo {
		t.Fatalf("expected demotion to info, got %q", out[0].Severity)
	}
}

// TestDownrankAuthorRebuttedThreads_ResolvedUntouched leaves findings alone
// when the thread is resolved (not in scope for this backstop).
func TestDownrankAuthorRebuttedThreads_ResolvedUntouched(t *testing.T) {
	th := codeownersThread()
	th.IsResolved = true
	findings := []Finding{{Severity: SeverityWarning, Comment: "@devonpmack CODEOWNERS update missing (prometheus/alerts/asset-handler.yml:4)."}}
	out := downrankAuthorRebuttedThreads(prWith("patyouss"), findings, []gh.ReviewThread{th})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("resolved thread must not be demoted, got %q", out[0].Severity)
	}
}

// TestDownrankAuthorRebuttedThreads_AuthorOpenedUntouched ignores threads the
// author themselves opened (not reviewer feedback).
func TestDownrankAuthorRebuttedThreads_AuthorOpenedUntouched(t *testing.T) {
	th := gh.ReviewThread{Comments: []gh.ReviewThreadComment{
		{Author: "patyouss", Body: "note to self", Path: "a.yml", Line: 1},
		{Author: "patyouss", Body: "fixed", Path: "a.yml", Line: 1},
	}}
	findings := []Finding{{Severity: SeverityWarning, Comment: "thread on a.yml:1 unaddressed"}}
	out := downrankAuthorRebuttedThreads(prWith("patyouss"), findings, []gh.ReviewThread{th})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("author-opened thread must not trigger demotion, got %q", out[0].Severity)
	}
}

// TestDownrankAuthorRebuttedThreads_ReviewerHadLastWord leaves findings alone
// when the reviewer replied after the author (still contested).
func TestDownrankAuthorRebuttedThreads_ReviewerHadLastWord(t *testing.T) {
	th := codeownersThread()
	th.Comments = append(th.Comments, gh.ReviewThreadComment{Author: "devonpmack", Body: "no, that's a different team", Path: "prometheus/alerts/asset-handler.yml", Line: 4})
	findings := []Finding{{Severity: SeverityWarning, Comment: "@devonpmack CODEOWNERS update missing (prometheus/alerts/asset-handler.yml:4)."}}
	out := downrankAuthorRebuttedThreads(prWith("patyouss"), findings, []gh.ReviewThread{th})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("reviewer-last-word thread must not be demoted, got %q", out[0].Severity)
	}
}

// TestDownrankAuthorRebuttedThreads_UnrelatedFindingUntouched leaves a finding
// that doesn't reference the rebutted thread alone.
func TestDownrankAuthorRebuttedThreads_UnrelatedFindingUntouched(t *testing.T) {
	findings := []Finding{{Severity: SeverityWarning, Comment: "@alice asked for a CHANGELOG entry that is missing."}}
	out := downrankAuthorRebuttedThreads(prWith("patyouss"), findings, []gh.ReviewThread{codeownersThread()})
	if out[0].Severity != SeverityWarning {
		t.Fatalf("unrelated finding must not be demoted, got %q", out[0].Severity)
	}
}
