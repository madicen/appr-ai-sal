package model

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"
	bubbledropdown "github.com/madicen/bubble-dropdown"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	techagentsstore "github.com/madicen/appr-ai-sal/internal/review/techagents"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// renderControlsPane builds the body of the right-hand "Review controls"
// pane: strictness rows, AI profile picker, agent freshness cards,
// run-mode toggles, and a "Start review" primary button. Width is the
// inner content width inside the pane border.
func (m *Model) renderControlsPane(width int) string {
	if width < 4 {
		width = 4
	}
	var b strings.Builder
	b.WriteString(m.renderControlsStrictness(width))
	b.WriteString("\n")
	b.WriteString(m.renderControlsProfile(width))
	b.WriteString("\n")
	b.WriteString(m.renderControlsAgents(width))
	b.WriteString("\n")
	b.WriteString(m.renderControlsToggles(width))
	b.WriteString("\n")
	b.WriteString(m.renderControlsStartButton(width))
	return b.String()
}

func (m *Model) renderControlsStrictness(width int) string {
	cur := aiconfig.ReviewBalanced
	if m.opts.AIConfig != nil {
		cur = m.opts.AIConfig.ReviewStrictness
	}
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Strictness") + "\n")
	rows := []struct {
		zoneID string
		level  aiconfig.ReviewStrictness
		label  string
		hint   string
	}{
		{zones.ControlsStrictCriticalOnly, aiconfig.ReviewCriticalOnly, "critical only", "merge blockers"},
		{zones.ControlsStrictLenient, aiconfig.ReviewLenient, "lenient", "error + critical"},
		{zones.ControlsStrictBalanced, aiconfig.ReviewBalanced, "balanced", "warning and above"},
		{zones.ControlsStrictStrict, aiconfig.ReviewStrict, "strict", "info-level nits"},
	}
	for _, r := range rows {
		mark := "  "
		if r.level == cur {
			mark = styles.OkStyle.Render("● ")
		}
		line := fmt.Sprintf("%s%-13s %s", mark, r.label, styles.DimStyle.Render(r.hint))
		b.WriteString(zone.Mark(r.zoneID, fitToWidth(line, width)) + "\n")
	}
	return b.String()
}

func (m *Model) renderControlsProfile(width int) string {
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("AI profile") + "\n")
	cfg := m.opts.AIConfig
	if cfg == nil || len(cfg.Profiles) == 0 {
		m.controlsProfileDD = nil
		b.WriteString(styles.DimStyle.Render("(no profiles configured — open settings)") + "\n")
		b.WriteString(zone.Mark(zones.ControlsProfileEdit, styles.DimStyle.Render(" edit in settings (o) ")) + "\n")
		return b.String()
	}
	m.refreshControlsProfileDropdown()
	active := cfg.Active()
	b.WriteString(zone.Mark(zones.ControlsProfileDD, m.controlsProfileDD.TriggerView()) + "\n")
	b.WriteString(styles.DimStyle.Render(active.Summary()) + "\n")
	if len(cfg.Profiles) > 1 {
		b.WriteString(styles.DimStyle.Render(fmt.Sprintf("%d profiles · click to pick or settings", len(cfg.Profiles))) + "\n")
	}
	b.WriteString(zone.Mark(zones.ControlsProfileEdit, styles.DimStyle.Render(" edit in settings (o) ")) + "\n")
	return b.String()
}

// refreshControlsProfileDropdown keeps the dropdown's options/selection in
// sync with the AI config while the panel is closed (the active profile may
// change via the settings tab or other paths). The component has no runtime
// SetOptions, so it is recreated; recreation is skipped while open.
func (m *Model) refreshControlsProfileDropdown() {
	cfg := m.opts.AIConfig
	if cfg == nil || len(cfg.Profiles) == 0 {
		m.controlsProfileDD = nil
		return
	}
	if m.controlsProfileDD != nil && m.controlsProfileDD.Open() {
		return
	}
	names := make([]string, len(cfg.Profiles))
	activeIdx := 0
	for i, p := range cfg.Profiles {
		names[i] = p.Name
		if p.Name == cfg.ActiveProfile {
			activeIdx = i
		}
	}
	d := bubbledropdown.New(
		bubbledropdown.WithOptions(names),
		bubbledropdown.WithInitialIndex(activeIdx),
		bubbledropdown.WithPlaceholder("profile"),
	)
	d.SetZoneManager(zone.DefaultManager)
	m.controlsProfileDD = d
}

