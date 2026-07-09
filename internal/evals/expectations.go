package evals

import (
	"regexp"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// Expectations is the golden truth for one case (corpus/<id>/expectations.json).
//
// The corpus cannot enumerate every acceptable finding, so precision/recall are
// scored against LABELLED findings only:
//   - MustAppear are the seeded issues that SHOULD be reported (recall + true
//     positives).
//   - MustNotAppear are the documented false-positive scars that must be
//     stripped/demoted by the deterministic gates or never emitted by the
//     model (the precision signal): the Kubernetes memory-unit rewrite
//     (717M→717Mi) filed by the wrong lane, `tags` on an
//     aws_s3_bucket_policy, snake_case prescribed on Go.
//
// A finding matching neither list is UNLABELLED and ignored by precision/recall
// (it is neither required nor forbidden). This keeps the metric honest without
// forcing the corpus to describe every legal finding.
type Expectations struct {
	// ExpectedVerdict is the merge verdict the run should reach
	// (approve|request_changes|comment). Empty means "don't score verdict".
	ExpectedVerdict string `json:"expected_verdict"`
	// MustAppear / MustNotAppear are the labelled findings (see above).
	MustAppear    []ExpectFinding `json:"must_appear"`
	MustNotAppear []ExpectFinding `json:"must_not_appear"`
	// ExpectJSONFirstTry, when set, asserts per-agent that the model's output
	// parsed on the first attempt (keyed by agent name). Optional.
	ExpectJSONFirstTry map[string]bool `json:"expect_json_first_try"`
}

// ExpectFinding describes a finding to match. All set fields must hold for a
// finding to match; unset fields (empty string / zero) are wildcards.
type ExpectFinding struct {
	// Specialist is the agent that must (not) file the finding. Required so
	// the metric is per-specialist.
	Specialist string `json:"specialist"`
	// Path / Line pin the location. Path "" matches any path (incl. PR-wide).
	// Line 0 matches any line. LineTolerance widens the line match to a ±N
	// window ("line-ish" matching); 0 means exact.
	Path          string `json:"path"`
	Line          int    `json:"line"`
	LineTolerance int    `json:"line_tolerance"`
	// Pattern is a Go regexp (case-insensitive) matched against the finding's
	// comment. A malformed regexp degrades to a case-insensitive substring
	// test. "" matches any comment.
	Pattern string `json:"pattern"`
	// Note is a human label for the report / test failures (documentary).
	Note string `json:"note"`
}

// matches reports whether finding f (filed by specialist) satisfies e.
func (e ExpectFinding) matches(specialist string, f review.Finding) bool {
	if s := strings.TrimSpace(e.Specialist); s != "" && !strings.EqualFold(s, specialist) {
		return false
	}
	if p := strings.TrimSpace(e.Path); p != "" && !pathEqual(p, f.Path) {
		return false
	}
	if e.Line > 0 {
		tol := e.LineTolerance
		if tol < 0 {
			tol = 0
		}
		if abs(e.Line-f.Line) > tol {
			return false
		}
	}
	if pat := strings.TrimSpace(e.Pattern); pat != "" && !patternMatch(pat, f.Comment) {
		return false
	}
	return true
}

// patternMatch matches pat (a case-insensitive Go regexp) against text,
// degrading to a case-insensitive substring test when pat is not valid regexp.
func patternMatch(pat, text string) bool {
	re, err := regexp.Compile("(?i)" + pat)
	if err != nil {
		return strings.Contains(strings.ToLower(text), strings.ToLower(pat))
	}
	return re.MatchString(text)
}

func pathEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
