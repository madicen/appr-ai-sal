package gh

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// APIError is a parsed view of a non-2xx response body emitted by `gh api`.
//
// gh's combined output for a failed API call looks roughly like:
//
//	gh api repos/OWNER/REPO/pulls/N/comments: {"message":"Validation Failed","errors":[{...}],"status":"422"}
//	gh: Validation Failed (HTTP 422)
//
// We extract the JSON object so the TUI can produce a structured diagnostic
// (which inline comment failed, why) instead of dumping the raw blob.
type APIError struct {
	Status      int            // HTTP status (0 if we couldn't parse it).
	Message     string         // Top-level "message" field (e.g. "Validation Failed").
	Errors      []APIErrorItem // Per-field errors, when present.
	DocURL      string         // documentation_url, when present.
	APIPath     string         // The API path that was called (extracted from gh's "gh api PATH:" prefix).
	CommitID    string         // commit_id we attempted to anchor to (set by callers that know it).
	Comment     *ReviewComment // Inline comment we attempted to post (set by CreatePullReviewComment).
	RawBody     string         // Original gh combined output (always populated).
	HumanReason string         // Heuristic, user-facing explanation when we recognise the cause.
}

// APIErrorItem is one entry from a 4xx response's "errors" array.
type APIErrorItem struct {
	Resource string `json:"resource"`
	Code     string `json:"code"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}

// Error returns a human-friendly multi-line description.
//
// The persistent overlay shows this as the "✗ …" badge so it must include the
// most actionable bits — what we tried, what GitHub said, and (when we can
// guess) the most likely cause.
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	switch {
	case e.Status > 0 && e.Message != "":
		fmt.Fprintf(&b, "GitHub %d: %s", e.Status, e.Message)
	case e.Status > 0:
		fmt.Fprintf(&b, "GitHub %d", e.Status)
	case e.Message != "":
		b.WriteString(e.Message)
	default:
		b.WriteString("GitHub API error")
	}
	if e.Comment != nil && e.Comment.Path != "" && e.Comment.Line > 0 {
		side := e.Comment.Side
		if side == "" {
			side = "RIGHT"
		}
		fmt.Fprintf(&b, " · %s:%d (%s)", e.Comment.Path, e.Comment.Line, side)
	}
	if e.CommitID != "" {
		fmt.Fprintf(&b, " · commit %s", shortSHA(e.CommitID))
	}
	for _, item := range e.Errors {
		switch {
		case item.Field != "" && item.Message != "":
			fmt.Fprintf(&b, "\n  · %s: %s", item.Field, item.Message)
		case item.Code != "" && item.Message != "":
			fmt.Fprintf(&b, "\n  · %s (%s)", item.Message, item.Code)
		case item.Message != "":
			fmt.Fprintf(&b, "\n  · %s", item.Message)
		}
	}
	if e.HumanReason != "" {
		b.WriteString("\n→ " + e.HumanReason)
	}
	return b.String()
}

// IsLineUnresolvable reports whether err carries a 422 with the
// "pull_request_review_thread.line could not be resolved" signature.
//
// That happens when the inline comment's commit_id+path+line tuple does not
// land on a hunk in the diff for that commit — usually because the PR was
// updated or the line we asked for is no longer commentable on that commit.
func IsLineUnresolvable(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || ae == nil {
		return false
	}
	if ae.Status != 422 {
		return false
	}
	for _, it := range ae.Errors {
		if strings.Contains(it.Field, "pull_request_review_thread.line") ||
			strings.Contains(strings.ToLower(it.Message), "could not be resolved") {
			return true
		}
	}
	return false
}

// HeadDriftError is returned by the post commands when we detect that the
// PR's head SHA has changed since the review was generated — i.e. the user
// pushed a new commit. Surfacing it as a typed error lets the overlay show a
// clear "[R] Refresh PR" prompt rather than letting the post fail downstream.
type HeadDriftError struct {
	Was string // pr.HeadSHA we were about to post against.
	Now string // current head SHA on GitHub.
}

func (e *HeadDriftError) Error() string {
	return fmt.Sprintf("PR head moved from %s to %s — the review was generated against an older commit. Refresh the PR (R) to re-anchor and retry.",
		shortSHA(e.Was), shortSHA(e.Now))
}

// IsHeadDrift reports whether err is a *HeadDriftError.
func IsHeadDrift(err error) (*HeadDriftError, bool) {
	var d *HeadDriftError
	if errors.As(err, &d) {
		return d, true
	}
	return nil, false
}

// parseGHError extracts a structured APIError from the combined output of a
// failed `gh api` invocation. It always returns a non-nil *APIError so callers
// can rely on .RawBody being populated even when no JSON was found.
//
// apiPath is the API path we hit (e.g. "repos/foo/bar/pulls/1/comments") and
// is recorded on the result for diagnostics.
func parseGHError(out []byte, apiPath string) *APIError {
	raw := strings.TrimSpace(string(out))
	res := &APIError{APIPath: apiPath, RawBody: raw}
	// Locate a JSON object embedded in the gh stderr/stdout. gh prints
	//   gh api PATH: { "message": "...", "errors": [...], "status": "422"}
	// followed by "gh: Validation Failed (HTTP 422)". The JSON is the only
	// "{...}" block on the first line.
	if start := strings.Index(raw, "{"); start >= 0 {
		end := strings.LastIndex(raw, "}")
		if end > start {
			body := raw[start : end+1]
			var parsed struct {
				Message          string         `json:"message"`
				Errors           []APIErrorItem `json:"errors"`
				DocumentationURL string         `json:"documentation_url"`
				Status           json.RawMessage `json:"status"`
			}
			if err := json.Unmarshal([]byte(body), &parsed); err == nil {
				res.Message = parsed.Message
				res.Errors = parsed.Errors
				res.DocURL = parsed.DocumentationURL
				res.Status = parseStatusField(parsed.Status)
			}
		}
	}
	// Fallback: gh appends "(HTTP 422)" to its trailing line.
	if res.Status == 0 {
		if i := strings.Index(raw, "(HTTP "); i >= 0 {
			tail := raw[i+len("(HTTP "):]
			if j := strings.Index(tail, ")"); j > 0 {
				_, err := fmt.Sscanf(tail[:j], "%d", &res.Status)
				_ = err // ignore — fall through to 0.
			}
		}
	}
	if res.Message == "" {
		res.Message = strings.TrimSpace(raw)
	}
	res.HumanReason = inferHumanReason(res)
	return res
}

// parseStatusField accepts either a JSON number or a string (GitHub returns
// "422" as a string in the comments endpoint and 422 as a number in some
// others) and returns the HTTP status code, or 0 when it can't.
func parseStatusField(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	s := strings.Trim(string(raw), "\" ")
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n
	}
	return 0
}

// inferHumanReason produces a short hint when we recognise the failure
// pattern. Returns "" when we don't have anything more useful than the raw
// GitHub response.
func inferHumanReason(e *APIError) string {
	if e == nil {
		return ""
	}
	for _, it := range e.Errors {
		if strings.Contains(it.Field, "pull_request_review_thread.line") ||
			strings.Contains(strings.ToLower(it.Message), "could not be resolved") {
			return "GitHub couldn't anchor this comment to the PR diff at that commit. The PR may have been updated since the review was generated, or the line is no longer part of an active hunk. Press R to refresh the PR."
		}
		if strings.EqualFold(it.Code, "missing_field") {
			return "GitHub rejected the request because a required field was empty: " + it.Field
		}
	}
	if e.Status == 401 || e.Status == 403 {
		return "GitHub rejected the request as unauthorized — re-run `gh auth login` or check repo permissions."
	}
	if e.Status == 404 {
		return "GitHub couldn't find that resource. The PR may have been closed, the path may have been renamed, or your gh user lacks access."
	}
	return ""
}

// shortSHA returns the first 7 chars of a SHA, or the original if shorter.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
