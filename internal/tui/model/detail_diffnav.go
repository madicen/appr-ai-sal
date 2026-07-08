package model

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/diffview"
)

// detail_diffnav.go implements the Phase 5 item-4 diff navigation wired into
// the PR-detail diff pane: n/p jumping between inline finding tags, the `/`
// in-diff search prompt with next/prev stepping, and jump-from-finding (scroll
// the diff to a given file:line anchor). The pure indexing lives in the
// diffview leaf package; this file owns only the model glue.

// highlighter lazily builds the model's chroma highlighter (NO_COLOR-aware,
// created on first use so the constructors don't all need updating).
func (m *Model) highlighter() *diffview.Highlighter {
	if m.hl == nil {
		m.hl = diffview.NewHighlighter()
	}
	return m.hl
}

// diffScrollTo scrolls the diff viewport so row lands at (or near) the top,
// clamped to the content so we never overscroll past the end.
func (m *Model) diffScrollTo(row int) {
	if row < 0 {
		row = 0
	}
	m.diffView.SetYOffset(row)
}

// jumpDiffForward is the `n` action: step to the next search match when a
// search is active, otherwise to the next inline finding tag.
func (m *Model) jumpDiffForward() {
	if m.diffSearchQuery != "" {
		m.jumpSearchMatch(1)
		return
	}
	m.jumpFindingTag(1)
}

// jumpDiffBackward is the `p` action: step to the previous search match when a
// search is active, otherwise to the previous inline finding tag.
func (m *Model) jumpDiffBackward() {
	if m.diffSearchQuery != "" {
		m.jumpSearchMatch(-1)
		return
	}
	m.jumpFindingTag(-1)
}

// jumpFindingTag moves the diff viewport to the next (dir>0) or previous
// (dir<0) inline finding-tag anchor relative to the current scroll position.
// A no-op when there are no anchors. Returns true when it moved.
func (m *Model) jumpFindingTag(dir int) bool {
	cur := m.diffView.YOffset
	var (
		row int
		ok  bool
	)
	if dir > 0 {
		row, ok = m.diffAnchors.Next(cur)
	} else {
		row, ok = m.diffAnchors.Prev(cur)
	}
	if !ok {
		return false
	}
	m.diffScrollTo(row)
	return true
}

// jumpSearchMatch moves the diff viewport to the next (dir>0) or previous
// (dir<0) in-diff search match, wrapping around. A no-op when the search has no
// matches. Returns true when it moved.
func (m *Model) jumpSearchMatch(dir int) bool {
	if m.diffSearch.Count() == 0 {
		return false
	}
	cur := m.diffView.YOffset
	var (
		row int
		ok  bool
	)
	if dir > 0 {
		row, ok = m.diffSearch.Next(cur + 1)
	} else {
		row, ok = m.diffSearch.Prev(cur - 1)
	}
	if !ok {
		return false
	}
	m.diffScrollTo(row)
	return true
}

// beginDiffSearch opens the in-diff search prompt: it focuses a fresh text
// input that captures the query. Enter commits (jump to first match), esc
// cancels. Returns the input's blink cmd.
func (m *Model) beginDiffSearch() tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "search diff…"
	ti.Prompt = "/"
	ti.SetValue(m.diffSearchQuery)
	ti.CursorEnd()
	m.diffSearchInput = ti
	m.diffSearching = true
	return m.diffSearchInput.Focus()
}

// commitDiffSearch finalizes the search query, rebuilds the match index over
// the current diff content, and jumps to the first match. Empty query clears
// the search.
func (m *Model) commitDiffSearch() {
	m.diffSearchQuery = m.diffSearchInput.Value()
	m.diffSearching = false
	m.refreshDetailViews() // rebuilds m.diffSearch over the current content
	if m.diffSearchQuery == "" {
		return
	}
	if row, ok := m.diffSearch.Next(m.diffView.YOffset); ok {
		m.diffScrollTo(row)
	}
}

// cancelDiffSearch closes the prompt without changing the active query.
func (m *Model) cancelDiffSearch() {
	m.diffSearching = false
}

// clearDiffSearch drops the active query and its highlights.
func (m *Model) clearDiffSearch() {
	m.diffSearchQuery = ""
	m.diffSearching = false
	m.diffSearch = diffview.SearchIndex{}
	m.refreshDetailViews()
}

// handleDiffSearchKey routes keys while the in-diff search prompt is open. It
// owns all input so the query field receives every keystroke; enter commits,
// esc cancels, everything else goes to the text input.
func (m *Model) handleDiffSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.commitDiffSearch()
		return m, nil
	case tea.KeyEsc:
		m.cancelDiffSearch()
		return m, nil
	}
	var cmd tea.Cmd
	m.diffSearchInput, cmd = m.diffSearchInput.Update(msg)
	return m, cmd
}

// JumpToFinding scrolls the PR-detail diff pane to a finding's anchor (Phase 5
// item 4 "jump from an approval card to its diff position"): it selects the
// finding's file in the tree and scrolls the diff so the finding's line sits
// near the top. Returns false when the file/line can't be located in the
// current diff. Safe to call from the review overlay via the root model.
func (m *Model) JumpToFinding(path string, line int) bool {
	if path == "" {
		return false
	}
	file := review.FindFile(m.parsedDiff, path)
	if file == nil {
		return false
	}
	m.selectedFilePath = path
	m.centerView = centerDiff
	m.focusedPane = paneDiff
	// Re-anchor the tree cursor onto the selected file so the panes agree.
	for i, fr := range m.treeRows {
		if fr.Path == path && i < len(m.treeFileToLine) && m.treeFileToLine[i] >= 0 {
			m.treeIdx = m.treeFileToLine[i]
			break
		}
	}
	m.diffView.SetYOffset(0)
	m.refreshDetailViews()
	// After the content is rebuilt, scroll to the rendered row for this line
	// (located via its new-side gutter number); fall back to the file's first
	// finding anchor, then to the top of the file.
	if row, ok := diffRowForNewLine(m.diffViewLines(), line); ok {
		m.diffScrollTo(row)
	} else if row, ok := m.diffAnchors.Next(-1); ok {
		m.diffScrollTo(row)
	}
	return true
}

// diffViewLines returns the full wrapped diff-pane content (all rows, not just
// the visible window). Populated by refreshDetailViews.
func (m *Model) diffViewLines() []string {
	return m.diffContentLines
}
