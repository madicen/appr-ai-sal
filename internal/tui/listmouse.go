package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

// Item vertical layout matches list.NewDefaultDelegate() with ShowDescription true
// and bubbles/list populatedView: each item is delegate.Height() (=2) text lines
// (title + description); between items the list prints (Spacing()+1) newlines
// (default Spacing 1 → "\n\n"), which is one blank screen row before the next
// item. So advance from one item's top line to the next is 2 + 1 = 3 lines.
const (
	listItemHeight = 2
	listItemGap    = 1
)

// listBodyOriginY is screen Y of the first row of the list widget (inside horizontal padding).
func (m *Model) listBodyOriginY() int {
	filterLines := lipgloss.Height(renderFilterLine(m.explicitReviewerOnly))
	return lipgloss.Height(m.renderHeader()) + filterLines
}

// listChromeTopHeight returns rendered rows above the list item viewport.
// It must stay in sync with charmbracelet/bubbles/list Model.View (title + status),
// including TitleBar / StatusBar lipgloss padding (default styles add a bottom row each).
func listChromeTopHeight(lm list.Model) int {
	var h int
	// list.go: if m.showTitle || (m.showFilter && m.filteringEnabled)
	if lm.ShowTitle() || (lm.ShowFilter() && lm.FilteringEnabled()) {
		h += listTitleBarRenderedHeight(lm)
	}
	if lm.ShowStatusBar() {
		h += listStatusBarRenderedHeight(lm)
	}
	return h
}

// listTitleBarRenderedHeight mirrors list.Model.titleView enough for lipgloss height.
func listTitleBarRenderedHeight(lm list.Model) int {
	var view string
	titleBarStyle := lm.Styles.TitleBar
	switch {
	case lm.ShowFilter() && lm.FilteringEnabled() && lm.FilterState() == list.Filtering:
		view = lm.FilterInput.View()
	case lm.ShowTitle():
		view = lm.Styles.Title.Render(lm.Title)
		if lm.FilterState() != list.Filtering {
			view = ansi.Truncate(view, max(1, lm.Width()), "…")
		}
	default:
		return 0
	}
	if strings.TrimSpace(view) == "" {
		return 0
	}
	return lipgloss.Height(titleBarStyle.Render(view))
}

// listStatusBarRenderedHeight mirrors list.Model.statusView for lipgloss height.
func listStatusBarRenderedHeight(lm list.Model) int {
	var status string
	totalItems := len(lm.Items())
	visibleItems := len(lm.VisibleItems())
	sing, plur := lm.StatusBarItemName()
	itemName := plur
	if visibleItems == 1 {
		itemName = sing
	}
	itemsDisplay := fmt.Sprintf("%d %s", visibleItems, itemName)

	switch lm.FilterState() {
	case list.Filtering:
		if visibleItems == 0 {
			status = lm.Styles.StatusEmpty.Render("Nothing matched")
		} else {
			status = itemsDisplay
		}
	default:
		if totalItems == 0 {
			status = lm.Styles.StatusEmpty.Render("No " + plur)
		} else {
			if lm.FilterState() == list.FilterApplied {
				f := strings.TrimSpace(lm.FilterValue())
				f = ansi.Truncate(f, 10, "…")
				status += fmt.Sprintf("“%s” ", f)
			}
			status += itemsDisplay
		}
	}
	numFiltered := totalItems - visibleItems
	if numFiltered > 0 {
		status += lm.Styles.DividerDot.String()
		status += lm.Styles.StatusBarFilterCount.Render(fmt.Sprintf("%d filtered", numFiltered))
	}
	return lipgloss.Height(lm.Styles.StatusBar.Render(status))
}

// listGlobalIndexAtClick returns the filtered-list index for a left-click in the
// item area, or (-1, false) if the click missed items or while editing filter.
func (m *Model) listGlobalIndexAtClick(msg tea.MouseMsg) (int, bool) {
	if m.list.SettingFilter() {
		return -1, false
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return -1, false
	}
	lm := m.list
	if len(lm.VisibleItems()) == 0 {
		return -1, false
	}

	listTop := m.listBodyOriginY()
	if msg.Y < listTop || msg.Y >= listTop+lm.Height() {
		return -1, false
	}
	if msg.X < 1 || msg.X >= m.width-1 {
		return -1, false
	}

	relY := msg.Y - listTop
	chrome := listChromeTopHeight(lm)
	lineInContent := relY - chrome
	if lineInContent < 0 {
		return -1, false
	}

	vis := lm.VisibleItems()
	start, end := lm.Paginator.GetSliceBounds(len(vis))
	if start >= end {
		return -1, false
	}

	return visibleIndexForContentLine(lineInContent, start, end, listItemHeight, listItemGap)
}

// visibleIndexForContentLine maps a Y offset inside the list item viewport (below
// title/status chrome) to a global index in the visible slice [start,end),
// using the same per-item stride as charmbracelet/bubbles/list populatedView.
func visibleIndexForContentLine(lineInContent, start, end, itemH, gap int) (int, bool) {
	if lineInContent < 0 || start >= end {
		return -1, false
	}
	line := 0
	for idx := start; idx < end; idx++ {
		if lineInContent < line+itemH {
			return idx, true
		}
		line += itemH
		if idx < end-1 {
			line += gap
		}
	}
	return end - 1, true
}

func (m *Model) listLoadDetailAtGlobalIndex(global int) tea.Cmd {
	m.list.Select(global)
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return nil
	}
	ref := gh.Ref{Owner: it.pr.Owner, Repo: it.pr.Repo, Number: it.pr.Number}
	return loadPRDetailCmd(ref)
}
