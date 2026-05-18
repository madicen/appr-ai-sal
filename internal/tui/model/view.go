package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

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
	out := m.overlayStack.View(main, m.width, m.height)
	return zone.Scan(out)
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
		if m.settings == nil {
			return styles.AppPadding.Render("settings unavailable")
		}
		return m.settings.View()
	case modeRepoAgents:
		if m.repoAgents == nil {
			return styles.AppPadding.Render("repo agents unavailable")
		}
		return m.repoAgents.View()
	case modeLangAgents:
		if m.langAgents == nil {
			return styles.AppPadding.Render("language experts unavailable")
		}
		return m.langAgents.View()
	}
	return ""
}

func (m *Model) renderStatus() string {
	dry := ""
	if m.opts.DryRun {
		dry = " · " + styles.ErrStyle.Render("DRY-RUN")
	}
	var hint string
	switch m.mode {
	case modeList:
		owner, repo := m.repoAgentsFreshnessForListSelection()
		lOwner, lRepo, lNum := m.listSelectionForLangFreshness()
		hint = "↑/↓ · click · double-click open · enter · tab fields · / search · u URL · esc clear · f filter · O browser · o/, settings · ctrl+g repo ctx · ctrl+r repo agents · " +
			m.renderBuildLangAgentsHint(lOwner, lRepo, lNum) +
			" · " + m.renderBuildAgentsHint(owner, repo) +
			" · R refresh · q quit" + dry
	case modeDetail:
		// Per-agent state (repo / tech / lang) is owned by the right-hand
		// "Review controls" pane now; the bottom status bar carries only
		// the cross-cutting keybindings.
		hint = "tab pane · j/k nav · space fold · r review · c toggle controls · a reopen approval · O browser · g description · d diff-only · P bulk · esc back" + dry
	case modeSettings:
		hint = "[ ] tabs · ctrl+s save · esc · tab fields · ↑/↓ strictness · wheel · o AI · , review · ctrl+g repo tab · ctrl+c quit" + dry
	case modeRepoAgents:
		hint = "←/→ repo · a add repo · A regen all · click chips · esc close · ctrl+s save edit · ctrl+c quit" + dry
	case modeLangAgents:
		hint = "↑/↓ select · g/r generate or regenerate · d delete cached · esc close · ctrl+c quit" + dry
	}
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
