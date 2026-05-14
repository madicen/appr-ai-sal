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
		m.vp.SetContent(util.EnforceMaxLineWidth(m.renderApprovalBody(), m.vp.Width))
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
	b.WriteString(renderProgressBar(doneN, totalN, max(20, rowW/2)) + "\n\n")

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
	if m.pendingSuppressAck && m.draft != nil {
		rowW := max(8, m.vp.Width)
		allN := len(m.draft.FlatPostableFindings())
		postN := len(m.draft.FlatPostableFindingsForPost())
		var b strings.Builder
		if banner := formatPriorActivityBanner(m.priorActivity); banner != "" {
			b.WriteString(banner + "\n\n")
		}
		b.WriteString(styles.ErrStyle.Render("Repo arbiter") + " will skip " + fmt.Sprintf("%d", allN-postN) +
			" of " + fmt.Sprintf("%d", allN) + " inline comment(s) (not in GitHub batch).\n\n")
		if ar := m.draft.RepoArbiter; ar != nil && len(ar.Demoted) > 0 {
			b.WriteString(styles.DimStyle.Render(fmt.Sprintf("It also demoted %d finding(s) one severity rank.", len(ar.Demoted))) + "\n\n")
		}
		if len(m.draft.ConventionWitness) > 0 {
			b.WriteString(styles.DimStyle.Render(fmt.Sprintf("Convention witness classified %d testing/docs finding(s) against the repo's evidence.", len(m.draft.ConventionWitness))) + "\n\n")
		}
		if ar := m.draft.RepoArbiter; ar != nil && strings.TrimSpace(ar.UserSummary) != "" {
			// Arbiter UserSummary is LLM-produced markdown; render it
			// so headings / lists / emphasis look like prose, not source.
			b.WriteString(util.RenderMarkdownIndented(strings.TrimSpace(ar.UserSummary), rowW, 0) + "\n\n")
		}
		b.WriteString(styles.BoldStyle.Render("Press Enter or Space to acknowledge and continue") + " · q abort\n")
		return b.String()
	}
	if m.idx < 0 || m.idx >= len(m.cards) {
		return styles.DimStyle.Render("(no findings to approve — press y to post the summary, or skip)")
	}
	rowW := max(8, m.vp.Width)
	cur := m.cards[m.idx]
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
		b.WriteString(styles.DimStyle.Render("— skipped") + "\n\n")
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
	finish := zone.Mark(zones.StagedFinish, styles.BoldStyle.Render(" Skip rest, post summary (f) "))
	quit := zone.Mark(zones.StagedQuit, styles.ErrStyle.Render(" Abort (q) "))
	row := strings.Join([]string{left, post, skip, right, finish, quit}, "  ")
	b.WriteString(lipgloss.NewStyle().Width(rowW).Render(row))
	b.WriteString("\n")
	if m.dryRun {
		b.WriteString("\n" + styles.ErrStyle.Render("DRY-RUN") + styles.DimStyle.Render(" — Post shows the GitHub payload only; nothing is sent.") + "\n")
	}
	return b.String()
}
