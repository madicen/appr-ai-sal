package gh

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// linked_issues.go fetches the GitHub issues a pull request is linked to, for
// the Q8 PR-author intent pre-pass. Two independent sources are unioned:
//
//  1. GitHub's own closingIssuesReferences connection on the pull request —
//     the issues GitHub has already resolved as "this PR closes …" (it derives
//     these from closing keywords in the body plus any manually-linked issues).
//  2. Closing keywords parsed directly from the PR body (`closes #N`,
//     `fixes owner/repo#N`, a full issue URL, …). This catches cross-repo
//     references and freshly-edited bodies GitHub has not re-indexed, and each
//     such issue is fetched individually.
//
// The whole operation is fail-open: a private / deleted / cross-repo issue we
// cannot read is simply dropped, never surfaced as an error, so a review is
// never blocked by an inaccessible linked issue. The primary
// closingIssuesReferences query's transport error IS returned (consistent with
// the other gh fetchers) — the caller (the intent pre-pass) treats any error as
// "no linked issues" and proceeds.

// LinkedIssue is a GitHub issue linked to a PR (via closingIssuesReferences or
// a closing keyword in the PR body). Body is the issue's markdown description.
type LinkedIssue struct {
	Repository string // "owner/name"
	Number     int
	Title      string
	Body       string
	State      string // "OPEN" / "CLOSED"
}

// Ref returns the canonical "owner/name#number" reference for the issue.
func (i LinkedIssue) Ref() string {
	return i.Repository + "#" + strconv.Itoa(i.Number)
}

// issueKey is the dedupe/cache key for one issue: lower-cased owner/name#number.
func issueKey(repo string, number int) string {
	return strings.ToLower(repo) + "#" + strconv.Itoa(number)
}

// closingKeywordRe matches a GitHub closing keyword followed by an issue
// reference token. The keyword set mirrors GitHub's: close/closes/closed,
// fix/fixes/fixed, resolve/resolves/resolved (case-insensitive). The captured
// token is the next whitespace-delimited word; parseClosingIssueRefs trims
// trailing punctuation before parseIssueRefToken resolves it (so a URL's own
// dots survive but a trailing "." / "," is dropped).
var closingKeywordRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b\s*:?\s+(\S+)`)

// issueURLRe matches a full github.com issue URL, capturing owner, repo, number.
var issueURLRe = regexp.MustCompile(`^https?://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/(\d+)`)

// issueRefRe matches the "owner/repo#N" and bare "#N" reference forms.
var issueRefRe = regexp.MustCompile(`^(?:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+))?#(\d+)$`)

// issueRef is a resolved reference to a single issue.
type issueRef struct {
	owner  string
	repo   string
	number int
}

// parseClosingIssueRefs extracts the issues a PR body links via closing
// keywords. Bare "#N" references default to defaultOwner/defaultRepo (the PR's
// own repo); "owner/repo#N" and full issue URLs carry their own repo. The
// result is de-duplicated in first-seen order.
func parseClosingIssueRefs(body, defaultOwner, defaultRepo string) []issueRef {
	var out []issueRef
	seen := map[string]bool{}
	for _, m := range closingKeywordRe.FindAllStringSubmatch(body, -1) {
		tok := strings.TrimRight(m[1], ".,;:!?)}\"'")
		ref, ok := parseIssueRefToken(tok, defaultOwner, defaultRepo)
		if !ok {
			continue
		}
		k := issueKey(ref.owner+"/"+ref.repo, ref.number)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, ref)
	}
	return out
}

// parseIssueRefToken resolves a single reference token (the text right after a
// closing keyword) into an issueRef. It accepts "#N", "owner/repo#N", and a
// full github.com issue URL. Returns ok=false for anything else (e.g. a PR
// reference or an external URL).
func parseIssueRefToken(tok, defaultOwner, defaultRepo string) (issueRef, bool) {
	tok = strings.TrimSpace(tok)
	if m := issueURLRe.FindStringSubmatch(tok); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil || n <= 0 {
			return issueRef{}, false
		}
		return issueRef{owner: m[1], repo: m[2], number: n}, true
	}
	if m := issueRefRe.FindStringSubmatch(tok); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil || n <= 0 {
			return issueRef{}, false
		}
		owner, repo := m[1], m[2]
		if owner == "" || repo == "" {
			owner, repo = defaultOwner, defaultRepo
		}
		if owner == "" || repo == "" {
			return issueRef{}, false
		}
		return issueRef{owner: owner, repo: repo, number: n}, true
	}
	return issueRef{}, false
}

// --- caching ---------------------------------------------------------------
//
// Linked issues rarely change within a session, and the intent pre-pass runs
// once per review; the cache keys on (ref, body-hash) so an edited body
// invalidates the entry. Mirrors the pr_cache.go process-lifetime pattern.

type linkedIssuesCacheEntry struct {
	issues []LinkedIssue
	err    error
}

