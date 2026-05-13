package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// pane identifies which side of the PR detail body has focus for keyboard
// navigation and inbound wheel events.
type pane int

const (
	paneTree pane = iota
	paneDiff
)

// prDetailPanel is the outer frame for each pane of the PR detail body.
var prDetailPanel = leftPanel

// treeRow describes one file in the changed-files tree.
type treeRow struct {
	Path        string
	Additions   int
	Deletions   int
	IsBinary    bool
	IsNewFile   bool
	IsDeleted   bool
	FindingsN   int // count of inline findings anchored in this file (across all specialists)
	HighestSev  string
}

// buildTreeRows builds the list shown in the left tree pane.
func buildTreeRows(files []review.FileDiff, draft *review.Draft) []treeRow {
	rows := make([]treeRow, 0, len(files))
	for _, f := range files {
		row := treeRow{
			Path:      f.Path,
			Additions: f.Additions,
			Deletions: f.Deletions,
			IsBinary:  f.IsBinary,
			IsNewFile: f.IsNewFile,
			IsDeleted: f.IsDeleted,
		}
		if draft != nil {
			for _, s := range draft.Specialists {
				if s.Err != nil {
					continue
				}
				for _, fnd := range s.Findings {
					if fnd.Path != f.Path || fnd.Line <= 0 {
						continue
					}
					row.FindingsN++
					row.HighestSev = elevateSeverity(row.HighestSev, string(fnd.Severity))
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// padCellVisual pads with plain ASCII spaces until ansi.StringWidth(s) >= cellCount.
// Bubblezone uses printable cell width per line; padding extends row hit boxes to the
// right edge so clicks past styled path/stat text still register on the row zone.
func padCellVisual(s string, cellCount int) string {
	for ansi.StringWidth(s) < cellCount {
		s += " "
	}
	return s
}

func elevateSeverity(cur, candidate string) string {
	rank := func(s string) int {
		switch s {
		case "critical":
			return 4
		case "error":
			return 3
		case "warning":
			return 2
		case "info":
			return 1
		}
		return 0
	}
	if rank(candidate) > rank(cur) {
		return candidate
	}
	return cur
}

// renderTreePane renders the file-tree pane content (without the surrounding
// border — the caller frames it with prDetailPanel and adds the "Files · …"
// title bar). Each row is the FIRST visible line so click-Y maps 1:1 to row
// index in treeRowFromMouse: row 0 sits at viewport line 0.
func renderTreePane(rows []treeRow, selectedIdx int, contentCols int, focused bool) string {
	contentCols = max(8, contentCols)
	var b strings.Builder
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("(no files)"))
		return b.String()
	}
	for i, r := range rows {
		marker := "  "
		if i == selectedIdx {
			if focused {
				marker = boldStyle.Render("> ")
			} else {
				marker = "> "
			}
		}
		stat := fmt.Sprintf(" %s/%s",
			okStyle.Render(fmt.Sprintf("+%d", r.Additions)),
			errStyle.Render(fmt.Sprintf("-%d", r.Deletions)))
		if r.IsBinary {
			stat = " " + dimStyle.Render("(binary)")
		}
		badge := ""
		if r.FindingsN > 0 {
			tag := dimStyle.Render(fmt.Sprintf(" %d", r.FindingsN))
			switch r.HighestSev {
			case "critical":
				tag = sevCritical.Render(fmt.Sprintf(" %d!!", r.FindingsN))
			case "error":
				tag = sevError.Render(fmt.Sprintf(" %d!", r.FindingsN))
			case "warning":
				tag = sevWarning.Render(fmt.Sprintf(" %d", r.FindingsN))
			case "info":
				tag = sevInfo.Render(fmt.Sprintf(" %d", r.FindingsN))
			}
			badge = " " + tag
		}
		// Compose row: marker + path + stat + badge — truncate path to fit.
		head := marker
		tail := stat + badge
		nameW := contentCols - ansi.StringWidth(head) - ansi.StringWidth(tail) - 1
		if nameW < 4 {
			nameW = 4
		}
		path := truncWidth(r.Path, nameW)
		row := head + path + tail
		row = lipgloss.NewStyle().Width(contentCols).Align(lipgloss.Left).Render(row)
		if ansi.StringWidth(row) > contentCols {
			row = ansi.Truncate(row, contentCols, "")
		}
		row = padCellVisual(row, contentCols)
		b.WriteString(zone.Mark(zoneTreeFile(i), row) + "\n")
	}
	return b.String()
}

// renderDiffPane renders the selected file's diff with inline finding
// annotations underneath each anchored finding's line. If file is nil
// (initial state), it falls back to an instruction.
func renderDiffPane(file *review.FileDiff, draft *review.Draft, focused bool, contentCols int) string {
	contentCols = max(8, contentCols)
	if file == nil {
		return dimStyle.Render("Select a file in the tree (j/k or click) to see its diff.")
	}
	var b strings.Builder
	header := fmt.Sprintf("%s  %s/%s",
		boldStyle.Render(file.Path),
		okStyle.Render(fmt.Sprintf("+%d", file.Additions)),
		errStyle.Render(fmt.Sprintf("-%d", file.Deletions)))
	if focused {
		b.WriteString(header + "\n\n")
	} else {
		b.WriteString(dimStyle.Render(file.Path) + "  " + dimStyle.Render(fmt.Sprintf("+%d/-%d", file.Additions, file.Deletions)) + "\n\n")
	}
	if file.IsBinary {
		b.WriteString(dimStyle.Render("(binary file — diff not shown)"))
		return b.String()
	}
	// Group findings by hunk by their anchor new-line.
	findingsByLine := map[int][]inlineFindingTag{}
	if draft != nil {
		for _, s := range draft.Specialists {
			if s.Err != nil {
				continue
			}
			for _, f := range s.Findings {
				if f.Path != file.Path || f.Line <= 0 {
					continue
				}
				findingsByLine[f.Line] = append(findingsByLine[f.Line], inlineFindingTag{
					Specialist: s.Specialist,
					Severity:   string(f.Severity),
					Comment:    f.Comment,
					HasFix:     review.SuggestionPostsToGitHub(f),
				})
			}
		}
	}
	for hi, h := range file.Hunks {
		if hi > 0 {
			b.WriteString("\n")
		}
		b.WriteString(dimStyle.Render(h.Header) + "\n")
		for _, ln := range h.Lines {
			lineStr := renderHunkLine(ln, contentCols)
			b.WriteString(lineStr + "\n")
			if tags, ok := findingsByLine[ln.NewNo]; ok && ln.Kind != review.DiffRemoved {
				for _, t := range tags {
					b.WriteString(renderInlineFindingTag(t, contentCols) + "\n")
				}
			}
		}
	}
	return b.String()
}

type inlineFindingTag struct {
	Specialist string
	Severity   string
	Comment    string
	HasFix     bool
}

func renderInlineFindingTag(t inlineFindingTag, contentCols int) string {
	contentCols = max(8, contentCols)
	indent := "    ↳ "
	prefix := indent + renderTag(t.Specialist) + " " + renderSeverity(t.Severity)
	suffix := ""
	if t.HasFix {
		suffix = " " + dimStyle.Render("(has 1-click fix)")
	}
	availW := contentCols - ansi.StringWidth(prefix) - ansi.StringWidth(suffix) - 1
	if availW < 8 {
		availW = 8
	}
	first := strings.SplitN(strings.TrimSpace(t.Comment), "\n", 2)[0]
	text := truncWidth(first, availW)
	return prefix + " " + text + suffix
}

// renderHunkLine formats one diff line for the diff pane viewport. It renders
// without the line numbers — the diff preview in the approval card already
// shows ◀here on the focused line, and the file pane prefers visual density
// over column noise.
func renderHunkLine(ln review.DiffLine, contentCols int) string {
	contentCols = max(8, contentCols)
	var head, body string
	switch ln.Kind {
	case review.DiffAdded:
		head = sevWarning.Render("+ ")
		body = ln.Text
	case review.DiffRemoved:
		head = sevError.Render("- ")
		body = ln.Text
	case review.DiffNoNewline:
		head = dimStyle.Render("  ")
		body = ln.Text
	default:
		head = dimStyle.Render("· ")
		body = ln.Text
	}
	full := head + body
	if ansi.StringWidth(full) > contentCols {
		// Use ansi-aware hardwrap so wrapped continuation lines line up under
		// the prefix.
		wrapped := ansi.Hardwrap(full, contentCols, false)
		return wrapped
	}
	return full
}