// controlsProfileDropdownOpen reports whether the controls-pane profile
// dropdown panel is currently displayed.
func (m *Model) controlsProfileDropdownOpen() bool {
	return m.controlsProfileDD != nil && m.controlsProfileDD.Open()
}

// forwardControlsProfileDropdown routes msg to the dropdown and, when the
// selection changes, applies it as the new active profile. Coordinates are
// absolute screen space (the panel is composited at the root), so no offset
// translation is needed.
func (m *Model) forwardControlsProfileDropdown(msg tea.Msg) tea.Cmd {
	if m.controlsProfileDD == nil {
		return nil
	}
	prev := m.controlsProfileDD.SelectedIndex()
	updated, cmd := m.controlsProfileDD.Update(msg)
	m.controlsProfileDD = updated
	if sel := m.controlsProfileDD.SelectedIndex(); sel != prev {
		m.applyControlsProfileSelection(sel)
	}
	return cmd
}

// handleControlsProfileResult applies an ItemChosenMsg / ItemCanceledMsg to
// the open dropdown (closing it and recording the choice).
func (m *Model) handleControlsProfileResult(msg tea.Msg) tea.Cmd {
	if !m.controlsProfileDropdownOpen() {
		return nil
	}
	return m.forwardControlsProfileDropdown(msg)
}

// applyControlsProfileSelection switches the in-memory active profile to the
// chosen index and refreshes the detail views (mirrors the old ‹/› cycler;
// not persisted to disk — the same as cycling).
func (m *Model) applyControlsProfileSelection(idx int) {
	cfg := m.opts.AIConfig
	if cfg == nil || idx < 0 || idx >= len(cfg.Profiles) {
		return
	}
	_ = cfg.SetActive(cfg.Profiles[idx].Name)
	m.refreshDetailViews()
}

// overlayControlsProfile composites the open profile dropdown panel onto the
// full-screen view. The trigger's absolute position is read from the most
// recent bubblezone scan while closed and frozen while open (the layout does
// not move during a selection), so the panel anchors to the trigger.
func (m *Model) overlayControlsProfile(main string) string {
	dd := m.controlsProfileDD
	if dd == nil {
		return main
	}
	if z := zone.Get(zones.ControlsProfileDD); z != nil && !dd.Open() {
		m.controlsProfileDDRow = z.StartY
		m.controlsProfileDDCol = z.StartX
	}
	if !dd.Open() {
		return main
	}
	tw, th := dd.TriggerSize()
	dd.SetBounds(m.controlsProfileDDRow, m.controlsProfileDDCol, tw, th)
	return dd.ViewWithOverlay(main, m.width, m.height)
}

func (m *Model) renderControlsAgents(width int) string {
	owner, repo, number := "", "", 0
	if m.currentPR != nil {
		owner = m.currentPR.Owner
		repo = m.currentPR.Repo
		number = m.currentPR.Number
	}
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Context agents") + "\n")
	b.WriteString(zone.Mark(zones.ControlsRepoAgents, fitToWidth(repoAgentRow(m.repoAgentsFreshness(owner, repo)), width)) + "\n")
	b.WriteString(zone.Mark(zones.ControlsTechAgents, fitToWidth(techAgentRow(techAgentsFreshness(owner, repo)), width)) + "\n")
	b.WriteString(zone.Mark(zones.ControlsLangAgents, fitToWidth(langAgentRow(m.langAgentsFreshness(owner, repo, number)), width)) + "\n")
	return b.String()
}

func repoAgentRow(state repoagentsstore.Freshness) string {
	label := "Repo agents"
	// Primary action on this row is "open the tab focused on the current
	// PR's repo" (ctrl+r). Regeneration (ctrl+b) lives on its own and is
	// not advertised here because clicking / pressing the row should not
	// trigger an expensive LLM run by default — the user can fire that
	// from inside the tab once they're confirmed at the right repo.
	switch state {
	case repoagentsstore.FreshnessMissing:
		return styles.ErrStyle.Render(" ● ") + label + " " + styles.ErrStyle.Render("missing") + " — " + styles.DimStyle.Render("ctrl+r")
	case repoagentsstore.FreshnessIncomplete:
		return styles.WarnStyle.Render(" ● ") + label + " " + styles.WarnStyle.Render("partial") + " — " + styles.DimStyle.Render("ctrl+r")
	case repoagentsstore.FreshnessStale:
		return styles.WarnStyle.Render(" ● ") + label + " " + styles.WarnStyle.Render("stale") + " — " + styles.DimStyle.Render("ctrl+r")
	case repoagentsstore.FreshnessFresh:
		return styles.OkStyle.Render(" ● ") + label + " " + styles.OkStyle.Render("fresh") + " — " + styles.DimStyle.Render("ctrl+r")
	default:
		return styles.DimStyle.Render(" ● ") + label + " — " + styles.DimStyle.Render("ctrl+r")
	}
}

