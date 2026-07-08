package review

import (
	"strings"
	"unicode"

	"github.com/madicen/appr-ai-sal/internal/review/findingkey"
)

// dedupeRef points at one finding inside a []SpecialistResult.
type dedupeRef struct {
	specIdx int
	findIdx int
}

// adjacentDedupeWindow is the ±line window (Q6.4) within which two
// near-duplicate inline findings on the SAME (path, side) collapse. Models
// routinely anchor the same concern one or two lines apart (the top of a
// block vs. the offending statement inside it); an exact-line-only match let
// those double-post. Two lines is tight enough that genuinely distinct issues
// a few lines apart still survive (the Jaccard test must ALSO agree).
const adjacentDedupeWindow = 2

// dedupeInlineFindingsAcrossSpecialists collapses the same finding when
// several specialists independently file it. Without this a glaring issue
// (e.g. a wrong unit suffix) posts once per specialist that noticed it,
// spamming the PR with near-identical comments.
//
// It handles two classes:
//
//   - Inline findings are grouped by (path, side) and, within a group, only
//     NEAR-DUPLICATES within ±adjacentDedupeWindow lines of the chosen keeper
//     are dropped (Q6.4 widened the window from an exact-line match). A
//     genuinely different concern that happens to share (or sit near) a line —
//     a formatting nit vs. a security issue — survives because the Jaccard
//     duplicate test must also agree.
//   - PR-wide findings (path "", line 0) are deduped across specialists too
//     (Q6.4): description and scope routinely file the same "this PR does two
//     things" note, which previously double-posted because PR-wide findings
//     were never touched.
//
// The keeper is chosen by lane ownership: a fixed specialist priority
// (security first, formatting, design, testing, docs, tech, then the PR
// agents), tie-broken by carrying a one-click suggestion, then by higher
// severity, then by stable order. Runs before the arbiter/vibe-coach so every
// surface and the GitHub post see the de-duplicated set.
func dedupeInlineFindingsAcrossSpecialists(specs []SpecialistResult) []SpecialistResult {
	drop := map[dedupeRef]bool{}
	collectDedupeGroups(specs, findingIsInlinePostable, dedupeColumnKey, true, drop)
	collectDedupeGroups(specs, findingIsPRWide, dedupePRWideKey, false, drop)
	if len(drop) == 0 {
		return specs
	}
	for si := range specs {
		if specs[si].Err != nil {
			continue
		}
		kept := make([]Finding, 0, len(specs[si].Findings))
		for fi := range specs[si].Findings {
			if drop[dedupeRef{specIdx: si, findIdx: fi}] {
				continue
			}
			kept = append(kept, specs[si].Findings[fi])
		}
		specs[si].Findings = kept
	}
	return specs
}

// collectDedupeGroups groups the findings selected by include under key and,
// within each group, marks near-duplicates of the chosen keeper for dropping.
// When lineWindowed is true (inline findings) two findings only merge if their
// lines are within adjacentDedupeWindow; PR-wide findings (line 0) ignore the
// window. The highest severity of any merged near-duplicate is carried onto
// the keeper so a merge never silently downgrades a more-severe report.
func collectDedupeGroups(specs []SpecialistResult, include func(Finding) bool, key func(Finding) string, lineWindowed bool, drop map[dedupeRef]bool) {
	groups := map[string][]dedupeRef{}
	var order []string
	for si := range specs {
		if specs[si].Err != nil {
			continue
		}
		for fi := range specs[si].Findings {
			f := specs[si].Findings[fi]
			if !include(f) {
				continue
			}
			k := key(f)
			if _, ok := groups[k]; !ok {
				order = append(order, k)
			}
			groups[k] = append(groups[k], dedupeRef{specIdx: si, findIdx: fi})
		}
	}
	for _, k := range order {
		refs := groups[k]
		if len(refs) < 2 {
			continue
		}
		remaining := append([]dedupeRef(nil), refs...)
		for len(remaining) > 0 {
			best := 0
			for i := 1; i < len(remaining); i++ {
				if dedupeRefBetterKeeper(specs, remaining[i], remaining[best]) {
					best = i
				}
			}
			keeper := remaining[best]
			kf := &specs[keeper.specIdx].Findings[keeper.findIdx]
			var next []dedupeRef
			for i, r := range remaining {
				if i == best {
					continue
				}
				rf := specs[r.specIdx].Findings[r.findIdx]
				if lineWindowed && abs(kf.Line-rf.Line) > adjacentDedupeWindow {
					next = append(next, r)
					continue
				}
				if findingsLikelyDuplicate(*kf, rf) {
					if severityRank(rf.Severity) > severityRank(kf.Severity) {
						kf.Severity = rf.Severity
					}
					drop[r] = true
				} else {
					next = append(next, r)
				}
			}
			remaining = next
		}
	}
}

// findingIsPRWide reports whether f is a PR-wide / general finding (no inline
// anchor) that still carries actionable prose.
func findingIsPRWide(f Finding) bool {
	if findingIsInlinePostable(f) {
		return false
	}
	return strings.TrimSpace(f.Comment) != ""
}

// dedupeColumnKey groups inline findings by (path, side) — independent of
// line, so the ±adjacentDedupeWindow merge can span nearby lines — and
// independent of specialist, so cross-specialist near-duplicates collapse.
func dedupeColumnKey(f Finding) string {
	return findingkey.New("", f.Path, 0, f.Side).Location()
}

// dedupePRWideKey groups PR-wide findings by side only (path is empty, line 0)
// so the same whole-PR note filed by different agents lands in one group.
func dedupePRWideKey(f Finding) string {
	return findingkey.New("", "", 0, f.Side).Location()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// dedupeRefBetterKeeper reports whether a is a better finding to keep than b:
// lane priority first (lower index wins), then prefer one carrying a
// suggestion, then higher severity, then stable order.
func dedupeRefBetterKeeper(specs []SpecialistResult, a, b dedupeRef) bool {
	pa := specialistLanePriority(specs[a.specIdx].Specialist)
	pb := specialistLanePriority(specs[b.specIdx].Specialist)
	if pa != pb {
		return pa < pb
	}
	fa := specs[a.specIdx].Findings[a.findIdx]
	fb := specs[b.specIdx].Findings[b.findIdx]
	sa := strings.TrimSpace(fa.Suggestion) != ""
	sb := strings.TrimSpace(fb.Suggestion) != ""
	if sa != sb {
		return sa
	}
	if ra, rb := severityRank(fa.Severity), severityRank(fb.Severity); ra != rb {
		return ra > rb
	}
	if a.specIdx != b.specIdx {
		return a.specIdx < b.specIdx
	}
	return a.findIdx < b.findIdx
}

// findingsLikelyDuplicate decides whether two same-line findings are the same
// concern. It is deliberately strict so distinct issues on one line are not
// collapsed: an identical (whitespace-normalised) suggestion is a strong
// signal, otherwise the comments must overlap heavily by word set.
func findingsLikelyDuplicate(a, b Finding) bool {
	sa := collapseWhitespace(a.Suggestion)
	sb := collapseWhitespace(b.Suggestion)
	if sa != "" && sa == sb {
		return true
	}
	return commentJaccard(a.Comment, b.Comment) >= 0.6
}

// commentJaccard is the Jaccard similarity of the two comments' lowercased
// word sets (0 when either is empty).
func commentJaccard(a, b string) float64 {
	ta := wordSet(a)
	tb := wordSet(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for w := range ta {
		if tb[w] {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out[w] = true
	}
	return out
}
