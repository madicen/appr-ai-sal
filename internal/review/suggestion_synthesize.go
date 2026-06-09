package review

import (
	"regexp"
	"strings"
)

// synthesizeSuggestions is the last-chance gate that builds a GitHub one-click
// `suggestion` from a finding's own comment when the model named the corrected
// token but emitted no suggestion of its own. It runs after every strip /
// validate gate (validateAndPruneSuggestions, validateAnchorKind,
// validateAnchorExcerpt, ...) so it only ever fills a genuinely empty
// suggestion and never resurrects one a gate deliberately stripped.
//
// It is deliberately a conservative string-substitution net, not semantic
// understanding: it acts only when the comment uses an explicit replacement
// phrasing over quoted/backticked tokens, the old token appears exactly once
// on the anchor line, and the resulting line passes the same safety checks
// (suggestionBreaksFile + anchorKindMismatch) a model-authored suggestion
// would. On any ambiguity it stays silent — a wrong synthesized suggestion is
// worse than none. Accepted suggestions are flagged SuggestionSynthesized so
// the TUI card and the posted comment disclose that appr-ai-sal derived them.
func synthesizeSuggestions(findings []Finding, files []FileDiff) []Finding {
	for i := range findings {
		f := &findings[i]
		if !findingIsInlinePostable(*f) {
			continue
		}
		// Only fill a genuinely empty suggestion, and never fight a gate
		// that already declined one (a stripped reason means the model's
		// suggestion — or its anchor — was suspect, so don't substitute).
		if strings.TrimSpace(f.Suggestion) != "" {
			continue
		}
		if strings.TrimSpace(f.SuggestionStrippedReason) != "" {
			continue
		}
		if cand := synthesizeFromComment(*f, files); cand != "" {
			f.Suggestion = cand
			f.SuggestionSynthesized = true
		}
	}
	return findings
}

// synthesizeFromComment returns a single-line replacement suggestion derived
// from f.Comment for the anchor line at f.Path:f.Line, or "" when nothing can
// be synthesized safely. Exposed (unexported) for tests.
func synthesizeFromComment(f Finding, files []FileDiff) string {
	file := FindFile(files, f.Path)
	if file == nil {
		return ""
	}
	h, _ := HunkAroundLine(file, f.Line)
	if h == nil {
		return ""
	}
	anchorIdx, anchorText := findAnchorLine(h, f.Line)
	if anchorIdx < 0 || strings.TrimSpace(anchorText) == "" {
		return ""
	}

	candidate := ""
	for _, pair := range extractReplacementPairs(f.Comment) {
		old := pair.old
		neu := pair.neu
		if old == "" || neu == "" || old == neu {
			continue
		}
		// The old token must appear exactly once on the anchor line so the
		// substitution target is unambiguous, and the new token must not be
		// present already (otherwise the line is either already fixed or the
		// substitution is ambiguous).
		if strings.Count(anchorText, old) != 1 {
			continue
		}
		if strings.Contains(anchorText, neu) {
			continue
		}
		got := strings.Replace(anchorText, old, neu, 1)
		if got == anchorText {
			continue
		}
		if candidate == "" {
			candidate = got
			continue
		}
		// Multiple extracted pairs disagree on the resulting line — refuse
		// to guess which the author meant.
		if candidate != got {
			return ""
		}
	}
	if candidate == "" {
		return ""
	}

	// Re-validate the synthesized line exactly like a model-authored
	// suggestion: it must not be a no-op, duplicate a nearby hunk line, or
	// mismatch the anchor, and the anchor kind must be compatible with the
	// comment's intent.
	tmp := f
	tmp.Suggestion = candidate
	if reason := suggestionBreaksFile(tmp, files); reason != "" {
		return ""
	}
	if reason := anchorKindMismatch(tmp, files); reason != "" {
		return ""
	}
	return candidate
}

// replacementPair is one (old -> new) substitution extracted from a comment.
type replacementPair struct {
	old string
	neu string
}

// quotedToken matches a single backtick-, single-quote-, or double-quote-
// delimited run. Used as a building block in the replacement-phrase regexes.
const quotedToken = "[`'\"]([^`'\"]{1,80})[`'\"]"

// Conservative replacement-phrase patterns. Each captures two quoted tokens;
// the surrounding wording fixes which is the old (wrong) token and which is
// the new (corrected) one. A bounded run of filler words is tolerated between
// the connective and the second token so real phrasings like
// "... like 'Mi' rather than 'M'" still match. Order of capture groups is
// documented per pattern in extractReplacementPairs.
var (
	// "`old` -> `new`" / "`old` → `new`"
	reArrow = regexp.MustCompile(quotedToken + `\s*(?:->|→|=>)\s*` + quotedToken)
	// "replace `old` with `new`" / "change `old` to `new`"
	reReplaceWith = regexp.MustCompile(`(?i)\b(?:replace|change|swap)\s+` + quotedToken + `\s+(?:with|to|for)\s+` + quotedToken)
	// "use `new` instead of `old`" / "use `new` in place of `old`"
	reInsteadOf = regexp.MustCompile(`(?i)\buse\s+` + quotedToken + `\s+(?:instead\s+of|in\s+place\s+of)\s+(?:\w+\s+){0,3}?` + quotedToken)
	// "`new` rather than `old`" / "`new` not `old`" / "`new`, not `old`"
	reRatherThan = regexp.MustCompile(quotedToken + `\s*,?\s*(?:rather\s+than|not)\s+(?:\w+\s+){0,3}?` + quotedToken)
)

// extractReplacementPairs pulls every (old, new) substitution it can read out
// of a comment using the conservative phrase patterns above.
func extractReplacementPairs(comment string) []replacementPair {
	var pairs []replacementPair
	add := func(old, neu string) {
		old = strings.TrimSpace(old)
		neu = strings.TrimSpace(neu)
		if old == "" || neu == "" {
			return
		}
		pairs = append(pairs, replacementPair{old: old, neu: neu})
	}
	// reArrow / reReplaceWith: first token is old, second is new.
	for _, m := range reArrow.FindAllStringSubmatch(comment, -1) {
		add(m[1], m[2])
	}
	for _, m := range reReplaceWith.FindAllStringSubmatch(comment, -1) {
		add(m[1], m[2])
	}
	// reInsteadOf / reRatherThan: first token is new, second is old.
	for _, m := range reInsteadOf.FindAllStringSubmatch(comment, -1) {
		add(m[2], m[1])
	}
	for _, m := range reRatherThan.FindAllStringSubmatch(comment, -1) {
		add(m[2], m[1])
	}
	return pairs
}