func techAgentRow(state techagentsstore.Freshness) string {
	label := "Tech experts"
	switch state {
	case techagentsstore.FreshnessMissing:
		// Tech experts are an opt-in per-repo feature with no canonical
		// expected set, so absence is the default — render in dim, not
		// red, so it reads as a hint rather than an error.
		return styles.DimStyle.Render(" ● ") + label + " " + styles.DimStyle.Render("not configured") + " — " + styles.DimStyle.Render("ctrl+t to add")
	case techagentsstore.FreshnessStale:
		return styles.WarnStyle.Render(" ● ") + label + " " + styles.WarnStyle.Render("stale") + " — " + styles.DimStyle.Render("ctrl+t")
	case techagentsstore.FreshnessFresh:
		return styles.OkStyle.Render(" ● ") + label + " " + styles.OkStyle.Render("fresh") + " — " + styles.DimStyle.Render("ctrl+t")
	default:
		return styles.DimStyle.Render(" ● ") + label + " — " + styles.DimStyle.Render("ctrl+t")
	}
}

func langAgentRow(state langagentsstore.Freshness) string {
	label := "Lang experts"
	switch state {
	case langagentsstore.FreshnessMissing:
		return styles.ErrStyle.Render(" ● ") + label + " " + styles.ErrStyle.Render("missing") + " — " + styles.DimStyle.Render("ctrl+l")
	case langagentsstore.FreshnessStale:
		return styles.WarnStyle.Render(" ● ") + label + " " + styles.WarnStyle.Render("stale") + " — " + styles.DimStyle.Render("ctrl+l")
	case langagentsstore.FreshnessFresh:
		return styles.OkStyle.Render(" ● ") + label + " " + styles.OkStyle.Render("fresh") + " — " + styles.DimStyle.Render("ctrl+l")
	default:
		return styles.DimStyle.Render(" ● ") + label + " — " + styles.DimStyle.Render("ctrl+l")
	}
}

// techAgentsFreshness returns the freshness for the current PR's tech
// experts, with a short cache so the renderer doesn't re-read disk on
// every frame. Mirrors the repoAgentsFreshnessCache pattern.
func techAgentsFreshness(owner, repo string) techagentsstore.Freshness {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return techagentsstore.FreshnessUnknown
	}
	return techagentsstore.LoadFreshness(owner, repo, time.Now(), techagentsstore.DefaultStaleAfter)
}

func (m *Model) renderControlsToggles(width int) string {
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Run options") + "\n")
	parallel, _, prAgentsParallel := repoParallelExecutionFlags()
	b.WriteString(zone.Mark(zones.ControlsToggleParallel, fitToWidth(toggleRow("Parallel specialists", parallel), width)) + "\n")
	b.WriteString(zone.Mark(zones.ControlsToggleParallelPRAgents, fitToWidth(toggleRow("Parallel PR agents", prAgentsParallel), width)) + "\n")
	b.WriteString(zone.Mark(zones.ControlsToggleDryRun, fitToWidth(toggleRow("Dry run", m.opts.DryRun), width)) + "\n")
	b.WriteString(zone.Mark(zones.ControlsToggleStartMinimized, fitToWidth(toggleRow("Start review minimized", m.startReviewMinimized), width)) + "\n")
	return b.String()
}

func toggleRow(label string, on bool) string {
	mark := styles.DimStyle.Render("[ ]")
	if on {
		mark = styles.OkStyle.Render("[x]")
	}
	return mark + " " + label
}

func (m *Model) renderControlsStartButton(width int) string {
	var b strings.Builder
	startLabel := " Start review (r) "
	startBtn := styles.OkStyle.Render(startLabel)
	if m.currentPR == nil {
		startBtn = styles.DimStyle.Render(startLabel)
	}
	b.WriteString(zone.Mark(zones.ControlsStartReview, startBtn) + "\n")
	return b.String()
}

// fitToWidth pads / truncates a styled line so it fits exactly w cells.
// Trailing-space padding makes click hit-testing extend to the right
// edge of the pane (zones use printable cell width per row).
func fitToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	cw := ansi.StringWidth(s)
	if cw > w {
		return ansi.Truncate(s, w, "…")
	}
	if cw < w {
		s += strings.Repeat(" ", w-cw)
	}
	return s
}
