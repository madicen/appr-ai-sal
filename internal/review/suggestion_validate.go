package review

import (
	"regexp"
	"strings"
)

// substantiveSuggestionLineMin is the minimum trimmed length a suggestion
// line needs before we treat a duplicate match against the diff as evidence
// of a malformed suggestion. Short syntactic lines (`}`, `)`, `};`, etc.)
// match across the file constantly and would create endless false positives.
const substantiveSuggestionLineMin = 20

// validateAndPruneSuggestions clears the Suggestion field of any inline
// finding whose suggestion would obviously break the file when GitHub
// applies it. The reason is recorded on Finding.SuggestionStrippedReason so
// the TUI can surface "(suggestion stripped: <reason>)" on the approval
// card instead of letting the user assume the model forgot.
//
// GitHub treats a `suggestion` block as "delete the anchor line, then
// insert these lines in its place". Specialists sometimes get this wrong
// and produce suggestions that:
//
//  1. Repeat a non-trivial line that already lives elsewhere in the same
//     hunk (typical failure: the model wanted to "insert" a block before
//     a new resource declaration but anchored at an unrelated line and
//     replayed the resource declaration in the suggestion — applying it
//     duplicates the declaration).
//  2. Are a single line equal to the anchor — a no-op that still consumes
//     a one-click suggestion slot on the GitHub UI.
//  3. Are anchored at a line that has nothing to do with the comment text
//     (the "comment about `hold`, anchored at `enginsights-dev`" failure
//     mode). Detected when the comment quotes a backtick-identifier that
//     appears nowhere in the surrounding hunk.
//
// We only clear the Suggestion text; the comment is preserved so the human
// reviewer still gets the prose feedback. Dropping the whole finding would
// hide the underlying issue.
//
// validateAndPruneSuggestions is intentionally conservative: it only acts
// on cases we are confident are wrong, because false positives silently
// strip useful one-click fixes. Anchors we cannot locate, suggestions
// shorter than substantiveSuggestionLineMin, and findings that don't anchor
// to a hunk all pass through untouched.
func validateAndPruneSuggestions(findings []Finding, files []FileDiff) []Finding {
	for i := range findings {
		f := &findings[i]
		if !findingIsInlinePostable(*f) {
			continue
		}
		if strings.TrimSpace(f.Suggestion) == "" {
			continue
		}
		if reason := suggestionBreaksFile(*f, files); reason != "" {
			f.Suggestion = ""
			f.SuggestionStrippedReason = reason
		}
	}
	return findings
}

// suggestionBreaksFile returns a non-empty reason string when applying
// f.Suggestion at f.Path:f.Line would clearly break the file (duplicate a
// nearby line, no-op-replace the anchor, etc.). Returns "" otherwise.
//
// Exposed for tests; not exported.
func suggestionBreaksFile(f Finding, files []FileDiff) string {
	file := FindFile(files, f.Path)
	if file == nil {
		return ""
	}
	h, _ := HunkAroundLine(file, f.Line)
	if h == nil {
		return ""
	}
	anchorIdx, anchorText := findAnchorLine(h, f.Line)
	if anchorIdx < 0 {
		return ""
	}

	sugLines := splitSuggestionLines(f.Suggestion)
	if len(sugLines) == 0 {
		return ""
	}

	// 1. No-op single-line suggestion that just repeats the anchor.
	if len(sugLines) == 1 && strings.TrimSpace(sugLines[0]) == strings.TrimSpace(anchorText) {
		return "no-op suggestion (matches the anchor line verbatim)"
	}

	// 2. Suggestion lines that duplicate substantive lines elsewhere in the
	//    same hunk's post-image. The anchor itself is excluded — the model
	//    is encouraged to replay the anchor inside its suggestion.
	hunkLines := postImageLines(h)
	for _, sl := range sugLines {
		st := strings.TrimSpace(sl)
		if len(st) < substantiveSuggestionLineMin {
			continue
		}
		for j, hl := range hunkLines {
			if j == anchorIdx {
				continue
			}
			if strings.TrimSpace(hl) == st {
				return "suggestion duplicates a line already in the hunk"
			}
		}
	}

	// 3. Anchor-vs-comment mismatch: the comment quotes a backtick-identifier
	//    that doesn't appear in the surrounding hunk. This catches the
	//    typical failure mode where the model writes "the entry `hold` lacks
	//    a comment" but anchors at a sibling line like `"enginsights-dev"`
	//    — applying the suggestion would replace the wrong line.
	//
	//    Hunk text is the only authoritative signal: the suggestion is the
	//    model's own output, so finding the identifier there proves only
	//    self-consistency, not anchor correctness. We require ALL quoted
	//    identifiers to be missing from the hunk before stripping, to
	//    avoid false positives on findings that legitimately mention
	//    multiple symbols (one of which was on the anchored line).
	if quoted := extractBacktickIdentifiers(f.Comment); len(quoted) > 0 {
		hunkText := strings.Join(hunkLines, "\n")
		anyHit := false
		for _, q := range quoted {
			if strings.Contains(hunkText, q) {
				anyHit = true
				break
			}
		}
		if !anyHit {
			return "anchor mismatch (comment names " + joinBacktickIdents(quoted) + " but the anchored hunk does not)"
		}
	}
	return ""
}

// backtickIdentifierRe extracts `identifier`-style spans from a comment.
// Identifiers we consider are at least 3 characters of letter/digit/_/-/. so
// we don't trip on things like `?`, `!`, single-char operators, single-letter
// type parameters, etc. Multi-line code spans are excluded by anchoring on
// non-newline content.
var backtickIdentifierRe = regexp.MustCompile("`([A-Za-z0-9_\\-\\.]{3,})`")

func extractBacktickIdentifiers(comment string) []string {
	matches := backtickIdentifierRe.FindAllStringSubmatch(comment, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		ident := m[1]
		if _, ok := seen[ident]; ok {
			continue
		}
		seen[ident] = struct{}{}
		out = append(out, ident)
	}
	return out
}

func joinBacktickIdents(idents []string) string {
	parts := make([]string, len(idents))
	for i, id := range idents {
		parts[i] = "`" + id + "`"
	}
	return strings.Join(parts, ", ")
}

// findAnchorLine returns (index in postImageLines, text) for the anchor or
// (-1, "") if we can't locate it (e.g. line is a deletion-only line).
func findAnchorLine(h *Hunk, target int) (int, string) {
	idx := 0
	for _, l := range h.Lines {
		if l.Kind == DiffRemoved {
			continue
		}
		if l.NewNo == target {
			return idx, l.Text
		}
		idx++
	}
	return -1, ""
}

// postImageLines returns the post-image (added + context) line texts in the
// order they appear in the hunk.
func postImageLines(h *Hunk) []string {
	out := make([]string, 0, len(h.Lines))
	for _, l := range h.Lines {
		if l.Kind == DiffRemoved {
			continue
		}
		out = append(out, l.Text)
	}
	return out
}

// splitSuggestionLines splits a suggestion body into individual lines and
// trims trailing blank lines so a suggestion ending with "\n" doesn't add
// a phantom empty line to the comparison set.
func splitSuggestionLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
