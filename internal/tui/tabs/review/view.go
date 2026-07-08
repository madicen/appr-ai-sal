package review

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

func (m *Model) rebuildBody() {
	switch m.phase {
	case phaseRunning:
		m.vp.SetContent(util.EnforceMaxLineWidth(m.renderRunningBody(), m.vp.Width))
	case phaseApprove:
		m.vp.SetContent(util.EnforceMaxLineWidth(m.renderAgentTab(m.activeAgent()), m.vp.Width))
	case phaseGeneratingSummary:
		m.vp.SetContent(util.EnforceMaxLineWidth(m.renderGeneratingSummaryBody(), m.vp.Width))
	case phaseSummary:
		m.vp.SetContent(util.EnforceMaxLineWidth(m.renderSummaryBody(), m.vp.Width))
	case phaseConfirmApprove:
		m.vp.SetContent(util.EnforceMaxLineWidth(m.renderConfirmApproveBody(), m.vp.Width))
	case phasePosted:
		m.vp.SetContent(util.EnforceMaxLineWidth(m.renderPostedBody(), m.vp.Width))
	}
}

// tabStatusGlyph returns a one-cell status glyph for a tab. Agent tabs
// delegate to tabAgentGlyph (which flags findings/actions with a
// severity-coloured dot); overview and summary tabs get their own markers.
func (m *Model) tabStatusGlyph(t reviewTab) string {
	switch t.kind {
	case tabOverview:
		return styles.DimStyle.Render("▦")
	case tabSummary:
		if m.posted {
			return styles.OkStyle.Render("✓")
		}
		if m.done {
			return styles.BoldStyle.Render("★")
		}
		return styles.DimStyle.Render("◌")
	case tabAgent:
		i := m.agentIndex(t.agent)
		if i < 0 {
			return styles.DimStyle.Render("◌")
		}
		return m.tabAgentGlyph(&m.agents[i])
	}
	return " "
}

// tabAgentGlyph picks the one-cell tab-strip marker for an agent tab.
// Unlike agentStatusIcon (used by the running-view rows, where a separate
// detail column already says "N finding(s)" vs "clean"), a completed agent
// that actually produced findings or took an action gets a severity-coloured
// "●" so the user can scan the strip for the tabs worth reading. A
// completed-but-clean agent gets a dim "✓" so it recedes into the bar.
func (m *Model) tabAgentGlyph(a *overlayAgentRow) string {
	switch a.phase {
	case oaRunning:
		return styles.BoldStyle.Render("⏱")
	case oaErr:
		return styles.ErrStyle.Render("✗")
	case oaSkipped:
		return styles.DimStyle.Render("⊘")
	case oaDone:
		if sev, ok := agentNotableSeverity(a); ok {
			return severityDot(sev)
		}
		return styles.DimStyle.Render("✓")
	}
	return styles.DimStyle.Render("◌")
}

// agentNotableSeverity reports whether a completed agent has something worth
// flagging in the tab strip, and at what severity. Specialists and PR agents
// are flagged by their most severe finding; the repo arbiter by whether it
// took any suppress/demote action; the vibe coach by its verdict (or
// surviving paste-ready prompts). A clean agent returns ("", false) so the
// strip shows a dim check instead of a coloured dot.
func agentNotableSeverity(a *overlayAgentRow) (review.Severity, bool) {
	switch {
	case a.name == review.SpecVibeCoach:
		if review.NormalizeVibeVerdict(a.verdict) == review.VibeVerdictRequestChanges {
			return review.SeverityError, true
		}
		if a.findingsN > 0 { // paste-ready author prompts
			return review.SeverityWarning, true
		}
		return "", false
	case a.name == overlayAgentRepoArbiter:
		if a.findingsN > 0 { // suppress/demote actions
			return review.SeverityInfo, true
		}
		return "", false
	case a.stage == stageGroupContextInjection:
		// Injected-brief counts aren't findings; keep these quiet.
		return "", false
	case a.findingsN > 0:
		return maxFindingSeverity(a.findings), true
	}
	return "", false
}

// maxFindingSeverity returns the highest severity among the findings, or
// info when the list is empty (or carries no recognised severity).
func maxFindingSeverity(fs []review.Finding) review.Severity {
	best := review.SeverityInfo
	bestRank := 0
	for _, f := range fs {
		if r := findingSeverityRank(f.Severity); r > bestRank {
			bestRank = r
			best = f.Severity
		}
	}
	return best
}

// findingSeverityRank orders severities for "highest wins" comparisons in
// the tab strip. Unknown values rank 0 so they never outrank a real one.
func findingSeverityRank(s review.Severity) int {
	switch s {
	case review.SeverityCritical:
		return 4
	case review.SeverityError:
		return 3
	case review.SeverityWarning:
		return 2
	case review.SeverityInfo:
		return 1
	}
	return 0
}

// severityDot renders the filled tab-strip marker in the matching severity
// colour from the active theme.
func severityDot(s review.Severity) string {
	switch s {
	case review.SeverityCritical:
		return styles.SevCritical.Render("●")
	case review.SeverityError:
		return styles.SevError.Render("●")
	case review.SeverityWarning:
		return styles.SevWarning.Render("●")
	default:
		return styles.SevInfo.Render("●")
	}
}

// renderTabBar draws the single-line tab strip shown above the viewport.
// Each tab is wrapped in a bubblezone for click selection; the active tab
// is highlighted. When the strip is wider than the modal it windows
// around the active tab with ‹ / › overflow markers so the active tab is
// always visible and no zone-marked label is cut mid-cell.
func (m *Model) renderTabBar(width int) string {
	if len(m.tabs) == 0 {
		return ""
	}
	// Pre-render each tab's labelled, zoned segment.
	segs := make([]string, len(m.tabs))
	widths := make([]int, len(m.tabs))
	for i, t := range m.tabs {
		label := m.tabStatusGlyph(t) + " " + tabShortLabel(t)
		styled := " " + label + " "
		if i == m.activeTab {
			styled = styles.BoldStyle.Render("[" + label + "]")
		} else {
			styled = styles.DimStyle.Render(" " + label + " ")
		}
		segs[i] = zone.Mark(zones.ReviewTab(i), styled)
		widths[i] = ansi.StringWidth(styled)
	}
	// Greedily window around the active tab so it stays visible.
	lo, hi := m.activeTab, m.activeTab
	used := widths[m.activeTab]
	for {
		grew := false
		if lo > 0 && used+widths[lo-1]+1 <= width {
			used += widths[lo-1] + 1
			lo--
			grew = true
		}
		if hi < len(m.tabs)-1 && used+widths[hi+1]+1 <= width {
			used += widths[hi+1] + 1
			hi++
			grew = true
		}
		if !grew {
			break
		}
	}
	var b strings.Builder
	if lo > 0 {
		b.WriteString(styles.DimStyle.Render("‹"))
	}
	for i := lo; i <= hi; i++ {
		if i > lo {
			b.WriteString(" ")
		}
		b.WriteString(segs[i])
	}
	if hi < len(m.tabs)-1 {
		b.WriteString(styles.DimStyle.Render("›"))
	}
	line := b.String()
	if ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, "")
	}
	return line
}

