package review

import (
	"regexp"
	"strings"
)

// validateActionability inspects each finding and demotes ones whose comment
// is a bare deficiency statement ("X lacks a comment", "missing
// documentation", "needs a docstring") with no concrete proposed wording
// and no GitHub-postable suggestion. Such findings violate the docs and
// testing prompts' actionability bar — the prose is supposed to spell out
// what to add — but the model occasionally still emits them. Demoting to
// info (rather than dropping outright) preserves the signal for reviewers
// who run with a strictness floor low enough to see info, while keeping
// these noisy nudges out of the way at balanced/strict.
//
// The Severity is mutated in place to SeverityInfo when (and only when):
//   - The specialist's registry spec carries GateActionability with a
//     deficiencyPattern (built-ins: docs and testing; other specialists have
//     their own rhetoric and aren't covered by this rule). This is the
//     registry consultation that replaced the hard-coded docs/testing switch.
//   - The current severity is warning or error (info / critical untouched —
//     critical is reserved for security and never produced by docs/testing
//     in practice; demoting info is a no-op).
//   - The comment matches the spec's bare-deficiency pattern (docs vs testing
//     regex, selected declaratively via the spec).
//   - The comment carries no proposed wording (no quoted text spans of
//     usable length, no "should be", no "rename to", no "→", no colon
//     followed by substantive replacement text).
//   - The Suggestion field is empty (no machine-applicable fix to lean on).
//
// When demoted, ActionabilityNote records why so the TUI can surface a
// "(low actionability: bare deficiency statement)" hint on the card.
//
// Returns the same slice for ergonomics.
func validateActionability(specialist string, findings []Finding) []Finding {
	spec, ok := lookupSpec(specialist)
	if !ok || !spec.hasGate(GateActionability) || spec.deficiencyPattern == nil {
		return findings
	}
	for i := range findings {
		f := &findings[i]
		body := strings.TrimSpace(f.Comment)
		if body == "" || !spec.deficiencyPattern.MatchString(body) {
			continue
		}
		if strings.TrimSpace(f.Suggestion) != "" {
			continue
		}
		if hasProposedWording(f.Comment) {
			continue
		}
		switch f.Severity {
		case SeverityWarning, SeverityError:
			f.Severity = SeverityInfo
			f.ActionabilityNote = "low actionability: comment names a deficiency without proposing wording / a fix"
		}
	}
	return findings
}

// docsDeficiencyRe matches comments of the form "X lacks a (doc) comment",
// "missing documentation", "should be documented", "needs a docstring",
// and similar bare-deficiency phrasings the docs prompt warns against.
var docsDeficiencyRe = regexp.MustCompile(`(?i)\b(lacks?|missing|no|needs?|requires?|should\s+have|should\s+be|undocumented)\b[^.\n]{0,80}\b(comment|comments|doc|docs|documentation|docstring|godoc|jsdoc|description|explanation)\b`)

// testingDeficiencyRe matches "lacks a test", "missing tests", "needs unit
// tests", "should be tested", etc. Mirrors docsDeficiencyRe but for the
// testing specialist's typical bare-deficiency comments.
//
// docsDeficiencyRe and testingDeficiencyRe are wired into the docs and testing
// specs' deficiencyPattern in registry.go; validateActionability selects the
// right one via the spec rather than a name switch.
var testingDeficiencyRe = regexp.MustCompile(`(?i)\b(lacks?|missing|no|needs?|requires?|should\s+have|should\s+be|untested)\b[^.\n]{0,80}\b(test|tests|coverage|unit\s+test|integration\s+test|spec|specs)\b`)

// combinedDeficiencyRe matches either the docs or the testing bare-deficiency
// phrasing. It is the pattern given to a user-defined specialist that opts into
// GateActionability (registry_user.go), since a custom lane may cover either
// concern; the built-in docs/testing specs use their own narrower patterns.
var combinedDeficiencyRe = regexp.MustCompile(`(?i)\b(lacks?|missing|no|needs?|requires?|should\s+have|should\s+be|undocumented|untested)\b[^.\n]{0,80}\b(comment|comments|doc|docs|documentation|docstring|godoc|jsdoc|description|explanation|test|tests|coverage|unit\s+test|integration\s+test|spec|specs)\b`)

// proposedWordingMarkers are short phrases that strongly indicate the comment
// has moved past "X is missing" into "here is what to add / change". When any
// of these are present the actionability check passes.
var proposedWordingMarkers = []string{
	"should be ", "rename to ", "change to ", "replace with ", "use ",
	"add ", "set ", "consider ", "instead of ", "→", "->",
	"e.g.", "e.g ", "for example", "such as ",
	"document ", "explain ", "clarify ", "describe ",
}

// hasProposedWording returns true when the comment contains any sign of
// concrete proposed wording: a substantive backtick-quoted span (likely
// the suggested replacement), a quoted block with double quotes, a colon
// followed by enough text to look like a suggestion, or one of the
// proposedWordingMarkers above.
func hasProposedWording(comment string) bool {
	body := strings.TrimSpace(comment)
	if body == "" {
		return false
	}
	lower := strings.ToLower(body)
	for _, m := range proposedWordingMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	for _, q := range extractBacktickIdentifiers(body) {
		if len(q) >= 6 {
			return true
		}
	}
	if hasSubstantiveQuotedSpan(body) {
		return true
	}
	if hasSubstantivePostColon(body) {
		return true
	}
	return false
}

// hasSubstantiveQuotedSpan reports whether body contains a "..." span at
// least 12 characters long — typical of a quoted proposed sentence.
func hasSubstantiveQuotedSpan(body string) bool {
	for {
		i := strings.Index(body, `"`)
		if i < 0 {
			return false
		}
		rest := body[i+1:]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return false
		}
		span := rest[:j]
		if len(strings.TrimSpace(span)) >= 12 {
			return true
		}
		body = rest[j+1:]
	}
}

// hasSubstantivePostColon reports whether the comment has a ": " with at
// least 24 characters of substantive text after it — typical of "Header:
// here is the actual proposed wording...".
func hasSubstantivePostColon(body string) bool {
	idx := strings.Index(body, ": ")
	if idx < 0 {
		return false
	}
	post := strings.TrimSpace(body[idx+2:])
	return len(post) >= 24
}
