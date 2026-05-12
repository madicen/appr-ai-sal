package review

import (
	"regexp"
	"strings"
)

// validateAnchorKind clears the Suggestion of an inline finding when the
// anchor line's "kind" (comment-only / blank / closing brace) is obviously
// incompatible with the kind of change the comment proposes (a declaration,
// a prose fix, etc.).
//
// This is the catch for the failure mode in the reference screenshot: a Go
// file where four `+` comment lines were added, and the model anchored at the
// last comment line while claiming "the function name should be snake_case"
// and emitting a struct-literal as the suggestion. The line being deleted is
// a code comment; the proposed replacement is unrelated code. None of the
// existing gates (no-op, duplicate-of-nearby-line, backtick-quoted-anchor-
// mismatch) fire on this case.
//
// The check is conservative — we strip only when both signals are
// unambiguous:
//   - the anchor is purely a comment line, a blank line, or a closing brace
//     (no code on it that could plausibly be what the comment is about);
//   - the comment's intent is to change a declaration (function, type,
//     variable, etc.) or to fix prose, neither of which can live on a bare
//     closing brace, and neither of which can be expressed by replacing a
//     comment line with non-comment code.
//
// We only clear Suggestion; Comment is preserved so the human reviewer still
// sees the prose. validateAnchorKind operates on the parsed diff supplied by
// the caller (don't re-parse — diffs can be large).
func validateAnchorKind(findings []Finding, files []FileDiff) []Finding {
	for i := range findings {
		f := &findings[i]
		if !findingIsInlinePostable(*f) {
			continue
		}
		if strings.TrimSpace(f.Suggestion) == "" {
			continue
		}
		if reason := anchorKindMismatch(*f, files); reason != "" {
			f.Suggestion = ""
			f.SuggestionStrippedReason = reason
		}
	}
	return findings
}

// anchorKindMismatch returns a non-empty reason when the anchor line's kind
// is incompatible with what the comment is asking to change. Returns "" on
// any uncertainty (anchor not found, comment intent indeterminate, etc.) so
// the gate is silent in normal cases.
//
// Exposed for tests; not exported.
func anchorKindMismatch(f Finding, files []FileDiff) string {
	file := FindFile(files, f.Path)
	if file == nil {
		return ""
	}
	h, _ := HunkAroundLine(file, f.Line)
	if h == nil {
		return ""
	}
	_, anchorText := findAnchorLine(h, f.Line)
	ak := classifyAnchorLine(anchorText)
	if ak == kindCode {
		return ""
	}
	suggestionFirst := firstNonBlankLine(f.Suggestion)
	suggestionKind := classifyAnchorLine(suggestionFirst)
	intent := classifyCommentIntent(f.Comment)

	// Comment names a declaration but the anchor isn't on code at all.
	if intent == intentDeclaration && (ak == kindBlank || ak == kindCommentOnly || ak == kindClosingBrace) {
		// Special case: if the suggestion itself is also a comment-only
		// block, the model is plausibly trying to add or fix a leading
		// doc comment near a declaration. Leave it to validateAndPrune /
		// the human in that case.
		if ak == kindCommentOnly && suggestionKind == kindCommentOnly {
			return ""
		}
		return "anchor line is " + anchorKindLabel(ak) + " but comment names a declaration"
	}

	// Comment names a prose fix (typo in a string / log / comment) but the
	// anchor is a bare closing brace. A prose fix cannot live on a `}`.
	if intent == intentProse && ak == kindClosingBrace {
		return "anchor line is just a closing brace but comment names a prose fix"
	}

	return ""
}

// anchorLineKind is the coarse "what is this line" classification used to
// decide if a suggestion at it could plausibly be doing what the comment
// claims.
type anchorLineKind int

const (
	kindCode anchorLineKind = iota
	kindBlank
	kindCommentOnly
	kindClosingBrace
)

func anchorKindLabel(k anchorLineKind) string {
	switch k {
	case kindBlank:
		return "blank"
	case kindCommentOnly:
		return "comment-only"
	case kindClosingBrace:
		return "closing brace"
	}
	return "code"
}

