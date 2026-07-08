package review

import (
	"path/filepath"
	"regexp"
	"strings"
)

// static_silence.go implements Q5.d: "the linter is silent" as a false-positive
// signal. When the static-analysis pre-pass ran a formatter/linter over a file
// and it passed CLEAN, that file's mechanical formatting is already correct —
// so a hand-rolled whitespace / indentation / gofmt-style finding on it is
// almost certainly noise. The gate demotes such findings to info (which the
// strictness floor then filters out under balanced/strict) and records why, so
// the TUI can explain the downgrade.
//
// It is deliberately conservative on two axes so it never silences a genuine
// finding:
//   - it only touches specs marked FormatterSilenceAware (the formatting
//     specialist), never security/design/testing/docs/tech;
//   - it only touches findings whose comment reads as a formatting-mechanics
//     nit (matched by formattingMechanicsRe). A formatting-lane finding about
//     naming inconsistency, a magic literal, or readability is left alone — a
//     clean formatter says nothing about those.

// formattingMechanicsRe matches comments about mechanical formatting a
// formatter would fix: whitespace, indentation, alignment, blank lines,
// trailing space, tabs vs spaces, semicolons/braces spacing, gofmt/format
// directives, line length/wrapping.
var formattingMechanicsRe = regexp.MustCompile(`(?i)\b(gofmt|go fmt|reformat|re-?format|formatting|indent(?:ation|ed|s)?|whitespace|white space|blank line|empty line|trailing (?:space|whitespace|newline)|tabs?(?: vs\.? spaces)?|spaces?(?: vs\.? tabs)?|alignment|aligned?|mis-?aligned|line length|line too long|wrap(?:ping)? this line|spacing|semicolons?)\b`)

// clean-file paths are compared case- and separator-normalised.
func silencePathKey(p string) string {
	return strings.ToLower(filepath.ToSlash(strings.TrimSpace(p)))
}

// downgradeFormatterSilencedFindings demotes formatting-mechanics findings that
// land on a file a formatter passed clean in the pre-pass. cleanFiles is the
// set from staticpass.Result.FormatterCleanFiles (forward-slashed paths).
// Returns the same slice for ergonomics; a no-op when the spec is not
// silence-aware, when cleanFiles is empty, or when nothing matches.
func downgradeFormatterSilencedFindings(name string, findings []Finding, cleanFiles map[string]bool) []Finding {
	if len(cleanFiles) == 0 || !specFormatterSilenceAware(name) {
		return findings
	}
	clean := make(map[string]bool, len(cleanFiles))
	for p := range cleanFiles {
		clean[silencePathKey(p)] = true
	}
	const note = "the formatter/linter pre-pass reported this file clean, so a mechanical formatting nit here is likely a false positive"
	for i := range findings {
		f := &findings[i]
		if strings.TrimSpace(f.Path) == "" {
			continue // PR-wide findings have no file to check against
		}
		if !clean[silencePathKey(f.Path)] {
			continue
		}
		if !formattingMechanicsRe.MatchString(f.Comment) {
			continue
		}
		switch f.Severity {
		case SeverityWarning, SeverityError, SeverityCritical:
			f.Severity = SeverityInfo
		}
		if f.ActionabilityNote == "" {
			f.ActionabilityNote = note
		}
		// Drop a one-click suggestion too: applying a whitespace fix a
		// formatter already rejected as unnecessary would be noise.
		if strings.TrimSpace(f.Suggestion) != "" {
			f.Suggestion = ""
			if f.SuggestionStrippedReason == "" {
				f.SuggestionStrippedReason = note
			}
		}
	}
	return findings
}
