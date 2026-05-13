package review

import (
	"strings"
)

// validateAnchorExcerpt cross-checks every inline finding's AnchorExcerpt
// — the model's verbatim quote of what it thought it was anchoring at —
// against the actual post-image text at Path:Line.
//
// Most "garbage suggestion" failures we see are anchor failures: the model
// picks the wrong line then produces a suggestion for a *different* line.
// Asking the model to quote the anchor turns those into a deterministic
// check, and crucially also gives us enough information to often *fix*
// the anchor instead of just dropping the suggestion.
//
// Three outcomes per finding:
//
//  1. Excerpt matches the line at Path:Line (modulo whitespace) → pass
//     through unchanged.
//
//  2. Excerpt does not match the anchored line BUT uniquely matches a
//     different post-image line in the same hunk → re-anchor to that
//     line (update Finding.Line; record Finding.AnchorRelocatedFrom for
//     audit / TUI hint). The Suggestion is preserved: the model wrote it
//     to replace the line containing the excerpt, so relocating to that
//     line preserves the intended substitution. Downstream gates like
//     validateAndPruneSuggestions then run against the corrected anchor.
//
//  3. Excerpt is absent from the hunk, matches more than one line
//     (ambiguous), or is shorter than substantiveSuggestionLineMin (too
//     noisy to relocate safely) → leave Line alone but strip Suggestion
//     and set SuggestionStrippedReason. We don't drop the whole finding —
//     the prose comment may still be useful to the human even when the
//     one-click fix is unsafe.
//
// The gate is conservative on entry:
//   - Inline-postable findings only (path != "" && line > 0).
//   - AnchorExcerpt must be non-empty (older runs / providers that strip
//     unknown JSON keys won't trigger the gate at all).
//   - Suggestion must be non-empty for the strip path; we still attempt
//     a re-anchor for excerpt-only findings so future gates that key off
//     Finding.Line see the corrected position.
func validateAnchorExcerpt(findings []Finding, files []FileDiff) []Finding {
	for i := range findings {
		f := &findings[i]
		if !findingIsInlinePostable(*f) {
			continue
		}
		if strings.TrimSpace(f.AnchorExcerpt) == "" {
			continue
		}
		applyAnchorExcerptVerdict(f, files)
	}
	return findings
}

// applyAnchorExcerptVerdict consults anchorExcerptVerdict and mutates f
// accordingly: re-anchor on a unique-match outcome, strip the suggestion
// on a mismatch outcome (but only if there is something to strip — annotating
// a finding that never had a suggestion as "suggestion stripped" would be
// misleading on the TUI card).
func applyAnchorExcerptVerdict(f *Finding, files []FileDiff) {
	v := anchorExcerptVerdict(*f, files)
	switch v.outcome {
	case anchorOutcomeMatch:
	case anchorOutcomeRelocate:
		f.AnchorRelocatedFrom = f.Line
		f.Line = v.relocateTo
	case anchorOutcomeStrip:
		if strings.TrimSpace(f.Suggestion) == "" {
			return
		}
		f.Suggestion = ""
		f.SuggestionStrippedReason = v.reason
	}
}

// anchorExcerptOutcome enumerates the three resolution outcomes:
// pass-through (match), re-anchor (unique relocate), or strip (no/ambiguous/short).
type anchorExcerptOutcome int

const (
	anchorOutcomeMatch anchorExcerptOutcome = iota
	anchorOutcomeRelocate
	anchorOutcomeStrip
)

// anchorExcerptVerdictResult is the result of consulting the parsed diff
// for a single finding. relocateTo is meaningful only when outcome is
// anchorOutcomeRelocate; reason only when anchorOutcomeStrip.
type anchorExcerptVerdictResult struct {
	outcome    anchorExcerptOutcome
	relocateTo int
	reason     string
}

