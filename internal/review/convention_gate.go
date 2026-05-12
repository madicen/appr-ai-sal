package review

import (
	"regexp"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/review/langagents"
)

// validateNamingConvention demotes findings whose comment recommends a
// naming convention that is wrong for the file's language, and clears the
// associated Suggestion. The reference failure is the screenshot's
// "function name should be in snake_case according to naming conventions"
// applied to a `.go` file — Go uses MixedCaps for both functions and types,
// and the model's claim is simply wrong about Go.
//
// The expected-convention data lives in langagents.Table — the SAME table
// that backs the language-brief prompt injected into specialists. Keeping
// both consumers reading one source prevents drift between "what we tell
// the model" and "what we silently strip."
//
// This gate complements validateAnchorKind: anchor-kind catches "the line
// being replaced makes no sense"; convention catches "the rule being cited
// makes no sense for this language." Either one would have killed the
// screenshot's bad suggestion; together they handle a wider class of
// cross-language confusions (e.g. "rename to camelCase" applied to a
// Python module, "use snake_case" applied to a Java class, etc.).
//
// The gate is intentionally conservative:
//   - Acts only when BOTH the recommended convention and the file's
//     expected convention for the target kind (function / type / variable)
//     are unambiguous from languageNamingConvention.
//   - Demotes severity to info instead of dropping the finding outright —
//     the prose may still be useful to a human reviewer, and we record
//     why on ActionabilityNote.
//   - Clears Suggestion (and records SuggestionStrippedReason) because any
//     one-click fix the model emitted is built on the wrong premise.
//
// Operates on findings in place; returns the same slice for ergonomics.
func validateNamingConvention(findings []Finding) []Finding {
	for i := range findings {
		f := &findings[i]
		if strings.TrimSpace(f.Path) == "" {
			continue
		}
		lang := langagents.LanguageForPath(f.Path)
		conv, ok := langagents.Table[lang]
		if !ok {
			continue
		}
		rec, kind := extractRecommendedConvention(f.Comment)
		if rec == "" {
			continue
		}
		expected := conv.ForKind(kind)
		if expected == "" || expected == rec {
			continue
		}
		note := "convention mismatch: " + langagents.LabelFor(lang) + " uses " + expected
		if kind != "" {
			note += " for " + kind + "s"
		}
		note += ", finding recommends " + rec
		switch f.Severity {
		case SeverityWarning, SeverityError, SeverityCritical:
			f.Severity = SeverityInfo
		}
		f.ActionabilityNote = note
		if strings.TrimSpace(f.Suggestion) != "" {
			f.Suggestion = ""
			f.SuggestionStrippedReason = note
		}
	}
	return findings
}

// recommendedConventionRe matches "should be (in) <convention>" / "use
// <convention>" / "rename to <convention>" / "<convention> per naming
// conventions" phrasings. Captures the convention name on group 1.
//
// We deliberately do NOT match "follow naming conventions" without a named
// style — that's too vague to act on.
var recommendedConventionRe = regexp.MustCompile(`(?i)\b(?:should\s+be(?:\s+in)?|use|rename(?:d)?\s+to|named\s+(?:in\s+)?|follow(?:s|ing)?(?:\s+the)?(?:\s+\w+)?\s+)\s*(snake_case|camelCase|PascalCase|UpperCamelCase|MixedCaps|kebab-case|SCREAMING_SNAKE_CASE|SCREAMING_KEBAB_CASE)\b`)

// looseConventionRe is a fallback that captures the convention name when
// the sentence shape doesn't fit recommendedConventionRe but the name
// itself appears alongside a clearly-prescriptive word like "convention" or
// "naming." Conservative on purpose to keep false positives down.
var looseConventionRe = regexp.MustCompile(`(?i)(snake_case|camelCase|PascalCase|UpperCamelCase|MixedCaps|kebab-case|SCREAMING_SNAKE_CASE|SCREAMING_KEBAB_CASE)\b[^\n.]*\b(convention|conventions|naming|style)\b`)

// kindHintRe maps surrounding words to one of the three kinds the convention
// table tracks. We only consult this when extractRecommendedConvention finds
// a recommendation; otherwise we never read these.
var (
	funcHintRe = regexp.MustCompile(`(?i)\b(function|func|method|handler|callback)\b`)
	typeHintRe = regexp.MustCompile(`(?i)\b(type|class|struct|interface|enum|trait)\b`)
	varHintRe  = regexp.MustCompile(`(?i)\b(variable|var|field|property|attribute|constant|const|parameter|param|argument)\b`)
)

// extractRecommendedConvention scans comment for "should be snake_case" /
// "use camelCase" / "rename to PascalCase" / "PascalCase per naming
// conventions" patterns and returns the canonical convention name plus the
// kind hint (one of "function", "type", "variable", or "" for unspecified).
// Returns ("", "") when nothing matches.
//
// Convention names are canonicalised:
//   - "UpperCamelCase" -> "PascalCase"
//
// Everything else returns as-found.
func extractRecommendedConvention(comment string) (convention, kind string) {
	body := strings.TrimSpace(comment)
	if body == "" {
		return "", ""
	}
	if m := recommendedConventionRe.FindStringSubmatch(body); len(m) >= 2 {
		convention = canonConvention(m[1])
	} else if m := looseConventionRe.FindStringSubmatch(body); len(m) >= 2 {
		convention = canonConvention(m[1])
	} else {
		return "", ""
	}
	switch {
	case funcHintRe.MatchString(body):
		kind = "function"
	case typeHintRe.MatchString(body):
		kind = "type"
	case varHintRe.MatchString(body):
		kind = "variable"
	}
	return convention, kind
}

func canonConvention(s string) string {
	switch s {
	case "UpperCamelCase":
		return "PascalCase"
	}
	return s
}