// renderAgentTab renders one per-agent tab: a header, the agent's
// always-present summary of what it did / found, and its findings. While
// the pipeline is still running the findings are read-only; once it
// completes, postable findings become interactive cards (post/skip), and
// findings the repo arbiter suppressed are listed read-only with the
// arbiter's reason.
func (m *Model) renderAgentTab(name string) string {
	rowW := max(8, m.vp.Width)
	i := m.agentIndex(name)
	if i < 0 {
		return styles.DimStyle.Render("(unknown agent)")
	}
	row := &m.agents[i]
	var b strings.Builder

	// Header: agent tag + status detail.
	b.WriteString(styles.RenderTag(name) + "  " + agentStatusDetail(row) + "\n")
	verdict := row.verdict
	if verdict == "" && name == review.SpecVibeCoach && m.draft != nil && m.draft.VibeCoach != nil {
		verdict = m.draft.VibeCoach.Verdict
	}
	if verdict != "" {
		if lbl := review.VibeVerdictShortLabel(review.NormalizeVibeVerdict(verdict)); lbl != "" {
			b.WriteString(styles.DimStyle.Render("Merge recommendation: ") + styles.BoldStyle.Render(lbl) + "\n")
		}
	}
	b.WriteString("\n")

	// Not finished yet — placeholder; the user can come back later. Once
	// the whole pipeline is done (m.done) we always render the agent's
	// summary + findings even if this row's streamed phase wasn't updated.
	if !m.done && (row.phase == oaPending || row.phase == oaRunning) {
		switch row.phase {
		case oaRunning:
			b.WriteString(styles.DimStyle.Render("This agent is still working. Its findings and summary will appear here when it finishes — you can keep browsing other tabs in the meantime.") + "\n")
		default:
			b.WriteString(styles.DimStyle.Render("Queued. This agent hasn't started yet.") + "\n")
		}
		return b.String()
	}
	if row.phase == oaErr && row.err != nil {
		b.WriteString(styles.ErrStyle.Render("✗ "+row.err.Error()) + "\n\n")
	}

	// Always-present summary of what the agent did / found.
	b.WriteString(styles.DimStyle.Render("Summary") + "\n")
	if s := strings.TrimSpace(m.agentSummaryText(name, row)); s != "" {
		b.WriteString(util.RenderMarkdownIndented(s, rowW, 2) + "\n")
	} else {
		b.WriteString(styles.DimStyle.Render("  (no written summary from this agent)") + "\n")
	}
	b.WriteString("\n")

	// Findings.
	if !m.done {
		b.WriteString(m.renderAgentFindingsReadonly(row, rowW))
		b.WriteString("\n" + styles.DimStyle.Render("Posting opens once the whole review completes (the repo arbiter runs after the specialists).") + "\n")
		return b.String()
	}

	idxs := m.agentCardIndices(name)
	inline, general := m.agentFindingBreakdown(name)
	demotedPRWide := m.agentDemotedPRWideFindings(name)
	switch {
	case len(idxs) > 0:
		// Per-agent progress strip + focused interactive card.
		onPR, posted, skipped := m.agentCardTally(name)
		pos := positionOf(idxs, m.idx)
		if pos < 0 {
			m.idx = idxs[0]
			pos = 0
		}
		b.WriteString(styles.BoldStyle.Render(fmt.Sprintf("Finding %d of %d", pos+1, len(idxs))) +
			styles.DimStyle.Render(fmt.Sprintf("  ·  %d already on PR  ·  %d posted  ·  %d skipped", onPR, posted, skipped)) + "\n\n")
		b.WriteString(m.renderCardDetail(rowW))
	case name == review.SpecVibeCoach:
		// The vibe coach never files findings; its merge recommendation
		// (shown above) and author prompts live on the Summary tab. It runs
		// last, so nothing here is ever "suppressed by the repo arbiter".
		b.WriteString(styles.DimStyle.Render("The vibe coach doesn't file individual findings. See the Summary tab for its merge recommendation and author prompts.") + "\n")
	case name == overlayAgentRepoArbiter:
		// The arbiter doesn't file its own findings — it adjusts the
		// specialists' findings. Suppressions / demotions are shown on the
		// affected specialist's tab.
		b.WriteString(styles.DimStyle.Render("The repo arbiter doesn't file findings; it suppresses or demotes the specialists' findings. Those adjustments appear on each affected agent's tab.") + "\n")
	case inline > 0 && m.agentHasArbiterActions(name):
		b.WriteString(styles.DimStyle.Render("All of this agent's inline findings were suppressed by the repo arbiter (see below).") + "\n")
	case general > 0:
		// PR-level / general feedback isn't tied to a diff line, so it has
		// no interactive card — it's posted in the review summary body.
		// Render it read-only here so the agent's tab isn't empty.
		b.WriteString(styles.DimStyle.Render("This agent's feedback is PR-level (not tied to a diff line); it's included in the review summary.") + "\n\n")
		b.WriteString(m.renderAgentGeneralFindings(name, rowW))
	case len(demotedPRWide) > 0:
		// The agent's only surviving output is a PR-wide finding the arbiter
		// demoted below the floor. Don't claim the tab is clean — the
		// read-only demoted block below shows it and offers the opt-in.
		b.WriteString(styles.DimStyle.Render("This agent's PR-wide feedback was demoted below the review threshold by the repo arbiter (see below). It is not posted unless you include it.") + "\n")
	default:
		b.WriteString(styles.OkStyle.Render("No findings from this agent.") + "\n")
	}

	// Demoted PR-wide findings (read-only + opt-in to include in the body).
	if dem := m.renderAgentDemotedPRWide(name, rowW); dem != "" {
		b.WriteString("\n" + dem)
	}

	// Suppressed / demoted findings for this agent (read-only context).
	if sup := m.renderAgentSuppressions(name, rowW); sup != "" {
		b.WriteString("\n" + sup)
	}
	return b.String()
}