// classifyAnchorLine returns the coarse classification for a single line of
// post-image text. Comment markers covered: `//`, `#`, `--`, `*` (typical
// continuation inside a `/* ... */` block), and `;` (lisps / asm).
//
// "Comment-only" requires no code tokens after the marker — `// foo()` is
// still a comment-only line because applying a suggestion to it would
// replace the comment, not the call; but `x := 1 // note` is code (kindCode)
// because the line has executable content that the suggestion might be
// validly replacing.
func classifyAnchorLine(text string) anchorLineKind {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return kindBlank
	}
	if isClosingBraceOnly(trimmed) {
		return kindClosingBrace
	}
	if isCommentOnly(trimmed) {
		return kindCommentOnly
	}
	return kindCode
}

// closingBraceOnlyRe matches lines whose only non-whitespace content is one
// or more `)`, `]`, `}`, plus optional trailing `,` `;` or `)`. Catches the
// typical block-tail lines (`}`, `})`, `};`, `})`).
var closingBraceOnlyRe = regexp.MustCompile(`^[\)\]\}]+[,;)\s]*$`)

func isClosingBraceOnly(trimmed string) bool {
	return closingBraceOnlyRe.MatchString(trimmed)
}

// commentMarkerPrefixes lists the leading tokens we recognise as starting a
// comment-only line. Order matters only for longest-prefix correctness (`//`
// before `*` so we don't classify `// *` as a star-continuation line).
var commentMarkerPrefixes = []string{"//", "/*", "*/", "--", "#", ";"}

func isCommentOnly(trimmed string) bool {
	// `*` continuation inside a block comment: starts with `*` followed by
	// space or end of line. Common in JSDoc / Javadoc / Go-style block
	// comments.
	if trimmed == "*" || strings.HasPrefix(trimmed, "* ") {
		return true
	}
	for _, p := range commentMarkerPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// commentIntent is the coarse "what is this finding trying to change"
// classification. We only act on the unambiguous categories; intentUnknown
// means "leave it alone".
type commentIntent int

const (
	intentUnknown commentIntent = iota
	intentDeclaration
	intentProse
)

// declarationVerbRe matches the verb half of "the function should be
// renamed" / "the type is missing a comment" / "rename the variable" /
// "use camelCase for ..." style sentences.
var declarationVerbRe = regexp.MustCompile(`(?i)\b(should\s+be|should\s+have|is\s+missing|lacks?|rename|renamed|renaming|use|name|named|naming|convention)\b`)

// declarationNounRe matches the noun half — what kind of code thing the
// comment is about. Kept broad on purpose; the gate triggers only when the
// VERB regex above ALSO matches.
var declarationNounRe = regexp.MustCompile(`(?i)\b(function|func|method|class|type|struct|interface|enum|trait|impl|module|package|namespace|constant|const|variable|var|let|parameter|param|field|property|attribute|endpoint|route|handler|resource|rule|identifier|name)\b`)

// proseFixRe matches "typo in ..." / "wrong spelling of ..." / "fix the
// wording" — comments whose fix is editing English prose somewhere.
var proseFixRe = regexp.MustCompile(`(?i)\b(typo|misspell(ed|ing)?|spelling|grammar|wording|phrasing|wrong\s+word)\b`)

// classifyCommentIntent reads the comment prose and decides which intent
// bucket it falls into. We require BOTH verb and noun to match for
// intentDeclaration, to avoid false positives on prose comments that
// incidentally use the word "function" ("functional tests should ...").
func classifyCommentIntent(comment string) commentIntent {
	body := strings.TrimSpace(comment)
	if body == "" {
		return intentUnknown
	}
	if proseFixRe.MatchString(body) {
		return intentProse
	}
	if declarationVerbRe.MatchString(body) && declarationNounRe.MatchString(body) {
		return intentDeclaration
	}
	return intentUnknown
}

// firstNonBlankLine returns the first non-blank line of s with leading and
// trailing whitespace preserved on that line (so the caller can classify it
// the same way it would classify the anchor line). Returns "" when s is
// empty or blank.
func firstNonBlankLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}
