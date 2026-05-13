package review

import (
	"regexp"
	"strconv"
	"strings"
)

// FileDiff is a structured view of one file in a unified diff.
type FileDiff struct {
	Path      string  // post-image path (the path findings refer to with side="RIGHT")
	OldPath   string  // pre-image path; equal to Path unless renamed
	IsBinary  bool    // diff was a "Binary files differ" stanza
	IsNewFile bool    // appears as new file (a -> /dev/null reversed)
	IsDeleted bool    // appears as deleted file
	Additions int     // count of "+" lines (excluding "+++" headers)
	Deletions int     // count of "-" lines (excluding "---" headers)
	Hunks     []Hunk  // hunks in document order
	Header    string  // raw "diff --git" line, useful when rendering full file
}

// Hunk is one @@ section of a unified diff.
type Hunk struct {
	Header  string     // raw "@@ -a,b +c,d @@ ..." line
	OldLine int        // first line in the pre-image (a)
	NewLine int        // first line in the post-image (c)
	Lines   []DiffLine // every line inside the hunk, including context
}

// DiffLine is one line inside a hunk.
type DiffLine struct {
	Kind  DiffLineKind
	OldNo int    // 0 if not present in pre-image
	NewNo int    // 0 if not present in post-image
	Text  string // the line content, without the leading +/-/space marker
}

// DiffLineKind tags context vs added vs removed.
type DiffLineKind int

const (
	DiffContext DiffLineKind = iota
	DiffAdded
	DiffRemoved
	DiffNoNewline // "\ No newline at end of file" marker (rare)
)

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff splits a unified diff into per-file structures. It is permissive
// about leading non-diff lines and unrecognised pragmas, since `gh pr diff`
// occasionally emits prefix metadata or trailing whitespace.
func ParseDiff(s string) []FileDiff {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	var oldNo, newNo int

	flushHunk := func() {
		if hunk != nil && cur != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &FileDiff{Header: line}
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				a := strings.TrimPrefix(parts[2], "a/")
				b := strings.TrimPrefix(parts[3], "b/")
				cur.OldPath = a
				cur.Path = b
			}
		case cur == nil:
			// Skip any preamble before the first diff stanza.
			continue
		case strings.HasPrefix(line, "new file mode"):
			cur.IsNewFile = true
		case strings.HasPrefix(line, "deleted file mode"):
			cur.IsDeleted = true
		case strings.HasPrefix(line, "Binary files "):
			cur.IsBinary = true
		case strings.HasPrefix(line, "--- "):
			// pre-image path; we already captured a/b above. ignore.
		case strings.HasPrefix(line, "+++ "):
			// post-image path; we already captured. ignore.
		case strings.HasPrefix(line, "@@ "):
			flushHunk()
			m := hunkHeaderRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[3])
			hunk = &Hunk{Header: line, OldLine: oldNo, NewLine: newNo}
		case hunk == nil:
			// Lines inside the file stanza but before the first hunk (e.g.
			// "index abc..def 100644"). Ignore.
		case strings.HasPrefix(line, "+"):
			cur.Additions++
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: DiffAdded, NewNo: newNo, Text: stripFirst(line)})
			newNo++
		case strings.HasPrefix(line, "-"):
			cur.Deletions++
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: DiffRemoved, OldNo: oldNo, Text: stripFirst(line)})
			oldNo++
		case strings.HasPrefix(line, " "):
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: DiffContext, OldNo: oldNo, NewNo: newNo, Text: stripFirst(line)})
			oldNo++
			newNo++
		case strings.HasPrefix(line, "\\ "):
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: DiffNoNewline, Text: line})
		default:
			// Stray empty line or unrecognized: treat as context if inside a hunk.
			if hunk != nil {
				hunk.Lines = append(hunk.Lines, DiffLine{Kind: DiffContext, OldNo: oldNo, NewNo: newNo, Text: line})
				if line != "" {
					oldNo++
					newNo++
				}
			}
		}
	}
	flushFile()
	return files
}

func stripFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return s[1:]
}

// FindFile returns the FileDiff with the given post-image path, or nil.
func FindFile(files []FileDiff, path string) *FileDiff {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}

// HunkAroundLine returns the hunk that contains the given new-image line
// number, with index. Returns (nil, -1) if no hunk covers it.
func HunkAroundLine(file *FileDiff, line int) (*Hunk, int) {
	if file == nil || line <= 0 {
		return nil, -1
	}
	for i := range file.Hunks {
		h := &file.Hunks[i]
		minNew, maxNew := hunkNewBounds(h)
		if minNew == 0 {
			continue
		}
		if line >= minNew && line <= maxNew {
			return h, i
		}
	}
	return nil, -1
}

// FindUniqueExcerptInFile searches every hunk in file for a post-image line
// whose text equals excerpt after whitespace normalisation (see
// normaliseExcerpt). It returns (line, true) iff exactly one such line
// exists across all hunks; otherwise (0, false).
//
// The TUI uses this to relocate a finding when its original Line falls
// outside any hunk in the current diff (e.g. a force-push shifted the
// hunks but the anchored content is still present at a new line number).
// The model's "anchor_excerpt" JSON field is the input to this search —
// when the excerpt uniquely identifies a line, we treat that as the
// re-anchor target; when it matches zero or multiple lines, we leave the
// card unresolved and offer the file-level fallback instead.
//
// Short excerpts (< 20 chars after normalisation) are intentionally
// rejected — lines like "}" or "return nil" recur all over real files,
// and a confident-looking re-anchor against an ambiguous excerpt is
// worse than a clear "cannot resolve" error. The 20-char threshold
// mirrors anchor_excerpt.go's posture for the same reason.
func FindUniqueExcerptInFile(file *FileDiff, excerpt string) (int, bool) {
	if file == nil {
		return 0, false
	}
	norm := normaliseExcerpt(excerpt)
	if len(norm) < 20 {
		return 0, false
	}
	matchLine := 0
	matches := 0
	for hi := range file.Hunks {
		h := &file.Hunks[hi]
		for _, l := range h.Lines {
			if l.Kind == DiffRemoved || l.NewNo == 0 {
				continue
			}
			if normaliseExcerpt(l.Text) == norm {
				matches++
				if matches > 1 {
					return 0, false
				}
				matchLine = l.NewNo
			}
		}
	}
	if matches != 1 {
		return 0, false
	}
	return matchLine, true
}

func hunkNewBounds(h *Hunk) (minNew, maxNew int) {
	for _, l := range h.Lines {
		if l.Kind == DiffRemoved || l.NewNo == 0 {
			continue
		}
		if minNew == 0 || l.NewNo < minNew {
			minNew = l.NewNo
		}
		if l.NewNo > maxNew {
			maxNew = l.NewNo
		}
	}
	return minNew, maxNew
}

// HunkSnippet renders a small window around a target line — at most window
// lines on each side — as a slice of strings tagged with their kind. Useful
// for approval cards that want to show just the relevant code without flooding
// the user with the whole hunk.
func HunkSnippet(h *Hunk, targetLine, window int) []DiffLine {
	if h == nil {
		return nil
	}
	if window <= 0 {
		window = 3
	}
	// Find the index of the target line within h.Lines.
	idx := -1
	for i, l := range h.Lines {
		if l.NewNo == targetLine && l.Kind != DiffRemoved {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Target wasn't found (rare — the line was a deletion-only line). Fall
		// back to the whole hunk, capped at 2*window lines.
		max := 2 * window
		if len(h.Lines) <= max {
			return append([]DiffLine(nil), h.Lines...)
		}
		return append([]DiffLine(nil), h.Lines[:max]...)
	}
	from := idx - window
	if from < 0 {
		from = 0
	}
	to := idx + window + 1
	if to > len(h.Lines) {
		to = len(h.Lines)
	}
	out := make([]DiffLine, to-from)
	copy(out, h.Lines[from:to])
	return out
}
