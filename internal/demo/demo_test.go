package demo

import (
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// TestDemoFixturesCarryRichFields guards the new diff-stats and check-rollup
// fields on the canned PRs. The list rows render those chips, so a fixture
// missing the field would silently drop the chip from the demo GIF.
func TestDemoFixturesCarryRichFields(t *testing.T) {
	prs := DemoPullRequests()
	if len(prs) == 0 {
		t.Fatal("DemoPullRequests returned empty slice")
	}
	for _, pr := range prs {
		ref := gh.Ref{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number}
		if pr.ChangedFiles == 0 || (pr.Additions == 0 && pr.Deletions == 0) {
			t.Errorf("%s: should carry diff stats; got +%d/-%d %d files",
				ref, pr.Additions, pr.Deletions, pr.ChangedFiles)
		}
		if pr.ChecksState == "" {
			t.Errorf("%s: should carry a ChecksState (SUCCESS / FAILURE / PENDING)", ref)
		}
	}
}

// TestDemoChecksLookupCoversFixturePRs verifies every canned PR has a
// non-nil checks report when looked up — even the "default" branch falls
// through to a small synthetic report so the Checks pane never lands in
// the "no checks" placeholder during demo runs.
func TestDemoChecksLookupCoversFixturePRs(t *testing.T) {
	for _, pr := range DemoPullRequests() {
		ref := gh.Ref{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number}
		report := DemoChecks(ref)
		if report == nil {
			t.Errorf("%s: DemoChecks returned nil", ref)
			continue
		}
		if report.RollupState == "" && len(report.Runs) == 0 {
			t.Errorf("%s: DemoChecks returned empty report", ref)
		}
	}
}

// TestDemoDiscussionLookupHandlesAllFixturePRs sanity-checks that every
// fixture PR can be looked up without panicking. PRs without a scripted
// timeline return nil — both cases are valid; we only assert no crash.
func TestDemoDiscussionLookupHandlesAllFixturePRs(t *testing.T) {
	for _, pr := range DemoPullRequests() {
		ref := gh.Ref{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number}
		// We don't assert on length — some PRs intentionally have an
		// empty timeline so the renderer can show its "no comments
		// yet" placeholder during the demo.
		_ = DemoDiscussion(ref)
	}
}
