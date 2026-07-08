package review

import (
	"fmt"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// thread_routing.go implements B3's thread-aware posting: instead of always
// filing a top-level inline comment, a postable finding that lands on the same
// anchor as an existing UNRESOLVED review thread is routed to an in-thread
// reply (addPullRequestReviewThreadReply via gh.ReplyToReviewThread), and on a
// re-review the tool leaves a short status reply ("resolved" / "still present")
// on its OWN prior threads.
//
// Everything here is pure (no IO, no model calls): it maps findings + gh
// review-thread data to a routing decision or a queued reply, so the posting
// orchestration (internal/tui/data) and the eventual headless CLI can both
// drive it and unit-test it against a fake gh. It is deliberately conservative
// — a finding is only rerouted when its (path, line, side) clearly matches an
// unresolved thread — so the fall-through is always the historical top-level
// post and a first review with no threads behaves exactly as before.

// ThreadRef is an unresolved review thread's identity + anchor, distilled from
// a gh.ReviewThread for routing. ID is the GraphQL node id reused verbatim as
// the reply mutation's pullRequestReviewThreadId (no re-fetch). ByTool marks a
// thread appr-ai-sal opened (its first comment carries the inline marker), so
// re-run status replies can target only the tool's own threads.
type ThreadRef struct {
	ID     string
	Path   string
	Line   int
	Side   string
	ByTool bool
}

// PostRouteKind classifies how a postable finding should reach GitHub.
type PostRouteKind int

const (
	// RouteTopLevel: file a new inline review comment (historical behaviour).
	RouteTopLevel PostRouteKind = iota
	// RouteReply: post an in-thread reply to ThreadID instead of duplicating
	// an existing open thread on the same anchor.
	RouteReply
)

// PostRoute is the routing decision for one finding. ThreadID is set only for
// RouteReply.
type PostRoute struct {
	Kind     PostRouteKind
	ThreadID string
}

// UnresolvedThreadRefs distills the routable unresolved threads from the PR's
// review threads: those that are not resolved, carry a node id, and anchor to
// a concrete (path, line). viewer scopes the ByTool flag to threads the local
// user opened when known; when viewer is empty any thread whose opener body
// carries the appr-ai-sal marker counts as tool-owned (mirrors
// gh.DetectPriorAprrAISalActivity's fallback).
func UnresolvedThreadRefs(threads []gh.ReviewThread, viewer string) []ThreadRef {
	var out []ThreadRef
	for _, t := range threads {
		if t.IsResolved || strings.TrimSpace(t.ID) == "" || len(t.Comments) == 0 {
			continue
		}
		path, line, side := threadAnchorFull(t)
		if strings.TrimSpace(path) == "" || line <= 0 {
			continue
		}
		out = append(out, ThreadRef{
			ID:     t.ID,
			Path:   path,
			Line:   line,
			Side:   side,
			ByTool: threadOpenedByTool(t, viewer),
		})
	}
	return out
}

// RouteFinding decides whether a postable finding should reply in-thread or
// post top-level. It matches on (path, line, side) against the unresolved
// thread refs; a clear match reroutes to a reply, everything else stays a
// top-level post. Only inline-postable findings can be rerouted (PR-wide
// findings have no anchor).
func RouteFinding(f Finding, refs []ThreadRef) PostRoute {
	if !findingIsInlinePostable(f) {
		return PostRoute{Kind: RouteTopLevel}
	}
	if ref, ok := matchFindingToThread(f, refs); ok {
		return PostRoute{Kind: RouteReply, ThreadID: ref.ID}
	}
	return PostRoute{Kind: RouteTopLevel}
}

// matchFindingToThread returns the first unresolved thread ref whose anchor
// matches the finding's path + line (+ side). Path comparison is
// slash-normalized and case-insensitive (mirrors the existing dedupe); side is
// compared after defaulting the empty side to RIGHT, and a thread with an
// unknown side (older payloads) matches any side so we never miss a genuine
// duplicate on a technicality.
func matchFindingToThread(f Finding, refs []ThreadRef) (ThreadRef, bool) {
	fSide := normalizeReviewSide(f.Side)
	for _, r := range refs {
		if r.Line != f.Line {
			continue
		}
		if !filepathToSlashEqual(f.Path, r.Path) {
			continue
		}
		if s := strings.TrimSpace(r.Side); s != "" && normalizeReviewSide(s) != fSide {
			continue
		}
		return r, true
	}
	return ThreadRef{}, false
}

// ThreadStatus is the outcome the tool reports on one of its own prior threads
// on a re-review.
type ThreadStatus int

const (
	// StatusStillPresent: the flagged code survived the new commits.
	StatusStillPresent ThreadStatus = iota
	// StatusResolved: the flagged code is gone from the current diff.
	StatusResolved
)

// StatusReply is a queued in-thread status update on one of the tool's own
// prior unresolved threads, computed from B2's carry-forward result.
type StatusReply struct {
	ThreadID string
	Path     string
	Line     int
	Status   ThreadStatus
	Body     string
}

// BuildStatusReplies derives the re-run status replies (B3.2): for each of the
// tool's OWN unresolved review threads, it finds the matching prior-review
// inline finding (by anchor) and, using B2's carry-forward classification
// against the new diff, decides whether the concern is now resolved (the code
// is gone) or still present (it survived). It returns at most one reply per
// thread.
//
// It returns nil — no replies — whenever the re-run preconditions are not met
// (nil prior cache, no prior diff, no threads), so a caller that always invokes
// it on a first review simply gets nothing to post. Threads whose finding is
// ambiguous (the file changed since, so the fresh specialist re-run re-decides)
// are skipped: the fresh pass posts or replies about those, and a premature
// "resolved" there would be wrong.
func BuildStatusReplies(prior *CachedDraft, newDiff string, threads []gh.ReviewThread, viewer, newHeadSHA string) []StatusReply {
	if prior == nil || strings.TrimSpace(prior.Diff) == "" {
		return nil
	}
	refs := UnresolvedThreadRefs(threads, viewer)
	toolRefs := refs[:0:0]
	for _, r := range refs {
		if r.ByTool {
			toolRefs = append(toolRefs, r)
		}
	}
	if len(toolRefs) == 0 {
		return nil
	}
	newFiles := ParseDiff(newDiff)

	used := map[string]bool{}
	var out []StatusReply
	for _, s := range prior.Specialists {
		if s.Err != nil {
			continue
		}
		for _, f := range s.Findings {
			if !findingIsInlinePostable(f) {
				continue
			}
			ref, ok := matchFindingToThread(f, toolRefs)
			if !ok || used[ref.ID] {
				continue
			}
			status, decided := priorFindingStatus(f, newFiles)
			if !decided {
				// We can't tell confidently (no excerpt to check against a
				// still-present file) — don't post a status we might get wrong.
				continue
			}
			used[ref.ID] = true
			out = append(out, StatusReply{
				ThreadID: ref.ID,
				Path:     ref.Path,
				Line:     ref.Line,
				Status:   status,
				Body:     statusReplyBody(status, newHeadSHA),
			})
		}
	}
	return out
}

// priorFindingStatus decides whether a prior finding's flagged code is gone
// (resolved) or still present in the new PR diff, checking the finding's
// verbatim AnchorExcerpt against the new post-image rather than reusing
// carryForward's changed-file heuristic (which treats any touched file as
// "re-decide" — too coarse for a status the reviewer will read). The decision:
//
//   - file no longer in the PR diff at all → resolved (the change was reverted
//     / rebased away, so the flagged addition is gone);
//   - excerpt still present in the new post-image → still present;
//   - excerpt absent from the new post-image → resolved.
//
// The second return is false when we cannot decide confidently (no excerpt on
// a still-changed file); the caller then skips the thread rather than guess.
func priorFindingStatus(f Finding, newFiles []FileDiff) (ThreadStatus, bool) {
	file := FindFile(newFiles, f.Path)
	if file == nil {
		return StatusResolved, true
	}
	// Only judge presence from an excerpt long enough to be reliable (the same
	// 20-char floor the anchor gate uses). A missing / too-short excerpt leaves
	// us unable to tell a resolved concern from a surviving one, so we skip.
	if len(normaliseExcerpt(f.AnchorExcerpt)) < 20 {
		return 0, false
	}
	if excerptPresentInFile(file, f.AnchorExcerpt) {
		return StatusStillPresent, true
	}
	return StatusResolved, true
}

// excerptPresentInFile reports whether excerpt matches at least one post-image
// (added or context) line of the file's diff, whitespace-normalised. Unlike
// FindUniqueExcerptInFile it accepts multiple matches (a line duplicated in the
// new code is still "present", not "gone"), so a status reply never
// misreports a still-present concern as resolved on a technicality. Excerpts
// shorter than the same 20-char floor are treated as too weak to judge
// presence and report false.
func excerptPresentInFile(file *FileDiff, excerpt string) bool {
	if file == nil {
		return false
	}
	norm := normaliseExcerpt(excerpt)
	if len(norm) < 20 {
		return false
	}
	for hi := range file.Hunks {
		h := &file.Hunks[hi]
		for _, l := range h.Lines {
			if l.Kind == DiffRemoved || l.NewNo == 0 {
				continue
			}
			if normaliseExcerpt(l.Text) == norm {
				return true
			}
		}
	}
	return false
}

// statusReplyBody renders the short in-thread status message. It carries the
// appr-ai-sal inline marker (gh.AprrAISalInlineMarker) so a later re-run still
// recognises the thread as tool-owned, and names the head SHA it was evaluated
// against.
func statusReplyBody(status ThreadStatus, headSHA string) string {
	lead := "**AI-generated review comment** — tool: **appr-ai-sal**, agent: **discussion**\n\n"
	at := ""
	if sha := strings.TrimSpace(headSHA); sha != "" {
		at = fmt.Sprintf(" as of `%s`", shortSHA(sha))
	}
	switch status {
	case StatusResolved:
		return lead + fmt.Sprintf("Re-review update: this looks **resolved**%s — the code this comment flagged is no longer present in the current diff. Please resolve this thread if you agree.", at)
	default:
		return lead + fmt.Sprintf("Re-review update: this concern appears **still present**%s — the flagged code remains in the current diff.", at)
	}
}

// threadAnchorFull returns the (path, line, side) the thread anchors to, taking
// the first comment that carries a path (mirrors threadAnchor but also returns
// the diff side).
func threadAnchorFull(t gh.ReviewThread) (string, int, string) {
	for _, c := range t.Comments {
		if strings.TrimSpace(c.Path) != "" {
			return c.Path, c.Line, c.Side
		}
	}
	return "", 0, ""
}

// threadOpenedByTool reports whether appr-ai-sal opened the thread: its first
// comment's body carries the inline marker and (when viewer is known) is
// authored by the local user.
func threadOpenedByTool(t gh.ReviewThread, viewer string) bool {
	if len(t.Comments) == 0 {
		return false
	}
	opener := t.Comments[0]
	if !strings.Contains(opener.Body, gh.AprrAISalInlineMarker) {
		return false
	}
	v := strings.TrimSpace(viewer)
	if v == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(opener.Author), v)
}

// normalizeReviewSide upper-cases a diff side and defaults the empty side to
// RIGHT (the post-image side findings and inline comments use by default).
func normalizeReviewSide(side string) string {
	s := strings.ToUpper(strings.TrimSpace(side))
	if s == "" {
		return "RIGHT"
	}
	return s
}
