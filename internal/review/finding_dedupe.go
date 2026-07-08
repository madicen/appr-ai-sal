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

// dedupeInlineFindingsAcrossSpecialists collapses the same inline finding when
// several specialists independently file it on the same diff line. Without this
// a glaring issue (e.g. a wrong unit suffix) posts once per specialist that
// noticed it, spamming the PR with near-identical comments.
//
// It is conservative: findings are grouped by (path, line, side) and, within a
// group, only NEAR-DUPLICATES of the chosen keeper are dropped — a genuinely
// different concern that happens to share a line (formatting nit vs. a security
// issue) survives. PR-wide findings (path "", line 0) are never touched.
//
// The keeper is chosen by lane ownership: a fixed specialist priority
// (formatting, design, security, testing, docs, then the PR agents), tie-broken
// by carrying a one-click suggestion, then by higher severity, then by stable
// order. Runs before the arbiter/vibe-coach so every surface and the GitHub
// post see the de-duplicated set.
func dedupeInlineFindingsAcrossSpecialists(specs []SpecialistResult) []SpecialistResult {
	groups := map[string][]dedupeRef{}
	var order []string
	for si := range specs {
		if specs[si].Err != nil {
			continue
		}
		for fi := range specs[si].Findings {
			f := specs[si].Findings[fi]
			if !findingIsInlinePostable(f) {
				continue
			}
			key := dedupeKey(f)
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], dedupeRef{specIdx: si, findIdx: fi})
		}
	}

	drop := map[dedupeRef]bool{}
	for _, key := range order {
		refs := groups[key]
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
			// Take a pointer so we can carry the highest severity of the
			// merged near-duplicates onto the keeper: a security "error"
			// absorbed under a lower-severity keeper must never be quietly
			// downgraded by the merge.
			kf := &specs[keeper.specIdx].Findings[keeper.findIdx]
			var next []dedupeRef
			for i, r := range remaining {
				if i == best {
					continue
				}
				rf := specs[r.specIdx].Findings[r.findIdx]
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

// dedupeKey groups findings by diff location, independent of specialist, so
// near-duplicates filed on the same line by different specialists collapse.
// It is the specialist-independent Location form of the unified FindingKey.
func dedupeKey(f Finding) string {
	return findingkey.New("", f.Path, f.Line, f.Side).Location()
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