// agentSummaryText returns the best available "what did this agent do"
// text: the streamed row summary when present, otherwise derived from the
// adopted draft (specialist Summary, arbiter recap, or vibe-coach
// Summary). This keeps every agent tab populated even when the per-agent
// progress events weren't observed (e.g. a reopened approval).
func (m *Model) agentSummaryText(name string, row *overlayAgentRow) string {
	if s := strings.TrimSpace(row.summary); s != "" {
		return s
	}
	if m.draft == nil {
		return ""
	}
	switch name {
	case overlayAgentRepoArbiter:
		return formatArbiterRowSummary(m.draft.RepoArbiter)
	case review.SpecVibeCoach:
		if m.draft.VibeCoach != nil {
			return strings.TrimSpace(m.draft.VibeCoach.Summary)
		}
		return ""
	}
	for _, s := range m.draft.Specialists {
		if s.Specialist == name {
			return strings.TrimSpace(s.Summary)
		}
	}
	return ""
}

// renderAgentFindingsReadonly lists an agent's streamed findings before
// the pipeline completes (no post/skip actions yet).
func (m *Model) renderAgentFindingsReadonly(row *overlayAgentRow, rowW int) string {
	if len(row.findings) == 0 {
		return styles.OkStyle.Render("No findings from this agent.") + "\n"
	}
	var b strings.Builder
	b.WriteString(styles.DimStyle.Render(fmt.Sprintf("%d finding(s)", len(row.findings))) + "\n")
	for _, f := range row.findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		if loc == "" {
			loc = "(PR-wide)"
		}
		head := "  " + styles.BoldStyle.Render(loc) + "  " + styles.RenderSeverity(string(f.Severity))
		b.WriteString(head + "\n")
		for _, wl := range strings.Split(util.WrapForViewport("    "+strings.TrimSpace(f.Comment), rowW), "\n") {
			b.WriteString(styles.DimStyle.Render(wl) + "\n")
		}
	}
	return b.String()
}

// renderAgentSuppressions renders the repo arbiter's suppressed and
// demoted findings for one agent so the reviewer can see what was held
// back and why.
func (m *Model) renderAgentSuppressions(name string, rowW int) string {
	if m.draft == nil || m.draft.RepoArbiter == nil {
		return ""
	}
	ar := m.draft.RepoArbiter
	var b strings.Builder
	for _, s := range ar.Suppressed {
		if s.Specialist != name {
			continue
		}
		loc := s.Path
		if s.Line > 0 {
			loc = fmt.Sprintf("%s:%d", s.Path, s.Line)
		}
		b.WriteString(styles.WarnStyle.Render("⊘ suppressed ") + styles.BoldStyle.Render(loc) + "\n")
		if r := strings.TrimSpace(s.Reason); r != "" {
			for _, wl := range strings.Split(util.WrapForViewport("    "+r, rowW), "\n") {
				b.WriteString(styles.DimStyle.Render(wl) + "\n")
			}
		}
	}
	for _, d := range ar.Demoted {
		if d.Specialist != name {
			continue
		}
		// PR-wide demotions (no diff anchor) are surfaced with their full
		// text and opt-in toggle by renderAgentDemotedPRWide; don't also
		// list them here as a bare "warning → info" with an empty location.
		if strings.TrimSpace(d.Path) == "" || d.Line <= 0 {
			continue
		}
		loc := fmt.Sprintf("%s:%d", d.Path, d.Line)
		b.WriteString(styles.DimStyle.Render(fmt.Sprintf("▾ demoted %s → %s ", string(d.From), string(d.To))) + styles.BoldStyle.Render(loc) + "\n")
	}
	out := b.String()
	if out == "" {
		return ""
	}
	return styles.DimStyle.Render("Repo arbiter adjustments") + "\n" + out
}

// agentCardTally counts already-on-PR / posted / skipped cards for one agent.
func (m *Model) agentCardTally(name string) (onPR, posted, skipped int) {
	for _, gi := range m.agentCardIndices(name) {
		c := m.cards[gi]
		// A demoted / memory-suppressed card sits at its default skipped state
		// until the reviewer acts; only count it once they post it (a real
		// action). Leaving it out of the skipped count keeps the strip honest
		// about what the reviewer actually did.
		if (c.demoted || c.memorySuppressed) && c.state != cardPosted {
			continue
		}
		switch c.state {
		case cardAlreadyOnPR:
			onPR++
		case cardPosted:
			posted++
		case cardSkipped:
			skipped++
		}
	}
	return onPR, posted, skipped
}

// agentFindingBreakdown counts an agent's inline (diff-anchored) vs
// PR-level / general (body-only) findings as recorded on the adopted
// draft. Used to explain, on an agent tab with no interactive cards, why
// there are none: all-suppressed vs PR-level-only vs genuinely clean.
// Synthetic agents that don't appear in d.Specialists (vibe-coach, repo
// arbiter) return (0, 0).
func (m *Model) agentFindingBreakdown(name string) (inline, general int) {
	if m.draft == nil {
		return 0, 0
	}
	for _, s := range m.draft.Specialists {
		if s.Specialist != name || s.Err != nil {
			continue
		}
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Path) != "" && f.Line > 0 {
				inline++
			} else if strings.TrimSpace(f.Comment) != "" {
				general++
			}
		}
	}
	return inline, general
}

