package model

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/theme"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// pane identifies which side of the PR detail body has focus for keyboard
// navigation and inbound wheel events.
type pane int

const (
	paneTree pane = iota
	paneDiff
	paneControls
)

// paneCount is the number of cyclable panes on the PR detail view.
const paneCount = 3

// centerView selects what the centre pane renders. centerDiff is the historic
// behaviour (the diff for whichever file the tree cursor selects); the other
// values swap that out for a full-pane render of the PR's description, the
// status-check rollup detail, or the conversation timeline.
type centerView int

const (
	centerDiff centerView = iota
	centerDescription
	centerChecks
	centerDiscussion
	// centerHistory (Phase 5 item 8) shows the browsable review-history pane:
	// the PR's existing inline review threads/comments, with a cursor and an
	// in-pane reply prompt. It has no overview-selector row — it's reached via
	// the `H` shortcut from the diff pane.
	centerHistory
)

// overviewItemKind is the semantic tag for one row in the PR-overview
// selector that sits above the file tree. Same set as centerView minus the
// diff entry — the file tree below is the "Files" selector.
type overviewItemKind int

const (
	overviewDescription overviewItemKind = iota
	overviewChecks
	overviewDiscussion
)

// overviewItemCount is how many rows the overview selector renders.
const overviewItemCount = 3

// overviewKindToCenter maps an overview row to the centre view it activates.
func overviewKindToCenter(k overviewItemKind) centerView {
	switch k {
	case overviewDescription:
		return centerDescription
	case overviewChecks:
		return centerChecks
	case overviewDiscussion:
		return centerDiscussion
	}
	return centerDiff
}

// centerToOverviewKind is the inverse of overviewKindToCenter; it reports
// (kind, true) for centerView values that correspond to an overview row,
// or (_, false) for centerDiff. The boolean lets the renderer skip
// highlighting any overview row when the user is browsing the tree.
func centerToOverviewKind(v centerView) (overviewItemKind, bool) {
	switch v {
	case centerDescription:
		return overviewDescription, true
	case centerChecks:
		return overviewChecks, true
	case centerDiscussion:
		return overviewDiscussion, true
	}
	return 0, false
}

// prDetailPanel is the outer frame for each pane of the PR detail body.
var prDetailPanel = styles.LeftPanel

// treeRow describes one file in the changed-files tree.
type treeRow struct {
	Path       string
	Additions  int
	Deletions  int
	IsBinary   bool
	IsNewFile  bool
	IsDeleted  bool
	FindingsN  int // count of inline findings anchored in this file (across all specialists)
	HighestSev string
}

// treeViewRow is one row in the rendered hierarchical tree. Folders and
// files share a single flat index space (so j/k can stop on a folder and
// space can toggle it); fileIndex distinguishes them and points back into
// m.treeRows so the renderer can pull stats and findings.
//
// fullPath stores the cumulative directory path for folders (no trailing
// slash) and the full file path for files. Folder rows use fullPath as
// the key into m.collapsedFolders.
type treeViewRow struct {
	isFile    bool
	indent    int
	name      string
	fullPath  string
	fileIndex int // index into m.treeRows; -1 for folders
}

// fileTreeNode is the intermediate trie used by buildTreeView. Mirrors
// jj-tui's renderFileTreeWithLineIndex node (see
// /Users/michael.madicen/Documents/GitHub/jj-tui/internal/tui/tabs/graph/view_helpers.go).
type fileTreeNode struct {
	name      string
	fullPath  string
	children  map[string]*fileTreeNode
	isFile    bool
	fileIndex int
}