var (
	linkedIssuesCacheMu sync.Mutex
	linkedIssuesCache   = map[string]linkedIssuesCacheEntry{}

	// Injectable so the cache logic is unit-testable without a live network.
	linkedIssuesFetch = fetchLinkedIssues
)

// linkedIssuesCacheKey combines the PR ref with a hash of the body so a body
// edit (which can change the linked set) busts the cache.
func linkedIssuesCacheKey(ref Ref, body string) string {
	sum := sha1.Sum([]byte(body))
	return ref.String() + "\x00" + hex.EncodeToString(sum[:])
}

// GetLinkedIssues returns the issues the PR is linked to (see the file comment).
// body is the PR description (closing keywords are parsed from it). Results are
// cached for the process lifetime keyed by (ref, body). Fail-open: issues we
// cannot read are dropped; only the primary closingIssuesReferences query's
// transport error is surfaced.
func GetLinkedIssues(ctx context.Context, ref Ref, body string) ([]LinkedIssue, error) {
	key := linkedIssuesCacheKey(ref, body)
	linkedIssuesCacheMu.Lock()
	ent, ok := linkedIssuesCache[key]
	linkedIssuesCacheMu.Unlock()
	if ok {
		return ent.issues, ent.err
	}
	issues, err := linkedIssuesFetch(ctx, ref, body)
	linkedIssuesCacheMu.Lock()
	linkedIssuesCache[key] = linkedIssuesCacheEntry{issues: issues, err: err}
	linkedIssuesCacheMu.Unlock()
	return issues, err
}

// closingIssuesRefsData mirrors the closingIssuesReferences GraphQL response.
type closingIssuesRefsData struct {
	Repository struct {
		PullRequest struct {
			ClosingIssuesReferences struct {
				Nodes []issueNode `json:"nodes"`
			} `json:"closingIssuesReferences"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// singleIssueData mirrors the single-issue GraphQL response.
type singleIssueData struct {
	Repository struct {
		Issue *issueNode `json:"issue"`
	} `json:"repository"`
}

// issueNode is the shared node shape for both queries.
type issueNode struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	State      string `json:"state"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

func (n issueNode) toLinkedIssue() LinkedIssue {
	return LinkedIssue{
		Repository: n.Repository.NameWithOwner,
		Number:     n.Number,
		Title:      n.Title,
		Body:       n.Body,
		State:      n.State,
	}
}

// fetchLinkedIssues is the uncached implementation behind GetLinkedIssues.
func fetchLinkedIssues(ctx context.Context, ref Ref, body string) ([]LinkedIssue, error) {
	byKey := map[string]LinkedIssue{}

	// Source 1: GitHub's own closingIssuesReferences connection.
	data, err := graphQLQuery[closingIssuesRefsData](ctx, graphqlClosingIssuesQuery, map[string]any{
		"owner":  ref.Owner,
		"name":   ref.Repo,
		"number": ref.Number,
	})
	if err != nil {
		return nil, err
	}
	for _, n := range data.Repository.PullRequest.ClosingIssuesReferences.Nodes {
		if n.Number <= 0 || n.Repository.NameWithOwner == "" {
			continue
		}
		li := n.toLinkedIssue()
		byKey[issueKey(li.Repository, li.Number)] = li
	}

	// Source 2: closing keywords parsed from the body. Fetch each referenced
	// issue not already captured above; per-issue failures are dropped so a
	// private / cross-repo issue never breaks the review.
	for _, r := range parseClosingIssueRefs(body, ref.Owner, ref.Repo) {
		k := issueKey(r.owner+"/"+r.repo, r.number)
		if _, ok := byKey[k]; ok {
			continue
		}
		li, ok := fetchSingleIssue(ctx, r)
		if !ok {
			continue
		}
		byKey[k] = li
	}

	out := make([]LinkedIssue, 0, len(byKey))
	for _, li := range byKey {
		out = append(out, li)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repository != out[j].Repository {
			return out[i].Repository < out[j].Repository
		}
		return out[i].Number < out[j].Number
	})
	return out, nil
}

// fetchSingleIssue fetches one issue by reference. Returns ok=false (never an
// error) when the issue can't be read, so the caller drops it fail-open.
func fetchSingleIssue(ctx context.Context, r issueRef) (LinkedIssue, bool) {
	data, err := graphQLQuery[singleIssueData](ctx, graphqlSingleIssueQuery, map[string]any{
		"owner":  r.owner,
		"name":   r.repo,
		"number": r.number,
	})
	if err != nil || data.Repository.Issue == nil {
		return LinkedIssue{}, false
	}
	n := data.Repository.Issue
	if n.Number <= 0 || n.Repository.NameWithOwner == "" {
		return LinkedIssue{}, false
	}
	return n.toLinkedIssue(), true
}

const graphqlClosingIssuesQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      closingIssuesReferences(first: 20) {
        nodes {
          number
          title
          body
          state
          repository { nameWithOwner }
        }
      }
    }
  }
}`

const graphqlSingleIssueQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      title
      body
      state
      repository { nameWithOwner }
    }
  }
}`
