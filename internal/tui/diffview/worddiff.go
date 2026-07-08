package diffview

// worddiff.go implements intra-line word-level diffing: given a removed line
// and the added line that replaced it, it computes which token spans actually
// changed so the renderer can emphasise just those spans instead of painting
// the whole line as changed.
//
// The algorithm is a classic longest-common-subsequence (LCS) over tokens.
// Tokens are runs of "word" characters (letters, digits, underscore) or single
// non-word characters, with whitespace kept as its own tokens, so that
// `foo(bar)` → `foo(baz)` highlights only `bar`→`baz` and not the parens. This
// is the standard "git --word-diff" style at a coarse granularity, which is
// plenty for a review diff.

// Seg is one span of a line tagged with whether it differs from the paired
// line. Adjacent segments with the same Changed flag are merged so the
// renderer emits the fewest styled runs.
type Seg struct {
	Text    string
	Changed bool
}

// WordDiff computes the changed spans between a removed line (old) and the
// added line (new) that replaced it. It returns the segmentation of each side:
// oldSegs covers old, newSegs covers new, and concatenating the Text of either
// side reproduces that side's input exactly. Segments whose Changed is true are
// the parts unique to that side (a deletion on the old side, an insertion on
// the new side); Changed=false segments are the common tokens.
//
// When either side is empty the whole non-empty side is a single changed
// segment. Identical inputs produce a single unchanged segment on each side.
func WordDiff(old, new string) (oldSegs, newSegs []Seg) {
	if old == new {
		return []Seg{{Text: old}}, []Seg{{Text: new}}
	}
	if old == "" {
		return nil, []Seg{{Text: new, Changed: true}}
	}
	if new == "" {
		return []Seg{{Text: old, Changed: true}}, nil
	}
	a := tokenize(old)
	b := tokenize(new)
	// lcs[i][j] = length of the LCS of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	// Walk the table: matched tokens are common (unchanged), unmatched tokens
	// on the old side are deletions, on the new side insertions.
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			oldSegs = appendSeg(oldSegs, a[i], false)
			newSegs = appendSeg(newSegs, b[j], false)
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			oldSegs = appendSeg(oldSegs, a[i], true)
			i++
		default:
			newSegs = appendSeg(newSegs, b[j], true)
			j++
		}
	}
	for ; i < len(a); i++ {
		oldSegs = appendSeg(oldSegs, a[i], true)
	}
	for ; j < len(b); j++ {
		newSegs = appendSeg(newSegs, b[j], true)
	}
	return oldSegs, newSegs
}

// appendSeg appends tok to segs, merging into the last segment when the
// Changed flag matches so runs of same-state tokens collapse into one Seg.
func appendSeg(segs []Seg, tok string, changed bool) []Seg {
	if n := len(segs); n > 0 && segs[n-1].Changed == changed {
		segs[n-1].Text += tok
		return segs
	}
	return append(segs, Seg{Text: tok, Changed: changed})
}

// tokenize splits s into word tokens (maximal runs of letters/digits/_) and
// single-character tokens for everything else, so word-boundary changes are
// isolated. Whitespace runs are collapsed into individual tokens too, which
// keeps indentation churn out of the highlighted spans.
func tokenize(s string) []string {
	var out []string
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		if isWordRune(runes[i]) {
			j := i
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			out = append(out, string(runes[i:j]))
			i = j
			continue
		}
		out = append(out, string(runes[i]))
		i++
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
