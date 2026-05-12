package review

import (
	"strings"
)

// validateAnchorExcerpt clears Finding.Suggestion when the model emitted an
// AnchorExcerpt that doesn't match the actual post-image text at Path:Line.
//
// The reviewOutputContract asks every specialist to copy the line it
// believes it is anchored at into the `anchor_excerpt` JSON field. Most
// "garbage suggestion" failures we see are anchor failures: the model picks
// the wrong line, then produces a suggestion for a *different* line. Asking
// the model to quote the anchor turns those into a single deterministic
// check — if the quoted excerpt doesn't appear at Path:Line, the model
// mis-anchored and the suggestion is unsafe to apply.
//
// The gate is conservative:
//   - Inline-postable findings with a non-empty Suggestion only.
//   - AnchorExcerpt must be non-empty (older runs / providers that strip
//     unknown JSON keys won't trigger the gate at all).
//   - Whitespace is normalised on both sides before comparing so a
//     legitimate excerpt with re-collapsed runs of whitespace still passes.
//   - On mismatch we clear Suggestion only — Comment is preserved so the
//     human reviewer keeps the prose.
func validateAnchorExcerpt(findings []Finding, files []FileDiff) []Finding {
	for i := range findings {
		f := &findings[i]
		if !findingIsInlinePostable(*f) {
			continue
		}
		if strings.TrimSpace(f.Suggestion) == "" {
			continue
		}
		if strings.TrimSpace(f.AnchorExcerpt) == "" {
			continue
		}
		if reason := anchorExcerptMismatch(*f, files); reason != "" {
			f.Suggestion = ""
			f.SuggestionStrippedReason = reason
		}
	}
	return findings
}

// anchorExcerptMismatch returns a non-empty reason string when the model's
// AnchorExcerpt does not match the post-image text at Path:Line. Returns ""
// on uncertainty (path not in diff, line not in any hunk, excerpt empty,
// excerpt matches modulo whitespace).
//
// Exposed for tests; not exported.
func anchorExcerptMismatch(f Finding, files []FileDiff) string {
	file := FindFile(files, f.Path)
	if file == nil {
		return ""
	}
	h, _ := HunkAroundLine(file, f.Line)
	if h == nil {
		return ""
	}
	_, anchorText := findAnchorLine(h, f.Line)
	if anchorText == "" {
		// Anchor line not found in the post-image (deletion-only line,
		// most likely). The model can't quote a line that doesn't exist
		// in the post-image, so any non-empty excerpt is suspect — but
		// we stay silent here and let the other gates handle it, to
		// avoid a thicket of overlapping reasons.
		return ""
	}
	if normaliseExcerpt(anchorText) == normaliseExcerpt(f.AnchorExcerpt) {
		return ""
	}
	return "anchor excerpt mismatch (model quoted " +
		quoteForReason(f.AnchorExcerpt) +
		" but " + f.Path + ":" + itoa(f.Line) + " is " +
		quoteForReason(anchorText) + ")"
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