// agentDemotedPRWideFindings returns the agent's PR-wide (body-only)
// findings the repo arbiter demoted below the strictness floor. They were
// pulled out of d.Specialists into d.DemotedHidden, so they would otherwise
// vanish from the agent's tab entirely; we surface them read-only with an
// opt-in "include in the body" toggle. Inline demoted findings are excluded
// here — those flow through the opt-in card path instead.
func (m *Model) agentDemotedPRWideFindings(name string) []review.Finding {
	if m.draft == nil {
		return nil
	}
	var out []review.Finding
	for _, ff := range m.draft.DemotedHidden {
		if ff.Specialist != name {
			continue
		}
		if strings.TrimSpace(ff.Finding.Path) != "" && ff.Finding.Line > 0 {
			continue // inline demoted → handled as a card
		}
		if strings.TrimSpace(ff.Finding.Comment) == "" {
			continue
		}
		out = append(out, ff.Finding)
	}
	return out
}

// renderAgentDemotedPRWide renders the agent's demoted PR-wide findings
// read-only, with the demotion reason and an opt-in affordance so the
// reviewer can include them in the posted body despite the demotion.
func (m *Model) renderAgentDemotedPRWide(name string, rowW int) string {
	findings := m.agentDemotedPRWideFindings(name)
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(styles.DimStyle.Render("Demoted below the review threshold by the repo arbiter") + "\n")
	for _, f := range findings {
		included := m.draft.DemotedPostingEnabled(name, f)
		orig, _ := m.draft.FindingOriginalSeverity(name, f)
		head := styles.WarnStyle.Render("⊘ demoted")
		if strings.TrimSpace(string(orig)) != "" {
			head += styles.DimStyle.Render(fmt.Sprintf(" (from %s)", string(orig)))
		}
		b.WriteString(head + "\n")
		for _, wl := range strings.Split(util.WrapForViewport("    "+strings.TrimSpace(f.Comment), rowW), "\n") {
			b.WriteString(styles.DimStyle.Render(wl) + "\n")
		}
		if included {
			b.WriteString("  " + styles.OkStyle.Render("✓ will be included in the posted review body — press y to exclude") + "\n")
		} else {
			b.WriteString("  " + styles.DimStyle.Render("not in the posted review — press y to include it anyway") + "\n")
		}
	}
	return b.String()
}

// agentHasArbiterActions reports whether the repo arbiter suppressed or
// demoted any of this agent's findings, so the "see below" suppression
// section will actually have content to show. The arbiter only ever acts
// on the code specialists, so this is always false for the PR agents,
// vibe-coach, and the arbiter row itself.
func (m *Model) agentHasArbiterActions(name string) bool {
	if m.draft == nil || m.draft.RepoArbiter == nil {
		return false
	}
	for _, s := range m.draft.RepoArbiter.Suppressed {
		if s.Specialist == name {
			return true
		}
	}
	for _, d := range m.draft.RepoArbiter.Demoted {
		if d.Specialist == name {
			return true
		}
	}
	return false
}

// renderAgentGeneralFindings lists an agent's PR-level (body-only)
// findings read-only. PR agents (description / checks / discussion /
// scope) typically emit only these, so without this their tab would be
// empty even though they had useful feedback.
func (m *Model) renderAgentGeneralFindings(name string, rowW int) string {
	if m.draft == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range m.draft.Specialists {
		if s.Specialist != name || s.Err != nil {
			continue
		}
		for _, f := range s.Findings {
			if strings.TrimSpace(f.Path) != "" && f.Line > 0 {
				continue
			}
			if strings.TrimSpace(f.Comment) == "" {
				continue
			}
			b.WriteString("  " + styles.BoldStyle.Render("(PR-wide)") + "  " + styles.RenderSeverity(string(f.Severity)) + "\n")
			for _, wl := range strings.Split(util.WrapForViewport("    "+strings.TrimSpace(f.Comment), rowW), "\n") {
				b.WriteString(styles.DimStyle.Render(wl) + "\n")
			}
		}
	}
	return b.String()
}

