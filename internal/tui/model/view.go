package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// renderDescriptionPane renders the PR description as the centre pane's
// full content. Body is treated as markdown — GitHub PR descriptions
// always are — and run through glamour so headings, lists, code fences,
// and links render with proper styling instead of as raw `# foo` text.
// An empty body falls back to a dim hint so the user knows the PR has no
// description rather than thinking the pane failed to load.
func renderDescriptionPane(body string, width int) string {
	width = max(8, width)
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Description") + "  " +
		zone.Mark(zones.DescriptionToggle, styles.DimStyle.Render(" hide (g) ")) + "\n\n")
	body = strings.TrimSpace(body)
	if body == "" {
		b.WriteString(styles.DimStyle.Render("(this PR has no description)"))
		return b.String()
	}
	b.WriteString(util.RenderMarkdownIndented(body, width, 0))
	return b.String()
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}

	header := m.renderHeader()
	body := m.renderBody()
	status := m.renderStatus()
	main := lipgloss.JoinVertical(lipgloss.Left, header, body, status)
	// Composite the PR-detail controls profile dropdown (if open) onto the
	// full-screen view before the overlay stack so it anchors to its
	// trigger using absolute coordinates.
	main = m.overlayControlsProfile(main)
	out := m.overlayStack.View(main, m.width, m.height)
	return zone.Scan(out)
}

// headerHeight is the rendered row count of the chrome header bar. Used as
// the settings body's content origin so an open dropdown's geometric mouse
// hit-test aligns with the on-screen panel.
func (m *Model) headerHeight() int {
	return lipgloss.Height(m.renderHeader())
}

func (m *Model) renderHeader() string {
	switch m.mode {
	case modeList:
		return styles.HeaderBar.Width(m.width).Render("appr-ai-sal · review queue")
	case modeDetail:
		t := "appr-ai-sal · detail"
		if m.currentPR != nil {
			t = fmt.Sprintf("appr-ai-sal · %s#%d  %s", m.currentPR.Repository, m.currentPR.Number, m.currentPR.Title)
		}
		return styles.HeaderBar.Width(m.width).Render(ansi.Truncate(t, m.width-2, "…"))
	case modeSettings:
		return styles.HeaderBar.Width(m.width).Render("appr-ai-sal · settings")
	case modeRepoAgents:
		return styles.HeaderBar.Width(m.width).Render("appr-ai-sal · repo agents")
	case modeLangAgents:
		return styles.HeaderBar.Width(m.width).Render("appr-ai-sal · language experts")
	}
	return ""
}

// renderDetailMiniHeader is a one-line strip above the detail body that shows
// PR meta, diff stats, and quick chips for description / approval reopen.
func (m *Model) renderBody() string {
	bodyH := m.chromeBodyHeight()
	switch m.mode {
	case modeList:
		panel := renderListPanel(m)
		if !m.prsLoaded {
			return lipgloss.JoinVertical(lipgloss.Left,
				panel,
				styles.AppPadding.Render(m.spinner.View()+" loading PRs from GitHub…"),
			)
		}
		return lipgloss.JoinVertical(lipgloss.Left, panel, styles.AppPadding.Render(m.list.View()))
	case modeDetail:
		return m.renderPRDetailBody(bodyH)
	case modeSettings:
		if tab := m.tabs[modeSettings]; tab != nil {
			return tab.View()
		}
		return styles.AppPadding.Render("settings unavailable")
	case modeRepoAgents:
		if tab := m.tabs[modeRepoAgents]; tab != nil {
			return tab.View()
		}
		return styles.AppPadding.Render("repo agents unavailable")
	case modeLangAgents:
		if tab := m.tabs[modeLangAgents]; tab != nil {
			return tab.View()
		}
		return styles.AppPadding.Render("language experts unavailable")
	}
	return ""
}

// statusSeg is one ` · `-separated chunk of the bottom status bar. When
// zone is non-empty the chunk is wrapped in a bubblezone marker so a
// mouse click on the hint dispatches the same action its key would (see
// handleStatusBarMouse). Descriptive-only chunks leave zone empty.
type statusSeg struct {
	text string
	zone string
}

// joinStatusSegs marks the clickable chunks and joins everything with the
// status bar's ` · ` separator. bubblezone markers are ANSI escape
// sequences that lipgloss width methods ignore, so the wrapping/height
// behaviour documented below is unchanged by marking.
func joinStatusSegs(segs []statusSeg) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if s.text == "" {
			continue
		}
		if s.zone != "" {
			parts = append(parts, zone.Mark(s.zone, s.text))
		} else {
			parts = append(parts, s.text)
		}
	}
	return strings.Join(parts, " · ")
}