// buildTreeView builds the hierarchical view-row list from a flat list of
// changed files plus a map of collapsed folder paths. Returns the rows in
// DFS order (dirs first, then files, alphabetical within each group) plus
// fileToLine and lineToFile maps so callers can jump between selection
// (file index) and viewport line (row index).
func buildTreeView(rows []treeRow, collapsed map[string]bool) (view []treeViewRow, fileToLine []int, lineToFile []int) {
	fileToLine = make([]int, len(rows))
	for i := range fileToLine {
		fileToLine[i] = -1
	}
	if len(rows) == 0 {
		return nil, fileToLine, nil
	}
	root := &fileTreeNode{children: map[string]*fileTreeNode{}, fileIndex: -1}
	for i, r := range rows {
		parts := strings.Split(r.Path, "/")
		cur := root
		var pathSoFar string
		for j, part := range parts {
			if cur.children == nil {
				cur.children = map[string]*fileTreeNode{}
			}
			if pathSoFar == "" {
				pathSoFar = part
			} else {
				pathSoFar = pathSoFar + "/" + part
			}
			child, ok := cur.children[part]
			if !ok {
				child = &fileTreeNode{
					name:      part,
					fullPath:  pathSoFar,
					children:  map[string]*fileTreeNode{},
					fileIndex: -1,
				}
				cur.children[part] = child
			}
			cur = child
			if j == len(parts)-1 {
				cur.isFile = true
				cur.fileIndex = i
			}
		}
	}

	var emit func(n *fileTreeNode, depth int, isRoot bool)
	emit = func(n *fileTreeNode, depth int, isRoot bool) {
		if !isRoot {
			row := treeViewRow{
				isFile:    n.isFile,
				indent:    depth,
				name:      n.name,
				fullPath:  n.fullPath,
				fileIndex: n.fileIndex,
			}
			view = append(view, row)
			lineToFile = append(lineToFile, n.fileIndex)
			if n.isFile && n.fileIndex >= 0 && n.fileIndex < len(fileToLine) {
				fileToLine[n.fileIndex] = len(view) - 1
			}
			if !n.isFile && collapsed[n.fullPath] {
				return // skip descendants
			}
		}
		var dirs, files []string
		for name, child := range n.children {
			if child.isFile {
				files = append(files, name)
			} else {
				dirs = append(dirs, name)
			}
		}
		sort.Strings(dirs)
		sort.Strings(files)
		nextDepth := depth
		if !isRoot {
			nextDepth = depth + 1
		}
		for _, name := range dirs {
			emit(n.children[name], nextDepth, false)
		}
		for _, name := range files {
			emit(n.children[name], nextDepth, false)
		}
	}
	emit(root, 0, true)
	return view, fileToLine, lineToFile
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

// renderOverviewBox renders the small selector that sits at the top of
// the left column above the file tree. Three rows — Description, Checks,
// Discussion — each padded to contentCols and zone-marked so a click
// flips the centre pane. The caller-supplied selectedKind is highlighted
// when active is true; otherwise no row carries the selection marker
// (the user is browsing the file tree below).
//
// The renderer mirrors the truncation pattern from renderTreeViewRow:
// names are pre-truncated to fit the available width before styling, so
// long labels never wrap onto a phantom blank line.
func renderOverviewBox(
	pr *ghPRSubset,
	selectedKind overviewItemKind,
	active bool,
	focused bool,
	contentCols int,
) string {
	contentCols = max(8, contentCols)
	rows := []struct {
		kind  overviewItemKind
		label string
		zone  string
		badge string
	}{
		{
			kind:  overviewDescription,
			label: "Description",
			zone:  zones.OverviewDescription,
			badge: pr.descriptionBadge,
		},
		{
			kind:  overviewChecks,
			label: "Checks",
			zone:  zones.OverviewChecks,
			badge: pr.checksBadge,
		},
		{
			kind:  overviewDiscussion,
			label: "Discussion",
			zone:  zones.OverviewDiscussion,
			badge: pr.discussionBadge,
		},
	}
	var b strings.Builder
	b.WriteString(styles.DimStyle.Render("PR overview") + "\n")
	for _, r := range rows {
		selected := active && r.kind == selectedKind
		row := renderOverviewRow(r.label, r.badge, selected, focused, contentCols)
		b.WriteString(zone.Mark(r.zone, row) + "\n")
	}
	// Horizontal divider so the overview block reads as a distinct
	// section from the file tree below. We use a styled rule rather than
	// padding so the eye lands on the boundary even when both halves
	// are dim. The width matches the inner content cols (so the line
	// extends the full pane interior, like the title strip above).
	b.WriteString(styles.DimStyle.Render(strings.Repeat("─", contentCols)))
	return b.String()
}

// ghPRSubset bundles the per-PR text we want to surface as overview row
// badges. Kept tight so renderOverviewBox doesn't need to know about the
// full Model.
type ghPRSubset struct {
	descriptionBadge string
	checksBadge      string
	discussionBadge  string
}

// overviewBadgesForModel computes the right-hand badges for each overview
// row based on the model's current state. The Description badge counts
// non-blank body lines; Checks reflects the rollup state if known; the
// Discussion badge shows a pending count when we have one.
func (m *Model) overviewBadgesForModel() ghPRSubset {
	out := ghPRSubset{}
	if m.currentPR != nil {
		body := strings.TrimSpace(m.currentPR.Body)
		if body == "" {
			out.descriptionBadge = styles.DimStyle.Render("(empty)")
		} else {
			lines := 0
			for _, ln := range strings.Split(body, "\n") {
				if strings.TrimSpace(ln) != "" {
					lines++
				}
			}
			out.descriptionBadge = styles.DimStyle.Render(fmt.Sprintf("%d ln", lines))
		}
		if c := checksRollupChip(m.currentPR.ChecksState); c != "" {
			out.checksBadge = c
		} else {
			out.checksBadge = styles.DimStyle.Render("--")
		}
	}
	switch {
	case m.checksLoading:
		out.checksBadge = styles.DimStyle.Render("loading…")
	case m.checksErr != nil:
		out.checksBadge = styles.ErrStyle.Render("error")
	case m.checks != nil:
		out.checksBadge = checksRollupChip(m.checks.RollupState)
		if out.checksBadge == "" {
			out.checksBadge = styles.DimStyle.Render("no checks")
		}
	}
	switch {
	case m.discussionLoading:
		out.discussionBadge = styles.DimStyle.Render("loading…")
	case m.discussionErr != nil:
		out.discussionBadge = styles.ErrStyle.Render("error")
	case m.discussion != nil:
		if len(m.discussion) == 0 {
			out.discussionBadge = styles.DimStyle.Render("0")
		} else {
			out.discussionBadge = styles.DimStyle.Render(fmt.Sprintf("%d", len(m.discussion)))
		}
	default:
		out.discussionBadge = styles.DimStyle.Render("…")
	}
	return out
}

// renderOverviewRow renders one row of the overview selector. Layout
// mirrors a tree file row: marker + label on the left, badge right-
// aligned. selected rows get a "> " marker and a soft highlight bg so
// the cursor position is obvious without stealing focus styling from the
// tree below.
func renderOverviewRow(label, badge string, selected, focused bool, contentCols int) string {
	marker := "  "
	if selected {
		if focused {
			marker = styles.BoldStyle.Render("> ")
		} else {
			marker = "> "
		}
	}
	badgeW := ansi.StringWidth(badge)
	labelW := contentCols - 2 - badgeW - 1
	if labelW < 4 {
		labelW = 4
	}
	labelTrunc := truncWidth(label, labelW)
	rendered := labelTrunc
	if selected {
		highlight := styles.DimStyle.Background(lipgloss.Color("#3d4f5f")).Foreground(lipgloss.Color("#ffffff"))
		if focused {
			highlight = highlight.Bold(true)
		}
		rendered = highlight.Render(labelTrunc)
	}
	row := marker + rendered
	gap := contentCols - ansi.StringWidth(row) - badgeW
	if gap < 1 {
		gap = 1
	}
	row += strings.Repeat(" ", gap) + badge
	if ansi.StringWidth(row) > contentCols {
		row = ansi.Truncate(row, contentCols, "")
	}
	return padCellVisual(row, contentCols)
}

// renderTreePane renders the hierarchical file-tree pane content (without
// the surrounding border — the caller frames it with prDetailPanel and adds
// the "Files · …" title bar).
//
// Each viewRow is exactly one physical line so click-Y maps 1:1 to view-row
// index via treeRowFromMouse + treeLineToFile. Folder rows show a ▶/▼
// disclosure marker and dim-styled `name/`; file rows show the existing
// status / stats / findings layout, indented under their parent folder.
//
// fileRows is consulted for stats and finding counts via the leaf row's
// fileIndex (set by buildTreeView). collapsed is consulted to pick the
// folder marker.
func renderTreePane(viewRows []treeViewRow, fileRows []treeRow, collapsed map[string]bool, selectedIdx int, contentCols int, focused bool) string {
	contentCols = max(8, contentCols)
	var b strings.Builder
	if len(viewRows) == 0 {
		b.WriteString(styles.DimStyle.Render("(no files)"))
		return b.String()
	}
	for i, vr := range viewRows {
		row := renderTreeViewRow(vr, fileRows, collapsed, i == selectedIdx, focused, contentCols)
		zoneID := zones.TreeFile(vr.fileIndex)
		if !vr.isFile {
			zoneID = zones.TreeFolder(i)
		}
		b.WriteString(zone.Mark(zoneID, row) + "\n")
	}
	return b.String()
}

// renderTreeViewRow renders a single tree row (folder or file) padded to
// contentCols. Folder rows show indent + ▶/▼ + dim name/. File rows show
// indent + selection marker + path + stats + finding badge.
func renderTreeViewRow(vr treeViewRow, fileRows []treeRow, collapsed map[string]bool, selected, focused bool, contentCols int) string {
	indent := strings.Repeat("  ", vr.indent)
	if !vr.isFile {
		marker := styles.DimStyle.Render("\u25bc ") // ▼
		if collapsed[vr.fullPath] {
			marker = styles.DimStyle.Render("\u25b6 ") // ▶
		}
		// Reserve space for indent + marker (visual width), then truncate
		// the folder name so the rendered row never exceeds contentCols.
		// Without this, lipgloss's Width() wraps overflow onto a second
		// padded line and the tree gains phantom blank rows.
		head := indent + marker
		nameW := contentCols - ansi.StringWidth(head)
		if nameW < 2 {
			nameW = 2
		}
		name := truncWidth(vr.name+"/", nameW)
		nameStyled := styles.DimStyle.Render(name)
		if selected {
			selStyle := styles.DimStyle.Background(lipgloss.Color("#3d4f5f")).Foreground(lipgloss.Color("#ffffff"))
			if focused {
				selStyle = selStyle.Bold(true)
			}
			nameStyled = selStyle.Render(name)
		}
		row := head + nameStyled
		if ansi.StringWidth(row) > contentCols {
			row = ansi.Truncate(row, contentCols, "")
		}
		return padCellVisual(row, contentCols)
	}

	r := treeRow{}
	if vr.fileIndex >= 0 && vr.fileIndex < len(fileRows) {
		r = fileRows[vr.fileIndex]
	}
	marker := "  "
	if selected {
		if focused {
			marker = styles.BoldStyle.Render("> ")
		} else {
			marker = "> "
		}
	}
	stat := fmt.Sprintf(" %s/%s",
		styles.OkStyle.Render(fmt.Sprintf("+%d", r.Additions)),
		styles.ErrStyle.Render(fmt.Sprintf("-%d", r.Deletions)))
	if r.IsBinary {
		stat = " " + styles.DimStyle.Render("(binary)")
	}
	badge := ""
	if r.FindingsN > 0 {
		tag := styles.DimStyle.Render(fmt.Sprintf(" %d", r.FindingsN))
		switch r.HighestSev {
		case "critical":
			tag = styles.SevCritical.Render(fmt.Sprintf(" %d!!", r.FindingsN))
		case "error":
			tag = styles.SevError.Render(fmt.Sprintf(" %d!", r.FindingsN))
		case "warning":
			tag = styles.SevWarning.Render(fmt.Sprintf(" %d", r.FindingsN))
		case "info":
			tag = styles.SevInfo.Render(fmt.Sprintf(" %d", r.FindingsN))
		}
		badge = " " + tag
	}
	head := indent + marker
	tail := stat + badge
	nameW := contentCols - ansi.StringWidth(head) - ansi.StringWidth(tail) - 1
	if nameW < 4 {
		nameW = 4
	}
	// Show only the file's basename in the tree — the directory path
	// is communicated by the indent / parent-folder rows above.
	name := vr.name
	if name == "" {
		name = r.Path
	}
	name = truncWidth(name, nameW)
	row := head + name + tail
	row = lipgloss.NewStyle().Width(contentCols).Align(lipgloss.Left).Render(row)
	if ansi.StringWidth(row) > contentCols {
		row = ansi.Truncate(row, contentCols, "")
	}
	return padCellVisual(row, contentCols)
}

// renderDiffPane renders the selected file's diff with inline finding
// annotations underneath each anchored finding's line. If file is nil
// (initial state), it falls back to an instruction.
//
// When the pane is wide enough (contentCols >= diffGutterMinPaneWidth),
// each hunk line is prefixed with a 10-cell old/new line-number gutter
// and add/del rows get a subtle muted background to make scanning easier.
// Below the threshold the gutter is dropped so the diff stays readable
// on narrow terminals.
func renderDiffPane(file *review.FileDiff, draft *review.Draft, focused bool, contentCols int) string {
	return renderDiffPaneHL(file, draft, focused, contentCols, nil, nil)
}

// renderDiffPaneHL is renderDiffPane with an optional chroma highlighter
// (Phase 5 item 4) and optional existing-comment annotations (Phase 5 item 8).
// When hl is active, each diff line's body is syntax-highlighted (or word-level
// emphasised for in-place edits) and the row is drawn without the add/del
// background tint (renderHunkLineHighlighted); a nil / inactive highlighter
// reproduces the original tinted, plain-text render exactly. comments, when
// non-nil, are rendered as inline "prior comment" annotations at their anchor
// lines alongside the AI finding tags.
func renderDiffPaneHL(file *review.FileDiff, draft *review.Draft, focused bool, contentCols int, hl *diffview.Highlighter, comments []gh.PullReviewComment) string {
	contentCols = max(8, contentCols)
	if file == nil {
		return styles.DimStyle.Render("Select a file in the tree (j/k or click) to see its diff.")
	}
	highlight := hl.Active() && !file.IsBinary
	commentsByLine := existingCommentsByLine(comments, file.Path)
	var b strings.Builder
	header := fmt.Sprintf("%s  %s/%s",
		styles.BoldStyle.Render(file.Path),
		styles.OkStyle.Render(fmt.Sprintf("+%d", file.Additions)),
		styles.ErrStyle.Render(fmt.Sprintf("-%d", file.Deletions)))
	if focused {
		b.WriteString(header + "\n\n")
	} else {
		b.WriteString(styles.DimStyle.Render(file.Path) + "  " + styles.DimStyle.Render(fmt.Sprintf("+%d/-%d", file.Additions, file.Deletions)) + "\n\n")
	}
	if file.IsBinary {
		b.WriteString(styles.DimStyle.Render("(binary file — diff not shown)"))
		return b.String()
	}
	useGutter := contentCols >= diffGutterMinPaneWidth
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
		// Hunk header: when the gutter is enabled, lead with a 10-space
		// blank gutter so the @@ line aligns with the content columns
		// below it. Without the gutter, render the header flush left
		// (preserving the prior look).
		hunkPrefix := ""
		if useGutter {
			hunkPrefix = strings.Repeat(" ", diffGutterWidth)
		}
		b.WriteString(hunkPrefix + styles.DimStyle.Render(h.Header) + "\n")
		var wordSegs [][]diffview.Seg
		if highlight {
			wordSegs = wordDiffForHunk(h.Lines)
		}
		for li, ln := range h.Lines {
			var lineStr string
			if highlight {
				body := diffBodyStyled(file.Path, ln, wordSegs[li], hl)
				lineStr = renderHunkLineHighlighted(ln, body, contentCols, useGutter)
			} else {
				lineStr = renderHunkLine(ln, contentCols, useGutter)
			}
			b.WriteString(lineStr + "\n")
			if tags, ok := findingsByLine[ln.NewNo]; ok && ln.Kind != review.DiffRemoved {
				for _, t := range tags {
					b.WriteString(renderInlineFindingTag(t, contentCols) + "\n")
				}
			}
			if cmts, ok := commentsByLine[ln.NewNo]; ok && ln.Kind != review.DiffRemoved {
				for _, c := range cmts {
					b.WriteString(renderInlineCommentTag(c, contentCols) + "\n")
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
	prefix := indent + styles.RenderTag(t.Specialist) + " " + styles.RenderSeverity(t.Severity)
	suffix := ""
	if t.HasFix {
		suffix = " " + styles.DimStyle.Render("(has 1-click fix)")
	}
	availW := contentCols - ansi.StringWidth(prefix) - ansi.StringWidth(suffix) - 1
	if availW < 8 {
		availW = 8
	}
	first := strings.SplitN(strings.TrimSpace(t.Comment), "\n", 2)[0]
	text := truncWidth(first, availW)
	return prefix + " " + text + suffix
}

// diffGutterWidth is the fixed cell width of the old/new line-number
// gutter rendered to the left of each diff body line:
//
//	"%4d│%4d " => 4 + 1 + 4 + 1 = 10 cells
//
// Inspired by jj-tui's unifiedDiffGutterColumns (12 there); we use 10
// here because our diff pane sits inside a 3-pane layout and every cell
// of body width matters.
const diffGutterWidth = 10

// diffGutterMinPaneWidth is the minimum content column count required to
// render the line-number gutter without starving the body. Below this we
// fall back to no-gutter rendering so the diff stays useful on narrow
// terminals (e.g. when the controls pane is showing on a small screen).
//
//	gutter (10) + glyph (2) + minimum body (8) = 20
const diffGutterMinPaneWidth = 20

// diffGutterBlank is the cell-width-equivalent leading prefix used on
// hunk headers and continuation rows of wrapped lines so column
// alignment holds across the entire diff.
var diffGutterBlank = strings.Repeat(" ", diffGutterWidth)

// Background tint colors for add/del rows. Deliberately darker than
// jj-tui's #1B4332/#4A232C so they read as "muted highlight" against
// our overall theme rather than "strong block of color"; the existing
// foreground styles (SevWarning / SevError) on the +/- glyphs still
// carry the primary signal.
var (
	diffAddBgStyle = lipgloss.NewStyle().Background(lipgloss.Color("#173027"))
	diffDelBgStyle = lipgloss.NewStyle().Background(lipgloss.Color("#2C1A1F"))
)

// formatGutter builds the 10-cell `old│new` gutter for one diff line and
// renders it pre-merged with the row background so the bg color paints
// continuously underneath the dim line numbers.
//
// We must compose dim + bg into a single lipgloss style before Render
// because lipgloss embeds an SGR reset (`\x1b[0m`) at the end of every
// Render() call, and a reset emitted in the middle of a row also clears
// any outer bg color that was applied to a wrapping Render(). The fix is
// to never wrap pre-styled text in another bg.Render — instead, every
// segment carries its own merged style.
//
//	context (DiffContext): "%4d│%4d " — both numbers
//	addition (DiffAdded):  "    │%4d " — left blank
//	deletion (DiffRemoved): "%4d│     " — right blank
//	no-newline / unknown:   10 spaces
func formatGutter(ln review.DiffLine) string {
	bg := rowBgStyle(ln.Kind)
	return styles.DimStyle.Inherit(bg).Render(gutterText(ln))
}

// gutterText builds the plain (unstyled) 10-cell "old│new" gutter content for
// one diff line. Callers wrap it in whatever style (with or without a row bg)
// they need. Width sanity: left + "│" + right + " " = 4 + 1 + 4 + 1 = 10 cells.
func gutterText(ln review.DiffLine) string {
	const blankNum = "    "
	left, right := blankNum, blankNum
	switch ln.Kind {
	case review.DiffContext:
		if ln.OldNo > 0 {
			left = fmt.Sprintf("%4d", ln.OldNo)
		}
		if ln.NewNo > 0 {
			right = fmt.Sprintf("%4d", ln.NewNo)
		}
	case review.DiffAdded:
		if ln.NewNo > 0 {
			right = fmt.Sprintf("%4d", ln.NewNo)
		}
	case review.DiffRemoved:
		if ln.OldNo > 0 {
			left = fmt.Sprintf("%4d", ln.OldNo)
		}
	default:
		// DiffNoNewline (rare) and any future kinds: blank gutter.
		return diffGutterBlank
	}
	return left + "\u2502" + right + " "
}

// pickGlyph returns the per-kind two-cell prefix that follows the gutter,
// rendered with the matching row bg pre-merged so the bg paints across
// the glyph cells without an SGR reset gap (see formatGutter for why).
//
// We resolve the severity colors via SevStyle (returns a fresh
// lipgloss.Style) so we can .Inherit(bg) onto them; the cached
// SeverityStyle wrappers don't expose a Style we could compose.
func pickGlyph(k review.DiffLineKind) string {
	bg := rowBgStyle(k)
	switch k {
	case review.DiffAdded:
		return styles.SevStyle(theme.KeySevWarning, false).Inherit(bg).Render("+ ")
	case review.DiffRemoved:
		return styles.SevStyle(theme.KeySevError, true).Inherit(bg).Render("- ")
	case review.DiffNoNewline:
		return styles.DimStyle.Inherit(bg).Render("  ")
	default:
		return styles.DimStyle.Inherit(bg).Render("· ")
	}
}

// rowBgStyle returns the background style for a given diff line kind, or
// the zero (transparent) style for context / no-newline rows. Used to
// tint add/del rows so they're visually distinct without overpowering
// the foreground glyph colors.
//
// Returned style has ONLY .Background() set (or nothing for context),
// so callers can safely .Inherit(rowBgStyle(k)) onto another style
// without overwriting unrelated fields.
func rowBgStyle(k review.DiffLineKind) lipgloss.Style {
	switch k {
	case review.DiffAdded:
		return diffAddBgStyle
	case review.DiffRemoved:
		return diffDelBgStyle
	default:
		return lipgloss.NewStyle()
	}
}

// renderHunkLine formats one diff line for the diff pane viewport.
//
// When useGutter is true, the line is prefixed with a 10-cell old/new
// line-number gutter (formatGutter), then the kind glyph (pickGlyph),
// then the body. Wrapped continuation rows are aligned under the body
// by emitting a blank gutter + glyph-width spacer.
//
// When useGutter is false (narrow pane), the legacy no-gutter layout is
// used: glyph + body, wrapped to contentCols. This keeps the diff
// usable on terminals where the gutter would crowd out the body.
//
// Background tint: add/del rows are painted edge-to-edge across the
// full contentCols. Each pre-styled segment (gutter / glyph /
// continuation prefix) carries the bg pre-merged, and the body + the
// trailing pad are rendered with bg as a separate Render call. We
// never wrap a row that already contains styled segments in another
// bg.Render — an embedded SGR reset (`\x1b[0m`) inside the inner
// styled chunk would otherwise clear the outer bg for everything that
// follows it on the line, which is the bug that previously made the
// tint stop right after the gutter.
func renderHunkLine(ln review.DiffLine, contentCols int, useGutter bool) string {
	contentCols = max(8, contentCols)
	bg := rowBgStyle(ln.Kind)
	glyph := pickGlyph(ln.Kind) // 2 printable cells; bg pre-merged

	var frame rowFrame
	if useGutter {
		gutter := formatGutter(ln) // 10 printable cells; bg pre-merged
		contGutter := styles.DimStyle.Inherit(bg).Render(diffGutterBlank)
		// Continuation rows reserve the same body-column start as the
		// first row by reproducing the 2-cell glyph slot, blank, with
		// bg merged in (so the tint band is unbroken vertically).
		contSpacer := bg.Render("  ")
		frame = rowFrame{
			head:     gutter + glyph,
			contHead: contGutter + contSpacer,
			headW:    diffGutterWidth + 2,
			bg:       bg,
		}
	} else {
		contSpacer := bg.Render("  ")
		frame = rowFrame{
			head:     glyph,
			contHead: contSpacer,
			headW:    2,
			bg:       bg,
		}
	}
	return wrapDiffBody(ln.Text, frame, contentCols)
}

// pickGlyphPlain is pickGlyph without a row background pre-merged. Used by the
// highlighter-active render path (renderHunkLineHighlighted), which drops the
// add/del bg tint so a syntax-highlighted body's inner SGR resets can't clear
// the tint mid-line — the coloured glyph + syntax colours carry the signal.
func pickGlyphPlain(k review.DiffLineKind) string {
	switch k {
	case review.DiffAdded:
		return styles.SevStyle(theme.KeySevWarning, false).Render("+ ")
	case review.DiffRemoved:
		return styles.SevStyle(theme.KeySevError, true).Render("- ")
	case review.DiffNoNewline:
		return styles.DimStyle.Render("  ")
	default:
		return styles.DimStyle.Render("· ")
	}
}

// renderHunkLineHighlighted renders a diff line with a caller-supplied,
// already-styled body and NO row background tint (Phase 5 item 4). It's used
// when chroma syntax highlighting or word-level diff emphasis is active: the
// styled body carries embedded SGR resets that would clear a row bg for the
// rest of the line (see renderHunkLine's docstring), so we omit the tint and
// let the coloured +/- glyph + the syntax/word colours convey the change.
//
// Layout matches renderHunkLine exactly (gutter + glyph + wrapped body) so the
// two paths stay column-aligned within a single diff.
func renderHunkLineHighlighted(ln review.DiffLine, styledBody string, contentCols int, useGutter bool) string {
	contentCols = max(8, contentCols)
	glyph := pickGlyphPlain(ln.Kind)
	var frame rowFrame
	if useGutter {
		gutter := styles.DimStyle.Render(gutterText(ln))
		contGutter := styles.DimStyle.Render(diffGutterBlank)
		frame = rowFrame{
			head:     gutter + glyph,
			contHead: contGutter + "  ",
			headW:    diffGutterWidth + 2,
		}
	} else {
		frame = rowFrame{
			head:     glyph,
			contHead: "  ",
			headW:    2,
		}
	}
	return wrapDiffBody(styledBody, frame, contentCols)
}

// rowFrame describes the per-row prefix used by wrapDiffBody. The first
// physical line uses head; continuation lines use contHead. Both must
// have the same printable cell width (headW) so the body column stays
// aligned across wraps.
type rowFrame struct {
	head     string
	contHead string
	headW    int
	bg       lipgloss.Style
}

// wrapDiffBody splits body into chunks of (contentCols - frame.headW)
// cells, prepends frame.head / frame.contHead, and renders each
// physical line edge-to-edge with the row bg. The body and trailing
// pad are rendered together with bg.Render so the tint runs to the
// right edge without any SGR-reset gap (see renderHunkLine docstring).
//
// We split body manually with ansi.Truncate so each chunk has a known
// printable cell width before re-attaching the prefix; using
// ansi.Hardwrap on (head+body) would re-emit head on continuation
// rows, which is wrong.
func wrapDiffBody(body string, frame rowFrame, contentCols int) string {
	bodyW := contentCols - frame.headW
	if bodyW < 1 {
		bodyW = 1
	}
	chunks := splitBodyChunks(body, bodyW)
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		head := frame.head
		if i > 0 {
			head = frame.contHead
		}
		chunkW := ansi.StringWidth(chunk)
		padW := contentCols - frame.headW - chunkW
		if padW < 0 {
			padW = 0
		}
		var tail string
		if hasBackground(frame.bg) {
			// Single Render on (body + pad) gives one continuous bg
			// SGR span that runs to the right edge.
			tail = frame.bg.Render(chunk + strings.Repeat(" ", padW))
		} else {
			tail = chunk + strings.Repeat(" ", padW)
		}
		row := head + tail
		if ansi.StringWidth(row) > contentCols {
			row = ansi.Truncate(row, contentCols, "")
		}
		out = append(out, row)
	}
	return strings.Join(out, "\n")
}

// splitBodyChunks breaks body into substrings whose printable widths are
// each <= chunkW. Preserves any SGR codes already present in body.
func splitBodyChunks(body string, chunkW int) []string {
	if chunkW < 1 {
		chunkW = 1
	}
	if ansi.StringWidth(body) <= chunkW {
		return []string{body}
	}
	var chunks []string
	remaining := body
	for ansi.StringWidth(remaining) > 0 {
		chunk := ansi.Truncate(remaining, chunkW, "")
		// Determine how much of remaining was consumed by chunk by
		// stripping chunk's printable prefix off remaining. ansi.Truncate
		// preserves SGR codes, so equality on the styled prefix can
		// fail with reordered codes; instead, compute the byte boundary
		// where chunk's printable width ends.
		consumed := chunkBytePrefix(remaining, ansi.StringWidth(chunk))
		if consumed <= 0 {
			// Defensive: avoid an infinite loop on pathological input.
			chunks = append(chunks, chunk)
			break
		}
		chunks = append(chunks, chunk)
		remaining = remaining[consumed:]
	}
	return chunks
}

// hasBackground reports whether s has an explicit background color set.
// Used as a fast-path check: when bg is empty (context / no-newline
// lines), we skip the tint logic entirely so plain ANSI output stays
// minimal and tests that strip ANSI continue to see clean printable text.
func hasBackground(s lipgloss.Style) bool {
	_, ok := s.GetBackground().(lipgloss.NoColor)
	return !ok && s.GetBackground() != nil
}

// chunkBytePrefix returns the byte index in s such that the printable
// width of s[:idx] is exactly want. Walks runes one at a time and
// accumulates ansi.StringWidth so the slice point lands on a rune
// boundary even when SGR codes are present.
func chunkBytePrefix(s string, want int) int {
	if want <= 0 {
		return 0
	}
	acc := 0
	for i := range s {
		// i increments by rune-byte-count per iteration; ansi.StringWidth
		// of the prefix gives the printable cell count consumed so far.
		w := ansi.StringWidth(s[:i])
		if w >= want {
			return i
		}
		acc = i
	}
	if ansi.StringWidth(s) >= want {
		return len(s)
	}
	return acc
}