// renderGeneratingSummaryBody is the brief interstitial shown while
// vibe-coach is being re-run because the user changed skips between
// the pipeline-time run and the summary phase. Keeps the user oriented
// (they just pressed "finish" or the last card just resolved) and
// explains why the summary isn't instant.
func (m *Model) renderGeneratingSummaryBody() string {
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Refining summary with your final selections…"))
	b.WriteString("\n\n")
	b.WriteString(styles.DimStyle.Render("Vibe-coach is re-reading the findings you kept and writing fix-prompts that only cover those. This usually takes a few seconds."))
	b.WriteString("\n\n")
	if m.coachErr != nil {
		b.WriteString(styles.ErrStyle.Render("Previous run failed: " + m.coachErr.Error()))
		b.WriteString("\n\n")
	}
	_, posted, skipped := m.tallyCardKinds()
	total := len(m.cards)
	if total > 0 {
		kept := total - skipped
		b.WriteString(styles.DimStyle.Render(fmt.Sprintf("Findings: %d kept · %d skipped · %d posted of %d card(s)", kept, skipped, posted, total)))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) renderRunningBody() string {
	var b strings.Builder
	rowW := max(8, m.vp.Width)
	bodyIndentW := max(8, m.vp.Width-6)

	// Top status strip: total elapsed + counts. Updates on every spinner tick
	// because rebuildBody runs per-tick during phaseRunning.
	doneN, runningN, failedN := m.countAgents()
	totalN := len(m.agents)
	elapsed := humanElapsed(time.Since(m.runStartedAt))
	stageActive := m.activeStageGroup()
	stageActiveLabel := ""
	if meta, ok := stageGroupMetas[stageActive]; ok && runningN > 0 {
		stageActiveLabel = meta.label
	}
	headline := fmt.Sprintf("%s of %d agents complete  ·  %s elapsed",
		styles.BoldStyle.Render(fmt.Sprintf("%d", doneN)), totalN, elapsed)
	if runningN > 0 && stageActiveLabel != "" {
		headline += "  ·  now: " + styles.BoldStyle.Render(stageActiveLabel)
	}
	if failedN > 0 {
		headline += "  ·  " + styles.ErrStyle.Render(fmt.Sprintf("%d failed", failedN))
	}
	b.WriteString(headline + "\n")
	b.WriteString(renderProgressBar(doneN, totalN, max(20, rowW/2)) + "\n")
	if ul := m.usageLine(); ul != "" {
		b.WriteString(ul + "\n")
	}
	b.WriteString("\n")

	// Stage groups in pipeline order. Each group prints a header (with its
	// own done/total) and then its rows, indented by 2 cells.
	for gi, sg := range stageGroupOrder {
		meta := stageGroupMetas[sg]
		note := meta.note
		switch sg {
		case stageGroupSpecialists:
			note = m.specialistsStageNote()
		case stageGroupExperts:
			note = m.repoExpertsStageNote()
		}
		rows := m.agentsInStage(sg)
		gDone, gRunning, gFailed := stageCounts(rows)

		// Group header — chevron reflects state, label bold when running.
		chev, state := stageChevronAndState(gRunning, gDone, gFailed, len(rows))
		labelStyle := styles.DimStyle
		if gRunning > 0 {
			labelStyle = styles.BoldStyle
		}
		header := fmt.Sprintf("%s %s  %s  %s",
			chev,
			labelStyle.Render(meta.label),
			styles.DimStyle.Render("· "+note),
			state)
		b.WriteString(header + "\n")

		// Agent rows.
		for _, row := range rows {
			i := m.agentIndex(row.name)
			b.WriteString(m.renderAgentRow(i, &m.agents[i], rowW, bodyIndentW))
		}
		if gi < len(stageGroupOrder)-1 {
			b.WriteString("\n")
		}
	}

	// Recent log tail — capped so it doesn't push the agents off-screen on
	// short overlays. The newest line is last.
	if len(m.log) > 0 {
		b.WriteString("\n")
		b.WriteString(styles.DimStyle.Render("recent log") + "\n")
		const maxLog = 4
		recent := m.log
		if len(recent) > maxLog {
			recent = recent[len(recent)-maxLog:]
		}
		for _, line := range recent {
			for _, wl := range strings.Split(util.WrapForViewport(styles.DimStyle.Render("  · ")+line, rowW), "\n") {
				b.WriteString(wl + "\n")
			}
		}
	}
	return b.String()
}

// formatPriorActivityLog renders a one-line "tool ran here before" entry
// for the running view's log stream. Counts plus a "last review: 2h ago"
// timer so the user can tell whether this is the first refresh or the
// tenth.
func formatPriorActivityLog(p gh.PriorAprrAISalActivity) string {
	parts := []string{"⟳ appr-ai-sal has reviewed this PR before"}
	if p.InlineCount > 0 {
		parts = append(parts, fmt.Sprintf("%d inline", p.InlineCount))
	}
	if p.ReviewCount > 0 {
		parts = append(parts, fmt.Sprintf("%d review body", p.ReviewCount))
	}
	if !p.LastAt.IsZero() {
		parts = append(parts, "last "+humanElapsed(time.Since(p.LastAt))+" ago")
	}
	return strings.Join(parts, " · ")
}

// formatPriorActivityBanner renders the multi-line acknowledgement panel
// shown above the approval card list and at the top of the post-summary
// screen when prior appr-ai-sal activity is detected on the PR. The
// banner is informational only — the new run still proceeds normally.
func formatPriorActivityBanner(p gh.PriorAprrAISalActivity) string {
	if !p.Found() {
		return ""
	}
	var b strings.Builder
	header := styles.BoldStyle.Render("Note: appr-ai-sal has reviewed this PR before.")
	b.WriteString(header + "\n")
	var bits []string
	if p.InlineCount > 0 {
		bits = append(bits, fmt.Sprintf("%d inline comment(s)", p.InlineCount))
	}
	if p.ReviewCount > 0 {
		bits = append(bits, fmt.Sprintf("%d review body(s)", p.ReviewCount))
	}
	when := ""
	if !p.LastAt.IsZero() {
		when = " · last " + humanElapsed(time.Since(p.LastAt)) + " ago"
	}
	if len(bits) > 0 || when != "" {
		b.WriteString(styles.DimStyle.Render(strings.Join(bits, " · ") + when))
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(p.LastSummarySnippet); s != "" {
		b.WriteString(styles.DimStyle.Render("Last review snippet: ") + s + "\n")
	}
	b.WriteString(styles.DimStyle.Render("This run is a refresh — duplicate inline comments will be marked \"already on PR\" below."))
	return b.String()
}

// formatArbiterRowSummary turns the repo-arbiter's structured result into a
// short multi-line block we put under the row when the user expands it.
// Returns "" when the arbiter ran but had nothing to say — the row then
// renders without an expand chevron (see hasExpandableContent).
//
// The output is shaped as Markdown (paragraphs separated by blank lines,
// `**bold**` labels, `- ` bullet list for the rationale) so the agent-row
// renderer can run it through glamour and the reviewer sees rendered
// prose instead of structured-looking literal text.
func formatArbiterRowSummary(arb *review.RepoArbiterResult) string {
	if arb == nil {
		return ""
	}
	var b strings.Builder
	if s := strings.TrimSpace(arb.UserSummary); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	if n := len(arb.Suppressed); n > 0 {
		fmt.Fprintf(&b, "Suppressed %d finding(s) as out-of-norm for this repo.\n\n", n)
	}
	if n := len(arb.Demoted); n > 0 {
		fmt.Fprintf(&b, "Demoted %d finding(s) one severity rank.\n\n", n)
	}
	if v := strings.TrimSpace(arb.EffectiveVerdict); v != "" {
		if vo := strings.TrimSpace(arb.VerdictOverride); vo != "" {
			fmt.Fprintf(&b, "**Verdict override:** %s.\n\n", vo)
		} else {
			fmt.Fprintf(&b, "**Verdict** (carried from vibe-coach): %s.\n\n", v)
		}
	}
	if n := len(arb.DroppedSuppressions); n > 0 {
		fmt.Fprintf(&b, "Dropped %d invalid suppression(s).\n\n", n)
	}
	if n := len(arb.DroppedDemotions); n > 0 {
		fmt.Fprintf(&b, "Dropped %d invalid demotion(s).\n\n", n)
	}
	if len(arb.RationaleBullets) > 0 {
		b.WriteString("**Rationale:**\n\n")
		for _, r := range arb.RationaleBullets {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", r)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// hasExpandableContent reports whether expanding this row would reveal
// anything (a summary, a retry detail, or an error). When false the
// renderer suppresses the chevron and click-to-expand zone so the row
// doesn't dangle a "▶" affordance that yields an empty body.
func hasExpandableContent(a *overlayAgentRow) bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(a.summary) != "" {
		return true
	}
	if a.phase == oaErr && a.err != nil {
		return true
	}
	if a.retries > 0 && strings.TrimSpace(a.lastRetry) != "" {
		return true
	}
	return false
}

// renderAgentRow renders one agent row (always one line, optionally followed
// by an expanded block). The status icon, agent badge, and right-side detail
// are all on the same line; bubblezone marks the whole row for click-to-expand.
func (m *Model) renderAgentRow(i int, a *overlayAgentRow, rowW, bodyIndentW int) string {
	var b strings.Builder
	cursor := "  "
	if m.cursor == i {
		cursor = "> "
	}
	icon := agentStatusIcon(a)
	tag := styles.RenderTag(a.name)
	right := agentStatusDetail(a)
	chev := " "
	if hasExpandableContent(a) {
		chev = "▶"
		if a.expanded {
			chev = "▼"
		}
	}
	prefix := cursor + chev + " " + icon + " " + tag
	// pad to a stable column so the right-side details line up.
	const tagCol = 36
	prefixW := ansi.StringWidth(prefix)
	pad := tagCol - prefixW
	if pad < 1 {
		pad = 1
	}
	header := prefix + strings.Repeat(" ", pad) + right
	hdrLine := lipgloss.NewStyle().Width(rowW).Align(lipgloss.Left).Render(header)
	if ansi.StringWidth(hdrLine) > rowW {
		hdrLine = ansi.Truncate(hdrLine, rowW, "")
	}
	row := zone.Mark(zoneOverlayAgent(i), hdrLine)
	b.WriteString(row + "\n")
	if a.expanded {
		// Retry detail (most useful failure-mode signal during a long run).
		if a.retries > 0 && strings.TrimSpace(a.lastRetry) != "" {
			line := styles.DimStyle.Render("    last retry: ") + a.lastRetry
			for _, wl := range strings.Split(util.WrapForViewport(line, bodyIndentW), "\n") {
				b.WriteString(wl + "\n")
			}
		}
		if a.phase == oaErr && a.err != nil {
			for _, wl := range strings.Split(util.WrapForViewport(styles.ErrStyle.Render("    ")+a.err.Error(), bodyIndentW), "\n") {
				b.WriteString(wl + "\n")
			}
		}
		if strings.TrimSpace(a.summary) != "" {
			label := "Thoughts"
			switch a.name {
			case overlayAgentRepoArbiter:
				label = "Arbiter notes"
			case review.SpecVibeCoach:
				label = "Vibe coach summary"
			}
			b.WriteString(styles.DimStyle.Render("    "+label) + "\n")
			// Agent summaries are model-produced markdown (or, for the
			// arbiter row, a markdown-shaped recap built from the result
			// struct). Render through glamour so headings, lists, and
			// fenced code blocks show as rendered text instead of raw
			// `**bold**` / `# heading` markup. extraIndent=2 pairs with
			// glamour's built-in 2-cell margin to land the body at the
			// same column as the "    Label" line above it.
			b.WriteString(util.RenderMarkdownIndented(strings.TrimSpace(a.summary), bodyIndentW+4, 2) + "\n")
		}
	}
	return b.String()
}

// agentStatusIcon picks a one-cell glyph for the row's current state.
func agentStatusIcon(a *overlayAgentRow) string {
	switch a.phase {
	case oaRunning:
		return styles.BoldStyle.Render("⏱")
	case oaDone:
		return styles.OkStyle.Render("✓")
	case oaSkipped:
		return styles.DimStyle.Render("⊘")
	case oaErr:
		return styles.ErrStyle.Render("✗")
	}
	return styles.DimStyle.Render("◌")
}

// agentStatusDetail renders the right-side detail for the row: elapsed time
// + result counts when applicable, with retry annotation when present.
func agentStatusDetail(a *overlayAgentRow) string {
	switch a.phase {
	case oaPending:
		return styles.DimStyle.Render("queued")
	case oaRunning:
		base := styles.BoldStyle.Render("running") + styles.DimStyle.Render(" · "+humanElapsed(time.Since(a.startedAt)))
		if a.retries > 0 {
			base += styles.DimStyle.Render(" · retried " + fmt.Sprintf("%d×", a.retries))
		}
		return base
	case oaDone:
		dur := humanElapsed(a.finishedAt.Sub(a.startedAt))
		count := ""
		switch {
		case a.name == overlayAgentRepoArbiter:
			if a.findingsN == 0 {
				count = styles.OkStyle.Render("no actions")
			} else {
				count = styles.OkStyle.Render(fmt.Sprintf("%d action(s)", a.findingsN))
			}
		case a.name == review.SpecVibeCoach:
			// The vibe coach files no findings — findingsN holds its
			// paste-ready author-prompt count. Label it as such so it isn't
			// mistaken for findings; its verdict lives on the Summary tab.
			if a.findingsN > 0 {
				count = styles.OkStyle.Render(fmt.Sprintf("%d prompt(s)", a.findingsN))
			} else {
				count = styles.OkStyle.Render("no prompts")
			}
		case a.stage == stageGroupContextInjection:
			if a.findingsN > 0 {
				count = styles.OkStyle.Render(fmt.Sprintf("injected %d", a.findingsN))
			} else {
				count = styles.OkStyle.Render("loaded")
			}
		case a.findingsN > 0:
			count = styles.OkStyle.Render(fmt.Sprintf("%d finding(s)", a.findingsN))
		case a.findingsN == 0:
			count = styles.OkStyle.Render("clean")
		}
		base := count + styles.DimStyle.Render(" · "+dur)
		if a.retries > 0 {
			base += styles.DimStyle.Render(" · retried " + fmt.Sprintf("%d×", a.retries))
		}
		return base
	case oaSkipped:
		if a.stage == stageGroupContextInjection {
			detail := strings.TrimSpace(a.summary)
			switch {
			case detail == "disabled" || strings.Contains(detail, "disabled"):
				return styles.DimStyle.Render("disabled")
			case strings.HasPrefix(detail, "none injected"):
				// Lang-agents emits "none injected; missing: ..." —
				// surface the full detail so the user knows which
				// languages had no brief on file.
				return styles.DimStyle.Render(detail)
			default:
				return styles.DimStyle.Render("none configured")
			}
		}
		return styles.DimStyle.Render("skipped · no specialist findings")
	case oaErr:
		dur := humanElapsed(a.finishedAt.Sub(a.startedAt))
		base := styles.ErrStyle.Render("failed") + styles.DimStyle.Render(" · "+dur)
		if a.retries > 0 {
			base += styles.DimStyle.Render(" · gave up after " + fmt.Sprintf("%d×", a.retries))
		}
		return base
	}
	return ""
}

// humanElapsed renders a Duration as a short human string ("12s", "1m 30s").
// Negative durations and uninitialized starts (zero) fall back to "—" so the
// renderer can call this unconditionally without guarding every call site.
func humanElapsed(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm %02ds", mins, secs)
}

// renderProgressBar draws a single-row block bar with width cells filled in
// proportion to done/total. Uses ASCII so the cell count matches the terminal
// glyph count exactly.
func renderProgressBar(done, total, width int) string {
	if total <= 0 || width < 4 {
		return ""
	}
	filled := done * width / total
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	bar := styles.OkStyle.Render(strings.Repeat("█", filled)) + styles.DimStyle.Render(strings.Repeat("░", width-filled))
	return bar + styles.DimStyle.Render(fmt.Sprintf("  %d/%d", done, total))
}

// countAgents returns done, running, and failed counts across all agents.
func (m *Model) countAgents() (done, running, failed int) {
	for _, a := range m.agents {
		switch a.phase {
		case oaDone, oaSkipped:
			done++
		case oaRunning:
			running++
		case oaErr:
			failed++
		}
	}
	return done, running, failed
}

// agentsInStage returns the agents belonging to the given stage group, in the
// order they appear in m.agents (which is also pipeline order).
func (m *Model) agentsInStage(sg overlayAgentStage) []*overlayAgentRow {
	var out []*overlayAgentRow
	for i := range m.agents {
		if m.agents[i].stage == sg {
			out = append(out, &m.agents[i])
		}
	}
	return out
}

// activeStageGroup returns the first stage group with any running agent. If
// none are running, returns the first group that still has pending agents
// (the "next up" group). Falls back to specialists.
func (m *Model) activeStageGroup() overlayAgentStage {
	for _, sg := range stageGroupOrder {
		rows := m.agentsInStage(sg)
		for _, r := range rows {
			if r.phase == oaRunning {
				return sg
			}
		}
	}
	for _, sg := range stageGroupOrder {
		rows := m.agentsInStage(sg)
		for _, r := range rows {
			if r.phase == oaPending {
				return sg
			}
		}
	}
	return stageGroupOrder[0]
}

// stageCounts returns done, running, failed for one stage's rows.
func stageCounts(rows []*overlayAgentRow) (done, running, failed int) {
	for _, r := range rows {
		switch r.phase {
		case oaDone, oaSkipped:
			done++
		case oaRunning:
			running++
		case oaErr:
			failed++
		}
	}
	return done, running, failed
}

// stageChevronAndState picks the visual marker and right-side state label
// for a stage header.
func stageChevronAndState(running, done, failed, total int) (string, string) {
	switch {
	case running > 0:
		return styles.BoldStyle.Render("▶"), styles.BoldStyle.Render(fmt.Sprintf("running · %d/%d", done, total))
	case done == total && failed == 0 && total > 0:
		return styles.OkStyle.Render("✓"), styles.OkStyle.Render("done")
	case failed > 0 && (done+failed) == total:
		return styles.ErrStyle.Render("✗"), styles.ErrStyle.Render(fmt.Sprintf("done with %d failed", failed))
	case done > 0 || failed > 0:
		return styles.DimStyle.Render("◐"), styles.DimStyle.Render(fmt.Sprintf("%d/%d", done, total))
	default:
		return styles.DimStyle.Render("◌"), styles.DimStyle.Render("queued")
	}
}

func (m *Model) renderApprovalBody() string {
	if m.idx < 0 || m.idx >= len(m.cards) {
		return styles.DimStyle.Render("(no findings to approve)")
	}
	rowW := max(8, m.vp.Width)
	var b strings.Builder

	if banner := formatPriorActivityBanner(m.priorActivity); banner != "" {
		b.WriteString(banner + "\n\n")
	}

	if m.existingCommentsLoading {
		b.WriteString(styles.DimStyle.Render("Checking GitHub for inline comments you already posted…") + "\n\n")
	}

	// Progress strip: posted/skipped/total
	onPR, posted, skipped := m.tallyCardKinds()
	progress := fmt.Sprintf("Approving %d of %d  ·  %s already on PR  ·  %s posted now  ·  %s skipped",
		m.idx+1, len(m.cards),
		styles.OkStyle.Render(fmt.Sprintf("%d", onPR)),
		styles.OkStyle.Render(fmt.Sprintf("%d", posted)),
		styles.DimStyle.Render(fmt.Sprintf("%d", skipped)),
	)
	b.WriteString(styles.BoldStyle.Render(progress) + "\n\n")
	b.WriteString(m.renderCardDetail(rowW))
	return b.String()
}

// renderCardDetail renders the focused approval card (m.cards[m.idx]):
// specialist + location, anchor warnings, diff hunk, the GitHub comment
// preview, the current state badge, and the post/skip/nav action row.
// Shared by renderApprovalBody and the per-agent tab.
func (m *Model) renderCardDetail(rowW int) string {
	if m.idx < 0 || m.idx >= len(m.cards) {
		return ""
	}
	cur := m.cards[m.idx]
	var b strings.Builder

	// Specialist + location
	b.WriteString(styles.RenderTag(cur.finding.Specialist) + "  ")
	loc := fmt.Sprintf("%s:%d", cur.finding.Finding.Path, cur.finding.Finding.Line)
	b.WriteString(styles.BoldStyle.Render(loc) + "  ")
	b.WriteString(styles.RenderSeverity(string(cur.finding.Finding.Severity)))
	if m.draft != nil {
		if orig, ok := m.draft.FindingOriginalSeverity(cur.finding.Specialist, cur.finding.Finding); ok {
			b.WriteString("  " + styles.DimStyle.Render(fmt.Sprintf("(demoted from %s by repo arbiter)", string(orig))))
		}
	}
	b.WriteString("\n")
	if cur.demoted {
		b.WriteString(styles.WarnStyle.Render("⊘ Demoted below the review threshold by the repo arbiter — it won't post and doesn't affect the verdict.") + "\n")
		b.WriteString(styles.DimStyle.Render("Press y to post it anyway as a normal inline comment.") + "\n")
	}
	if cur.memorySuppressed {
		b.WriteString(styles.WarnStyle.Render(fmt.Sprintf("⊘ Suppressed: you've skipped this pattern %d× in this repo — held back before the arbiter and excluded from the verdict.", cur.memorySuppSkipCount)) + "\n")
		if cur.state == cardPending {
			b.WriteString(styles.DimStyle.Render("Resurfaced — press y to post it, or x to re-suppress.") + "\n")
		} else {
			b.WriteString(styles.DimStyle.Render("Press x to resurface it (then y to post).") + "\n")
		}
	}
	// Anchor auto-correction notes. Two independent code paths can move
	// a finding off its model-reported line, and we surface each so the
	// reviewer can sanity-check the new position before posting:
	//
	//  * Finding.AnchorRelocatedFrom is set by validateAnchorExcerpt
	//    (review pipeline) when the model's quoted excerpt uniquely
	//    matches a different line in the SAME hunk.
	//  * approvalCard.anchorRelocatedFrom is set by anchorCardToDiff
	//    (this file) when the original line fell outside every hunk in
	//    the current diff but the excerpt matched uniquely elsewhere in
	//    the file's hunks — a cross-hunk relocation done at TUI time.
	if from := cur.finding.Finding.AnchorRelocatedFrom; from > 0 && from != cur.finding.Finding.Line {
		b.WriteString(styles.WarnStyle.Render(fmt.Sprintf("⚠ Anchor auto-corrected from line %d → %d based on the model's quoted excerpt; verify the new position.", from, cur.finding.Finding.Line)) + "\n")
	}
	if from := cur.anchorRelocatedFrom; from > 0 && from != cur.finding.Finding.Line {
		b.WriteString(styles.WarnStyle.Render(fmt.Sprintf("⚠ Anchor auto-corrected from line %d → %d based on the model's quoted excerpt; verify the new position before posting.", from, cur.finding.Finding.Line)) + "\n")
	}
	b.WriteString("\n")

	// Diff hunk preview
	if cur.hunk != nil {
		b.WriteString(styles.DimStyle.Render("Diff context") + "\n")
		b.WriteString(renderHunkSnippet(cur.hunk, cur.finding.Finding.Line, 4, rowW))
		b.WriteString("\n")
	} else {
		b.WriteString(styles.DimStyle.Render("(no diff hunk located for this line — F posts as a file-level comment, R refreshes the PR, s skips)") + "\n\n")
	}

	// Comment + suggestion preview
	b.WriteString(styles.DimStyle.Render("Comment GitHub will post") + "\n")
	// The preview body is exactly what GitHub will receive — markdown
	// with an `aiCommentLead` paragraph and (optionally) a fenced
	// ```suggestion block. Run through glamour so the reviewer sees the
	// rendered shape (headings, lists, code-block padding) rather than
	// the literal Markdown source. 2-cell extra indent keeps the body
	// visually nested under the section header.
	preview := review.ReviewCommentBody(cur.finding.Specialist, cur.finding.Finding)
	b.WriteString(util.RenderMarkdownIndented(preview, rowW, 2) + "\n")
	if reason := strings.TrimSpace(cur.finding.Finding.SuggestionStrippedReason); reason != "" {
		b.WriteString("  " + styles.DimStyle.Render("(suggestion stripped: "+reason+")") + "\n")
	}
	if note := strings.TrimSpace(cur.finding.Finding.ActionabilityNote); note != "" {
		b.WriteString("  " + styles.DimStyle.Render("("+note+")") + "\n")
	}
	if cur.finding.Finding.SuggestionSynthesized {
		b.WriteString("  " + styles.DimStyle.Render("(suggestion derived from the comment by appr-ai-sal — verify before posting)") + "\n")
	} else if cur.finding.Finding.SuggestionRepaired {
		b.WriteString("  " + styles.DimStyle.Render("(suggestion generated by appr-ai-sal's repair pass — verify before posting)") + "\n")
	}
	b.WriteString("\n")

	// Status badge for current card
	switch cur.state {
	case cardAlreadyOnPR:
		b.WriteString(styles.OkStyle.Render("✓ already on pull request") + "\n\n")
	case cardPosted:
		if cur.fileLevelPost {
			b.WriteString(styles.OkStyle.Render("✓ posted as file-level comment") + "\n\n")
		} else {
			b.WriteString(styles.OkStyle.Render("✓ posted") + "\n\n")
		}
	case cardSkipped:
		switch {
		case cur.demoted:
			b.WriteString(styles.DimStyle.Render("— not posting (demoted); press y to post anyway") + "\n\n")
		case cur.memorySuppressed:
			b.WriteString(styles.DimStyle.Render("— suppressed by reviewer memory; press x to resurface") + "\n\n")
		default:
			b.WriteString(styles.DimStyle.Render("— skipped") + "\n\n")
		}
	case cardError:
		if cur.err != nil {
			b.WriteString(renderPostErrorBlock(cur.err, rowW))
		} else {
			b.WriteString(styles.ErrStyle.Render("✗ failed") + "\n\n")
		}
	}
	if m.refreshing {
		b.WriteString(styles.DimStyle.Render("⟳ refreshing PR…") + "\n\n")
	} else if m.refreshNote != "" {
		b.WriteString(styles.OkStyle.Render("✓ "+m.refreshNote) + "\n\n")
	}

	// Action row
	left := zone.Mark(zones.StagedPrev, styles.DimStyle.Render(" ← prev "))
	right := zone.Mark(zones.StagedNext, styles.DimStyle.Render(" next → "))
	post := zone.Mark(zones.StagedPost, styles.OkStyle.Render(" Post (y) "))
	skip := zone.Mark(zones.StagedSkip, styles.DimStyle.Render(" Skip (n) "))
	quit := zone.Mark(zones.StagedQuit, styles.ErrStyle.Render(" Abort (q) "))
	row := strings.Join([]string{left, post, skip, right, quit}, "  ")
	b.WriteString(lipgloss.NewStyle().Width(rowW).Render(row))
	b.WriteString("\n")
	if m.dryRun {
		b.WriteString("\n" + styles.ErrStyle.Render("DRY-RUN") + styles.DimStyle.Render(" — Post shows the GitHub payload only; nothing is sent.") + "\n")
	}
	return b.String()
}
