package review

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// formatting_evidence.go harvests repo-grounding evidence for FORMATTING
// findings so the convention witness (Q6.5) can judge whether a formatting
// finding asks the author to follow a convention the rest of the repo
// actually uses — or to buck it. It generalizes tech_evidence.go's
// sibling-file token-counting to two formatting-relevant signals:
//
//  1. Token presence: for each backtick-quoted identifier a finding
//     references, how many sampled sibling files of the SAME extension
//     already contain it (identical to the tech harvester's counting).
//  2. Identifier-style census: the dominant identifier casing style
//     (snake_case / camelCase / PascalCase) across the sampled siblings. A
//     naming-convention finding ("rename to snake_case") is congruent with
//     the repo only when the siblings actually favour that style; when they
//     overwhelmingly use another, the finding is divergent from the repo norm.
//
// Bounds mirror the tech harvester (techEvidenceMax* constants) so even a
// large repo returns quickly.

const (
	// formattingEvidenceMaxFindings caps how many formatting findings get an
	// evidence block, matching the tech harvester's per-finding cap.
	formattingEvidenceMaxFindings = 12
	// formattingIdentifierSampleCap bounds how many identifiers are read from
	// each sampled sibling file for the style census.
	formattingIdentifierSampleCap = 400
)

// identifierRe matches a source identifier (letters/digits/underscore, not
// starting with a digit). Used for the naming-style census.
var identifierRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// BuildFormattingConventionEvidence harvests sibling-file evidence for
// formatting findings. For each formatting finding anchored to a file it
// samples same-extension siblings and reports (a) how many contain each
// backtick identifier the comment references and (b) the dominant identifier
// casing style across the siblings.
//
// Returns "" when there are no formatting findings anchored to a file, or when
// no siblings could be sampled (the witness then falls back to `unknown`).
func BuildFormattingConventionEvidence(worktree string, findings []Finding) string {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" || len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	cache := map[string][]string{} // ext|searchRoot → sampled sibling paths
	rendered := 0
	for _, f := range findings {
		if rendered >= formattingEvidenceMaxFindings {
			break
		}
		path := strings.TrimSpace(f.Path)
		if path == "" || f.Line <= 0 {
			continue
		}
		searchRoot, siblings := sampleSiblingsCached(worktree, path, cache)
		if len(siblings) == 0 {
			continue
		}
		if rendered == 0 {
			b.WriteString("_Formatting convention evidence (auto-harvested sibling sampling)._\n\n")
		}
		rendered++
		side := f.Side
		if side == "" {
			side = "RIGHT"
		}
		fmt.Fprintf(&b, "- Finding `%s:%d` side=%s (formatting): sampled %d sibling `%s` file(s) near `%s`.\n",
			path, f.Line, side, len(siblings), fileExt(path), searchRoot)
		style, snake, camel, pascal := identifierStyleCensus(worktree, siblings)
		if style != "" {
			fmt.Fprintf(&b, "  - dominant identifier style in siblings: **%s** (snake_case=%d, camelCase=%d, PascalCase=%d).\n",
				style, snake, camel, pascal)
		}
		for _, tok := range extractBacktickIdentifiers(f.Comment) {
			if len(tok) < 3 || isAllDigits(tok) {
				continue
			}
			present := countFilesContaining(worktree, siblings, tok)
			fmt.Fprintf(&b, "  - token `%s`: present in %d of %d sampled file(s).\n", tok, present, len(siblings))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// appendFormattingConventionEvidence folds harvested formatting evidence into
// the shared per-PR evidence pack handed to the convention witness, mirroring
// appendTechConventionEvidence. Returns the pack unchanged when there is no
// formatting evidence.
func appendFormattingConventionEvidence(evidence, worktree string, formattingFindings []Finding) string {
	fmtEv := BuildFormattingConventionEvidence(worktree, formattingFindings)
	if strings.TrimSpace(fmtEv) == "" {
		return evidence
	}
	if strings.TrimSpace(evidence) == "" {
		return fmtEv
	}
	return strings.TrimRight(evidence, "\n") + "\n\n" + fmtEv
}

// identifierStyleCensus samples identifiers across the sibling files and
// classifies each as snake_case, camelCase, or PascalCase, returning the
// dominant style label ("" when no classifiable identifiers were seen) and the
// three counts. Identifiers that fit none of the three (all-caps constants,
// single lowercase words) are ignored so the dominant style reflects
// multi-token naming habits, which is what a naming-convention finding is about.
func identifierStyleCensus(worktree string, siblings []string) (dominant string, snake, camel, pascal int) {
	for _, rel := range siblings {
		content := readFileCapped(filepath.Join(worktree, filepath.FromSlash(rel)), techEvidencePerFileRead)
		if content == "" {
			continue
		}
		ids := identifierRe.FindAllString(content, formattingIdentifierSampleCap)
		for _, id := range ids {
			switch classifyIdentifierStyle(id) {
			case idStyleSnake:
				snake++
			case idStyleCamel:
				camel++
			case idStylePascal:
				pascal++
			}
		}
	}
	dominant = dominantStyle(snake, camel, pascal)
	return dominant, snake, camel, pascal
}

type identifierStyle int

const (
	idStyleOther identifierStyle = iota
	idStyleSnake
	idStyleCamel
	idStylePascal
)

// classifyIdentifierStyle labels an identifier by its multi-token casing
// convention. Single-token words (no underscore, no internal case change) and
// all-caps constants are idStyleOther so they don't bias the census.
func classifyIdentifierStyle(id string) identifierStyle {
	if id == "" {
		return idStyleOther
	}
	hasUnderscore := strings.Contains(strings.Trim(id, "_"), "_")
	hasUpper := strings.IndexFunc(id, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0
	hasLower := strings.IndexFunc(id, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
	if hasUnderscore && !hasUpper {
		return idStyleSnake // multi-token lower-snake
	}
	if hasUnderscore {
		return idStyleOther // mixed / SCREAMING_SNAKE — not a camel/pascal signal
	}
	first := rune(id[0])
	if first >= 'A' && first <= 'Z' && hasLower {
		return idStylePascal
	}
	if first >= 'a' && first <= 'z' && hasUpper {
		return idStyleCamel
	}
	return idStyleOther
}

// dominantStyle returns the label of the strictly-largest count, or "" on a
// tie or all-zero (no confident signal).
func dominantStyle(snake, camel, pascal int) string {
	type sc struct {
		name  string
		count int
	}
	all := []sc{{"snake_case", snake}, {"camelCase", camel}, {"PascalCase", pascal}}
	sort.SliceStable(all, func(i, j int) bool { return all[i].count > all[j].count })
	if all[0].count == 0 {
		return ""
	}
	if all[0].count == all[1].count {
		return "" // tie — no confident dominant style
	}
	return all[0].name
}
