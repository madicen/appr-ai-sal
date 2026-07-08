package review

import (
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

const toolLead = "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **security**\n\n"

func humanThread(id, path string, line int, side string) gh.ReviewThread {
	return gh.ReviewThread{
		ID: id,
		Comments: []gh.ReviewThreadComment{
			{Author: "alice", Body: "please validate this", Path: path, Line: line, Side: side},
		},
	}
}

func toolThread(id, path string, line int) gh.ReviewThread {
	return gh.ReviewThread{
		ID: id,
		Comments: []gh.ReviewThreadComment{
			{Author: "octocat", Body: toolLead + "token is not validated", Path: path, Line: line, Side: "RIGHT"},
		},
	}
}

// TestRouteFindingMatchingThreadReplies confirms a finding on the same anchor
// as an unresolved thread is rerouted to an in-thread reply carrying that
// thread's node id.
func TestRouteFindingMatchingThreadReplies(t *testing.T) {
	refs := UnresolvedThreadRefs([]gh.ReviewThread{humanThread("PRRT_1", "auth.go", 42, "RIGHT")}, "")
	f := Finding{Path: "auth.go", Line: 42, Side: "RIGHT", Comment: "still unvalidated", Severity: SeverityError}
	route := RouteFinding(f, refs)
	if route.Kind != RouteReply {
		t.Fatalf("expected RouteReply, got %v", route.Kind)
	}
	if route.ThreadID != "PRRT_1" {
		t.Fatalf("reply should reuse thread id, got %q", route.ThreadID)
	}
}

// TestRouteFindingNonMatchingTopLevel confirms a finding whose anchor matches
// no unresolved thread posts top-level as today.
func TestRouteFindingNonMatchingTopLevel(t *testing.T) {
	refs := UnresolvedThreadRefs([]gh.ReviewThread{humanThread("PRRT_1", "auth.go", 42, "RIGHT")}, "")
	// Different line → no match.
	f := Finding{Path: "auth.go", Line: 7, Side: "RIGHT", Comment: "c", Severity: SeverityWarning}
	if got := RouteFinding(f, refs); got.Kind != RouteTopLevel {
		t.Fatalf("non-matching line should stay top-level, got %v", got.Kind)
	}
	// Different file → no match.
	f2 := Finding{Path: "other.go", Line: 42, Side: "RIGHT", Comment: "c", Severity: SeverityWarning}
	if got := RouteFinding(f2, refs); got.Kind != RouteTopLevel {
		t.Fatalf("non-matching path should stay top-level, got %v", got.Kind)
	}
}

// TestRouteFindingResolvedThreadIgnored confirms resolved threads never draw a
// reply — the finding falls through to a fresh top-level post.
func TestRouteFindingResolvedThreadIgnored(t *testing.T) {
	th := humanThread("PRRT_1", "auth.go", 42, "RIGHT")
	th.IsResolved = true
	refs := UnresolvedThreadRefs([]gh.ReviewThread{th}, "")
	if len(refs) != 0 {
		t.Fatalf("resolved thread should not produce a ref, got %d", len(refs))
	}
	f := Finding{Path: "auth.go", Line: 42, Side: "RIGHT", Comment: "c", Severity: SeverityError}
	if got := RouteFinding(f, refs); got.Kind != RouteTopLevel {
		t.Fatalf("resolved thread should not reroute, got %v", got.Kind)
	}
}

// TestRouteFindingFirstReviewBackwardCompat confirms that with no threads at
// all every finding routes top-level (a first review behaves exactly as
// before B3).
func TestRouteFindingFirstReviewBackwardCompat(t *testing.T) {
	refs := UnresolvedThreadRefs(nil, "")
	f := Finding{Path: "auth.go", Line: 42, Comment: "c", Severity: SeverityError}
	if got := RouteFinding(f, refs); got.Kind != RouteTopLevel {
		t.Fatalf("no threads should route top-level, got %v", got.Kind)
	}
}

// TestRouteFindingPRWideNeverReplies confirms PR-wide (unanchored) findings are
// never rerouted.
func TestRouteFindingPRWideNeverReplies(t *testing.T) {
	refs := UnresolvedThreadRefs([]gh.ReviewThread{humanThread("PRRT_1", "auth.go", 42, "RIGHT")}, "")
	f := Finding{Comment: "whole-PR note", Severity: SeverityInfo}
	if got := RouteFinding(f, refs); got.Kind != RouteTopLevel {
		t.Fatalf("PR-wide finding must not reply, got %v", got.Kind)
	}
}

// diff fixtures for status-reply carry-forward.
const priorDiffRouting = `diff --git a/auth.go b/auth.go
--- a/auth.go
+++ b/auth.go
@@ -1,3 +1,4 @@
 package auth
+var token = readToken() // unvalidated-token-marker
 func check() {}
`

// newDiffResolved drops the flagged line (code gone → resolved).
const newDiffResolved = `diff --git a/auth.go b/auth.go
--- a/auth.go
+++ b/auth.go
@@ -1,3 +1,3 @@
 package auth
 func check() {}
`

// newDiffStillPresent keeps the flagged line byte-identical (survives).
const newDiffStillPresent = priorDiffRouting

func priorCachedDraft() *CachedDraft {
	return &CachedDraft{
		Diff: priorDiffRouting,
		Specialists: []SpecialistResult{{
			Specialist: SpecSecurity,
			Findings: []Finding{{
				Path: "auth.go", Line: 2, Side: "RIGHT", Severity: SeverityError,
				Comment: "token is not validated", AnchorExcerpt: "var token = readToken() // unvalidated-token-marker",
			}},
		}},
	}
}

// TestBuildStatusRepliesResolved confirms a prior finding whose code is gone in
// the new diff yields a "resolved" status reply on the tool's own thread.
func TestBuildStatusRepliesResolved(t *testing.T) {
	threads := []gh.ReviewThread{toolThread("PRRT_own", "auth.go", 2)}
	replies := BuildStatusReplies(priorCachedDraft(), newDiffResolved, threads, "octocat", "deadbeef1234")
	if len(replies) != 1 {
		t.Fatalf("expected 1 status reply, got %d", len(replies))
	}
	if replies[0].Status != StatusResolved {
		t.Fatalf("expected StatusResolved, got %v", replies[0].Status)
	}
	if replies[0].ThreadID != "PRRT_own" {
		t.Fatalf("reply should target own thread, got %q", replies[0].ThreadID)
	}
	if !strings.Contains(replies[0].Body, "resolved") || !strings.Contains(replies[0].Body, "deadbee") {
		t.Fatalf("resolved body should mention resolved + short sha: %q", replies[0].Body)
	}
}

// TestBuildStatusRepliesStillPresent confirms a surviving prior finding yields
// a "still present" reply.
func TestBuildStatusRepliesStillPresent(t *testing.T) {
	threads := []gh.ReviewThread{toolThread("PRRT_own", "auth.go", 2)}
	replies := BuildStatusReplies(priorCachedDraft(), newDiffStillPresent, threads, "octocat", "cafebabe")
	if len(replies) != 1 || replies[0].Status != StatusStillPresent {
		t.Fatalf("expected 1 still-present reply, got %+v", replies)
	}
	if !strings.Contains(replies[0].Body, "still present") {
		t.Fatalf("body should say still present: %q", replies[0].Body)
	}
}

// TestBuildStatusRepliesOnlyOwnThreads confirms status replies never target a
// human reviewer's thread — only threads appr-ai-sal opened.
func TestBuildStatusRepliesOnlyOwnThreads(t *testing.T) {
	threads := []gh.ReviewThread{humanThread("PRRT_human", "auth.go", 2, "RIGHT")}
	if replies := BuildStatusReplies(priorCachedDraft(), newDiffResolved, threads, "octocat", "sha"); len(replies) != 0 {
		t.Fatalf("expected no replies on human thread, got %+v", replies)
	}
}

// TestBuildStatusRepliesFirstReviewNil confirms no prior cache → no replies
// (re-run gating; first review behaves as before).
func TestBuildStatusRepliesFirstReviewNil(t *testing.T) {
	if replies := BuildStatusReplies(nil, newDiffResolved, []gh.ReviewThread{toolThread("x", "auth.go", 2)}, "octocat", "sha"); replies != nil {
		t.Fatalf("nil prior cache should yield no replies, got %+v", replies)
	}
}

// TestDryRunThreadReplyDisclosesRouting confirms the dry-run preview names the
// reply routing and carries the thread id (accuracy of the reply-vs-new
// preview).
func TestDryRunThreadReplyDisclosesRouting(t *testing.T) {
	f := Finding{Path: "auth.go", Line: 42, Comment: "c", Severity: SeverityError}
	p := DryRunThreadReply(gh.Ref{Owner: "o", Repo: "r", Number: 3}, "PRRT_1", SpecSecurity, f)
	if !strings.Contains(p.Title, "reply to existing thread") {
		t.Fatalf("title should disclose reply routing: %q", p.Title)
	}
	if !strings.Contains(p.Payload, "PRRT_1") || !strings.Contains(p.Payload, "addPullRequestReviewThreadReply") {
		t.Fatalf("payload should carry the thread id + mutation: %q", p.Payload)
	}
}
