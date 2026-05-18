package model

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// filterChipDescriptor groups the four bits each filter chip needs:
// the filterMode value it represents, the bubblezone id to wrap it in,
// and the visible label. The slice order is also the cycle order of
// the `f` keybinding (see nextFilterMode), so changing the screen
// layout means changing the keyboard cycle too.
type filterChipDescriptor struct {
	mode  filterMode
	zone  string
	label string
}

var filterChips = []filterChipDescriptor{
	{mode: filterReviewTeams, zone: zones.FilterTeams, label: "teams+you"},
	{mode: filterReviewExplicit, zone: zones.FilterExplicit, label: "explicit only"},
	{mode: filterAuthored, zone: zones.FilterAuthored, label: "my PRs"},
}

// listPanelMargin is the horizontal gap between the rounded panel
// frame and the terminal edge. Mirrors the 1-cell breathing room
// styles.AppPadding gives the rest of the list body so the panel
// lines up with the list rows below it.
const listPanelMargin = 1

// listPanelStyle wraps the top-of-list panel in a rounded border so
// the filter / search / URL controls feel like a real panel rather
// than a free-floating strip. Inner padding gives the chip + input
// rows a comfortable margin on the left and right.
var listPanelStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(styles.PanelBorder).
	Padding(0, 2).
	MarginLeft(listPanelMargin).
	MarginRight(listPanelMargin)

// activeChipStyle / inactiveChipStyle render filter chips. Active
// uses the same purple palette as HeaderBar so the eye reads them as
// "this filter is on" without introducing a second accent colour.
var (
	activeChipStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5D2D91")).
			Padding(0, 2)

	inactiveChipStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#BBBBBB"}).
				Padding(0, 2)

	// refreshChipStyle gives the refresh affordance a matching chip
	// shape so it visually balances the filter chip row on the right.
	refreshChipStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#BBBBBB"}).
				Padding(0, 2)

	// sectionLabelStyle aligns the "filter" / "search" / "url" gutter
	// labels into a fixed-width column so the chips and inputs line up
	// vertically across rows.
	sectionLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#666666", Dark: "#9A9A9A"}).
				Width(8)

	// activeSectionLabelStyle is the focused variant — same alignment
	// as sectionLabelStyle but bold + tinted so the user can tell at a
	// glance which input row their keystrokes will land in.
	activeSectionLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#1F6FEB", Dark: "#58A6FF"}).
				Width(8)

	// inputPromptStyle paints the "▎" gutter beside each text input
	// so the field reads as a slot to type into rather than free
	// text. Focused fields get the accent colour, idle fields stay
	// dim.
	inputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#9A9A9A", Dark: "#6E6E6E"})
	activeInputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#1F6FEB", Dark: "#58A6FF"})
)