func (m *Model) statusSegs() []statusSeg {
	km := m.keys
	// seg builds a status segment whose label is derived from a keymap
	// binding (keys.SegText) so the hint text can never drift from the key
	// that triggers it. The zone (when set) makes the hint clickable.
	seg := func(b key.Binding, z string) statusSeg {
		return statusSeg{text: keys.SegText(b), zone: z}
	}
	var segs []statusSeg
	switch m.mode {
	case modeList:
		owner, repo := m.repoAgentsFreshnessForListSelection()
		lOwner, lRepo, lNum := m.listSelectionForLangFreshness()
		segs = []statusSeg{
			seg(km.ListNav, ""),
			seg(km.ListClick, ""),
			seg(km.ListOpenClick, ""),
			seg(km.ListOpen, ""),
			seg(km.ListCycleFocus, ""),
			seg(km.ListSearch, zones.StatusSearch),
			seg(km.ListURL, zones.StatusURL),
			seg(km.ListClearSearch, ""),
			seg(km.ListFilter, zones.StatusFilter),
			seg(km.Browser, zones.StatusOpenBrowser),
			seg(km.SettingsAI, zones.StatusSettingsAI),
			seg(km.RepoCtx, zones.StatusRepoCtx),
			seg(km.RepoAgents, zones.StatusRepoAgents),
			// The lang / build-agents hints carry a dynamic freshness
			// suffix (missing / stale), so their text is rendered from the
			// per-PR freshness state rather than the plain binding label;
			// the base label ("ctrl+l lang experts" / "ctrl+b build
			// agents") still matches the keymap keys.
			{text: m.renderBuildLangAgentsHint(lOwner, lRepo, lNum), zone: zones.StatusLangAgents},
			{text: m.renderBuildAgentsHint(owner, repo), zone: zones.StatusBuildAgents},
			seg(km.ListRefresh, zones.StatusRefresh),
			seg(km.Palette, zones.StatusPalette),
			seg(km.Help, zones.StatusHelp),
			seg(km.ListQuit, zones.StatusQuit),
		}
	case modeDetail:
		// Per-agent state (repo / tech / lang) is owned by the right-hand
		// "Review controls" pane now; the bottom status bar carries only
		// the cross-cutting keybindings.
		segs = []statusSeg{
			seg(km.DetailCyclePane, zones.StatusCyclePane),
			seg(km.DetailNav, ""),
			seg(km.DetailFold, ""),
			seg(km.DetailReview, zones.StatusReview),
			seg(km.DetailToggleControls, zones.StatusToggleControls),
			seg(km.DetailReopenApproval, zones.StatusReopenApproval),
			seg(km.Browser, zones.StatusOpenBrowser),
			seg(km.DetailDescription, zones.StatusDescription),
			seg(km.DetailDiffOnly, zones.StatusDiffOnly),
			seg(km.DetailBulk, zones.StatusBulk),
			seg(km.Palette, zones.StatusPalette),
			seg(km.Help, zones.StatusHelp),
			seg(km.DetailBack, zones.StatusBack),
		}
	case modeSettings:
		// The settings tab strip, Save/Cancel, strictness rows, and
		// profile buttons are all already clickable in the body; the
		// only status-only action a mouse can't otherwise reach is quit.
		segs = []statusSeg{
			{text: "[ ] tabs"},
			{text: "ctrl+s save"},
			{text: "esc"},
			{text: "tab fields"},
			{text: "↑/↓ strictness"},
			{text: "wheel"},
			{text: "o AI"},
			{text: ", review"},
			{text: "ctrl+g repo tab"},
			{text: "ctrl+c quit", zone: zones.StatusQuit},
		}
	case modeRepoAgents:
		segs = []statusSeg{
			{text: "←/→ repo"},
			{text: "a add repo"},
			{text: "A regen all"},
			{text: "click chips"},
			{text: "esc close"},
			{text: "ctrl+s save edit"},
			{text: "ctrl+c quit", zone: zones.StatusQuit},
		}
	case modeLangAgents:
		segs = []statusSeg{
			{text: "↑/↓ select"},
			{text: "g/r generate or regenerate"},
			{text: "d delete cached"},
			{text: "esc close"},
			{text: "ctrl+c quit", zone: zones.StatusQuit},
		}
	}
	if m.opts.DryRun {
		segs = append(segs, statusSeg{text: styles.ErrStyle.Render("DRY-RUN")})
	}
	return segs
}

func (m *Model) renderStatus() string {
	hint := joinStatusSegs(m.statusSegs())
	// Pre-wrap the hint at the StatusBar's content width (m.width minus
	// horizontal padding) so its rendered height matches what the
	// terminal will actually display.
	//
	// Without this, a hint longer than the terminal would lipgloss out
	// to a single logical line that the terminal then visually wraps
	// to a second row at write time. chromeBodyHeight reads
	// lipgloss.Height(renderStatus()) and would budget only 1 row,
	// pushing the View() output one row past m.height. The standard
	// renderer's auto-wrap then scrolls the screen up by 1, dropping
	// the header and shifting every row above the status by 1 — but
	// bubblezone's recorded zone bounds still point at the unscrolled
	// View() rows, so panel clicks land 1 row below their visible
	// content. Wrapping here keeps the rendered height honest.
	return styles.StatusBar.Width(m.width).Render(hint)
}

func humanSince(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// truncWidth truncates by byte length (not terminal cells). Safe only for plain
// ASCII; strings with ANSI or wide runes need ansi.Truncate.
func truncWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if len(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return s[:w-1] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
