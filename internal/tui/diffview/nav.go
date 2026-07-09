package diffview

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// nav.go holds the two navigation indexes for the diff pane: jumping between
// inline finding-tag anchors (n/p) and stepping through in-diff search matches
// (/ then n/N). Both operate over rendered viewport rows so the caller only has
// to set the viewport's YOffset to the returned row.

// AnchorIndex maps finding anchors to the viewport rows they render on, in
// ascending row order, and supports next/previous navigation relative to the
// current scroll position. It is built once per diff render (BuildAnchorIndex)
// and queried by the n/p key handlers.
type AnchorIndex struct {
	rows []int
}

// BuildAnchorIndex sorts and de-duplicates the given rendered row numbers (the
// viewport rows where inline finding tags appear) into a navigable index.
func BuildAnchorIndex(rows []int) AnchorIndex {
	if len(rows) == 0 {
		return AnchorIndex{}
	}
	cp := append([]int(nil), rows...)
	sort.Ints(cp)
	out := cp[:0]
	prev := -1
	for _, r := range cp {
		if r == prev {
			continue
		}
		out = append(out, r)
		prev = r
	}
	return AnchorIndex{rows: append([]int(nil), out...)}
}

// Len reports how many distinct anchors are indexed.
func (a AnchorIndex) Len() int { return len(a.rows) }

// Rows returns the sorted anchor rows (copy) for callers that need the raw set.
func (a AnchorIndex) Rows() []int { return append([]int(nil), a.rows...) }

// Next returns the row of the first anchor strictly below current, and ok=false
// when there is none (already at/after the last anchor). current is the
// viewport's top row (YOffset).
func (a AnchorIndex) Next(current int) (int, bool) {
	for _, r := range a.rows {
		if r > current {
			return r, true
		}
	}
	return 0, false
}

// Prev returns the row of the last anchor strictly above current, and ok=false
// when there is none.
func (a AnchorIndex) Prev(current int) (int, bool) {
	for i := len(a.rows) - 1; i >= 0; i-- {
		if a.rows[i] < current {
			return a.rows[i], true
		}
	}
	return 0, false
}

// SearchIndex records every viewport row whose (ANSI-stripped) text contains
// the query, in ascending order, and supports wrap-around next/previous
// stepping. It is rebuilt whenever the query changes (BuildSearchIndex).
type SearchIndex struct {
	query string
	rows  []int
}

// BuildSearchIndex scans lines (the rendered diff, one string per row) for a
// case-insensitive substring match against query and records the matching row
// numbers. ANSI escape sequences in each line are stripped before matching so a
// syntax-highlighted line still matches its plain text. An empty query yields
// an empty index.
func BuildSearchIndex(lines []string, query string) SearchIndex {
	q := strings.TrimSpace(query)
	if q == "" {
		return SearchIndex{}
	}
	needle := strings.ToLower(q)
	var rows []int
	for i, ln := range lines {
		if strings.Contains(strings.ToLower(ansi.Strip(ln)), needle) {
			rows = append(rows, i)
		}
	}
	return SearchIndex{query: q, rows: rows}
}

// Query returns the query the index was built for.
func (s SearchIndex) Query() string { return s.query }

// Count returns how many rows matched.
func (s SearchIndex) Count() int { return len(s.rows) }

// Rows returns the matching row numbers (copy).
func (s SearchIndex) Rows() []int { return append([]int(nil), s.rows...) }

// Next returns the first match at or below `from`, wrapping to the first match
// when there is none below. ok is false only when there are no matches at all.
// `from` is typically the current viewport top row; passing current+1 steps to
// the following match.
func (s SearchIndex) Next(from int) (int, bool) {
	if len(s.rows) == 0 {
		return 0, false
	}
	for _, r := range s.rows {
		if r >= from {
			return r, true
		}
	}
	return s.rows[0], true // wrap
}

// Prev returns the last match at or above `from`, wrapping to the last match
// when there is none above. ok is false only when there are no matches.
func (s SearchIndex) Prev(from int) (int, bool) {
	if len(s.rows) == 0 {
		return 0, false
	}
	for i := len(s.rows) - 1; i >= 0; i-- {
		if s.rows[i] <= from {
			return s.rows[i], true
		}
	}
	return s.rows[len(s.rows)-1], true // wrap
}

// MatchLine reports whether the ANSI-stripped line contains query
// (case-insensitive). Exposed so the renderer can decide per-row whether to
// paint the match-highlight background without rebuilding the whole index.
func MatchLine(line, query string) bool {
	q := strings.TrimSpace(query)
	if q == "" {
		return false
	}
	return strings.Contains(strings.ToLower(ansi.Strip(line)), strings.ToLower(q))
}