// anchorExcerptVerdict classifies the model's AnchorExcerpt against the
// hunk that contains f.Line. Returns anchorOutcomeMatch (silently) when we
// lack ground truth (path not in diff, line not in any hunk, anchor line
// is a deletion-only row): we don't have enough information to either
// trust or distrust the model.
//
// Exposed for tests; not exported.
func anchorExcerptVerdict(f Finding, files []FileDiff) anchorExcerptVerdictResult {
	file := FindFile(files, f.Path)
	if file == nil {
		return anchorExcerptVerdictResult{outcome: anchorOutcomeMatch}
	}
	h, _ := HunkAroundLine(file, f.Line)
	if h == nil {
		return anchorExcerptVerdictResult{outcome: anchorOutcomeMatch}
	}
	_, anchorText := findAnchorLine(h, f.Line)
	if anchorText == "" {
		// Anchor line not found in the post-image (deletion-only line,
		// most likely). The model can't quote a line that doesn't exist
		// in the post-image, so any non-empty excerpt is suspect — but
		// we stay silent here and let the other gates handle it, to
		// avoid a thicket of overlapping reasons.
		return anchorExcerptVerdictResult{outcome: anchorOutcomeMatch}
	}
	normExcerpt := normaliseExcerpt(f.AnchorExcerpt)
	if normaliseExcerpt(anchorText) == normExcerpt {
		return anchorExcerptVerdictResult{outcome: anchorOutcomeMatch}
	}

	// Below this point: the model's excerpt does not match the line it
	// anchored to. Decide between re-anchor and strip-suggestion.

	// Very short excerpts are unsafe to relocate against — `}`, `)`,
	// `return nil`, blank lines, etc. match all over the place. Mirror
	// substantiveSuggestionLineMin's posture from suggestion_validate.go
	// rather than introducing a separate threshold so a future change to
	// either tracks the other.
	if len(normExcerpt) < substantiveSuggestionLineMin {
		return anchorExcerptVerdictResult{
			outcome: anchorOutcomeStrip,
			reason: "anchor excerpt mismatch (model quoted " +
				quoteForReason(f.AnchorExcerpt) +
				" but " + f.Path + ":" + itoa(f.Line) + " is " +
				quoteForReason(anchorText) +
				"; excerpt too short to relocate safely)",
		}
	}

	matches := findAnchorExcerptMatches(h, normExcerpt, f.Line)
	switch len(matches) {
	case 1:
		return anchorExcerptVerdictResult{
			outcome:    anchorOutcomeRelocate,
			relocateTo: matches[0],
		}
	case 0:
		return anchorExcerptVerdictResult{
			outcome: anchorOutcomeStrip,
			reason: "anchor excerpt mismatch (model quoted " +
				quoteForReason(f.AnchorExcerpt) +
				" but " + f.Path + ":" + itoa(f.Line) + " is " +
				quoteForReason(anchorText) + ")",
		}
	default:
		return anchorExcerptVerdictResult{
			outcome: anchorOutcomeStrip,
			reason: "anchor excerpt mismatch (model quoted " +
				quoteForReason(f.AnchorExcerpt) +
				" but " + f.Path + ":" + itoa(f.Line) + " is " +
				quoteForReason(anchorText) +
				"; excerpt matches multiple lines in the hunk, ambiguous)",
		}
	}
}

// findAnchorExcerptMatches returns the new-image line numbers in h whose
// normalised text equals normExcerpt, excluding the line at exclude (the
// original anchor we already rejected). Removed lines are skipped because
// inline findings can only anchor on post-image lines anyway.
func findAnchorExcerptMatches(h *Hunk, normExcerpt string, exclude int) []int {
	var out []int
	for _, l := range h.Lines {
		if l.Kind == DiffRemoved || l.NewNo == 0 {
			continue
		}
		if l.NewNo == exclude {
			continue
		}
		if normaliseExcerpt(l.Text) == normExcerpt {
			out = append(out, l.NewNo)
		}
	}
	return out
}

// normaliseExcerpt strips leading and trailing whitespace and collapses
// runs of internal whitespace to a single space. This lets legitimate
// excerpts pass even when the model re-formatted whitespace (e.g. spaces
// vs tabs, trailing CR, soft-wrap). Anything beyond that — different
// tokens, missing identifiers — counts as a mismatch.
func normaliseExcerpt(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n':
			if b.Len() == 0 {
				continue
			}
			if !inSpace {
				b.WriteRune(' ')
				inSpace = true
			}
		default:
			b.WriteRune(r)
			inSpace = false
		}
	}
	out := b.String()
	return strings.TrimRight(out, " ")
}

// quoteForReason renders s as a short `"..."` excerpt for the human-readable
// SuggestionStrippedReason string. Long lines get truncated with an ellipsis
// so the reason fits comfortably on one TUI line.
func quoteForReason(s string) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	const max = 60
	if len(s) > max {
		s = s[:max] + "…"
	}
	return `"` + s + `"`
}

// itoa avoids pulling in strconv just for one int conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