// renderListPanel renders the combined filter + search + URL panel
// that sits between the header bar and the bubbles list on modeList.
//
// Row 1 hosts the filter chips (one zone per mode) followed by the
// refresh chip. Row 2 hosts the inline search and URL inputs. The
// focused input's section label flips to the accent colour so the
// user can tell which field their keystrokes are landing in.
func renderListPanel(m *Model) string {
	// Filter chips row.
	chips := make([]string, 0, len(filterChips))
	for _, c := range filterChips {
		style := inactiveChipStyle
		if c.mode == m.filter {
			style = activeChipStyle
		}
		chips = append(chips, zone.Mark(c.zone, style.Render(c.label)))
	}
	refreshLabel := "refresh (R)"
	if !m.prsLoaded {
		refreshLabel = "refreshing…"
	}
	refreshChip := zone.Mark(zones.RefreshList, refreshChipStyle.Render(refreshLabel))

	filterLabel := sectionLabelStyle.Render("filter")
	chipsCluster := strings.Join(chips, "  ")
	filterRow := lipgloss.JoinHorizontal(
		lipgloss.Top,
		filterLabel,
		chipsCluster,
		strings.Repeat(" ", panelGapToRight(m, chipsCluster, refreshChip)),
		refreshChip,
	)

	// Inputs row — search + URL side by side with the same gutter
	// label column as the filter row so everything lines up.
	searchLabelStyle := sectionLabelStyle
	urlLabelStyle := sectionLabelStyle
	searchPromptStyle := inputPromptStyle
	urlPromptStyle := inputPromptStyle
	switch m.listFocus {
	case focusSearch:
		searchLabelStyle = activeSectionLabelStyle
		searchPromptStyle = activeInputPromptStyle
	case focusURL:
		urlLabelStyle = activeSectionLabelStyle
		urlPromptStyle = activeInputPromptStyle
	}

	searchCell := lipgloss.JoinHorizontal(
		lipgloss.Top,
		searchLabelStyle.Render("search"),
		searchPromptStyle.Render("▎ "),
		zone.Mark(zones.SearchField, m.searchInput.View()),
	)
	urlCell := lipgloss.JoinHorizontal(
		lipgloss.Top,
		urlLabelStyle.Render("url"),
		urlPromptStyle.Render("▎ "),
		zone.Mark(zones.URLField, m.urlInput.View()),
	)
	inputsRow := lipgloss.JoinHorizontal(lipgloss.Top, searchCell, "    ", urlCell)

	// One blank row between the chips and the inputs adds a touch of
	// breathing room without inflating the panel beyond the bubbles
	// list's screen budget.
	body := lipgloss.JoinVertical(lipgloss.Left, filterRow, "", inputsRow)
	return listPanelStyle.Width(m.listPanelContentWidth()).Render(body)
}

// panelGapToRight returns how many spaces to insert between the
// filter chips cluster and the refresh chip so the chip floats on
// the far right of the row. Falls back to a small minimum gap when
// the terminal is narrow enough that the chips would otherwise
// collide with refresh.
func panelGapToRight(m *Model, left, right string) int {
	avail := m.listPanelContentWidth() - listPanelStyle.GetHorizontalPadding()
	used := lipgloss.Width(sectionLabelStyle.Render("filter")) + lipgloss.Width(left) + lipgloss.Width(right)
	gap := avail - used
	if gap < 2 {
		return 2
	}
	return gap
}

// listPanelContentWidth is the value passed to listPanelStyle.Width.
// lipgloss treats Width as "content + padding" — the border is rendered
// outside it — so to land the total footprint (margin + border +
// padding + content) exactly inside m.width we have to subtract the
// margin AND the border up front. Forgetting the border subtraction
// pushes the right border one cell past the terminal edge.
func (m *Model) listPanelContentWidth() int {
	w := m.width - 2*listPanelMargin - listPanelStyle.GetHorizontalBorderSize()
	if w < 20 {
		w = 20
	}
	return w
}

// listPanelInputWidth is the per-input character budget used by
// relayout when sizing m.searchInput / m.urlInput. We split the
// available inner space (minus the gutter labels, prompt glyph, and
// the gap between the two columns) evenly between the two fields.
//
// listPanelContentWidth() already excludes border + margin; here we
// only need to subtract padding to land at the bare content area.
func (m *Model) listPanelInputWidth() int {
	inner := m.listPanelContentWidth() - listPanelStyle.GetHorizontalPadding()
	labelW := lipgloss.Width(sectionLabelStyle.Render("search"))
	promptW := lipgloss.Width(inputPromptStyle.Render("▎ "))
	gap := 4 // "    " between the two input cells
	per := (inner - 2*(labelW+promptW) - gap) / 2
	if per < 10 {
		per = 10
	}
	return per
}

// listPanelHeight is the rendered height of renderListPanel for the
// current model state. Used by relayout (and the mouse helpers in
// listmouse.go) to reserve the right number of rows above the list.
func (m *Model) listPanelHeight() int {
	return lipgloss.Height(renderListPanel(m))
}
