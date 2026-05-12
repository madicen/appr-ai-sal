package langagents

import (
	"sort"
	"strings"
)

// Brief is one language's contribution to a specialist's prompt. The
// runner concatenates Briefs in the order returned by BriefsForDiff
// (most touched first) and wraps the result in a section header.
type Brief struct {
	Language Language
	Body     string
	// Touches counts the number of lines (added + deleted) in the diff
	// attributed to this language. Surfaced for TUI / progress events
	// and for the runner to decide whether to warn about missing
	// briefs ("the PR has 412 .swift lines but no Swift brief").
	Touches int
}

// MaxBriefsPerReview caps how many language briefs the runner injects
// into a single specialist prompt. Two is the cap because most PRs are
// dominantly one or two languages; injecting more wastes tokens on
// briefs the specialist will not exercise.
const MaxBriefsPerReview = 2

// MinTouchesToInject is the smallest line count for a secondary
// language to earn a brief slot. Tiny ".yml" touches in a Go PR
// shouldn't pull in the YAML brief and crowd out the Go one.
const MinTouchesToInject = 5

// BriefsForDiff inspects the file paths and line counts in a unified
// diff (parsed as touchedPaths -> touchCount) and returns the language
// briefs that should be injected, capped at MaxBriefsPerReview.
//
// Selection rule:
//
//  1. Bucket touched lines by canonical language.
//  2. Sort languages descending by touch count (alphabetical tiebreak).
//  3. Skip languages with no cached brief; report them in `missing` so
//     the caller can warn the user and offer to generate.
//  4. Apply MinTouchesToInject to secondary languages so a few stray
//     YAML touches don't displace the dominant language's brief.
//
// touchesByPath maps file path -> number of changed lines (added +
// removed). Callers can compute this from review.ParseDiff; we keep
// langagents free of any diff-parsing import so the package stays a
// leaf in the import graph.
func BriefsForDiff(touchesByPath map[string]int) (briefs []Brief, missing []Language) {
	if len(touchesByPath) == 0 {
		return nil, nil
	}
	byLang := map[Language]int{}
	for path, n := range touchesByPath {
		c := LanguageForPath(path)
		if c == "" {
			continue
		}
		byLang[c] += n
	}
	if len(byLang) == 0 {
		return nil, nil
	}
	cache, _ := LoadCache()
	type entry struct {
		Lang    Language
		Touches int
	}
	entries := make([]entry, 0, len(byLang))
	for l, t := range byLang {
		entries = append(entries, entry{l, t})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Touches != entries[j].Touches {
			return entries[i].Touches > entries[j].Touches
		}
		return entries[i].Lang < entries[j].Lang
	})
	for _, e := range entries {
		if len(briefs) > 0 && e.Touches < MinTouchesToInject {
			// Secondary language with very few touches; skip rather
			// than displace the dominant brief.
			continue
		}
		body, ok := resolveBrief(e.Lang, cache)
		if !ok {
			missing = append(missing, e.Lang)
			continue
		}
		briefs = append(briefs, Brief{
			Language: e.Lang,
			Body:     body,
			Touches:  e.Touches,
		})
		if len(briefs) >= MaxBriefsPerReview {
			break
		}
	}
	return briefs, missing
}

// resolveBrief looks up the cached brief for lang. Returns the trimmed
// body and ok=true when the cache holds a non-empty entry; otherwise
// empty/false. cache may be nil (treated as empty).
func resolveBrief(lang Language, cache *LangAgents) (string, bool) {
	if cache == nil {
		return "", false
	}
	if a, ok := cache.Get(lang); ok && strings.TrimSpace(a.Context) != "" {
		return strings.TrimSpace(a.Context), true
	}
	return "", false
}

// FormatBriefsSection renders a list of Briefs as the user-prompt
// section that gets injected into specialist prompts. Returns "" when
// briefs is empty so callers can drop the section header cleanly.
func FormatBriefsSection(briefs []Brief) string {
	if len(briefs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Language conventions\n\n")
	b.WriteString("The brief(s) below describe how each language is conventionally written. ")
	b.WriteString("They are repo-independent: the repo agent (if any) lists deltas from these defaults. ")
	b.WriteString("Do not file findings that contradict the language conventions stated here.\n\n")
	for i, br := range briefs {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		// Strip the brief's own H1 header (if any) to avoid double-stacking
		// under the section heading; keep the rest verbatim.
		body := stripLeadingH1(br.Body)
		b.WriteString("### ")
		b.WriteString(LabelFor(br.Language))
		b.WriteString("\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// stripLeadingH1 removes a leading `# ...` heading line plus any blank
// line that follows it. We compose briefs under our own section header,
// so the brief's own top-of-file title would render as a redundant
// nested heading.
func stripLeadingH1(body string) string {
	body = strings.TrimLeft(body, "\n")
	if !strings.HasPrefix(body, "# ") {
		return body
	}
	idx := strings.Index(body, "\n")
	if idx < 0 {
		return ""
	}
	rest := body[idx+1:]
	return strings.TrimLeft(rest, "\n")
}
