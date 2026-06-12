package review

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

// renderPostErrorBlock formats a post-time error so the user can see
// (a) what GitHub said, plus parsed validation details when available,
// (b) a one-line "what to do" hint when we recognise the cause, and
// (c) a clickable "[R] Refresh PR & retry" button.
//
// width is the viewport content width used for soft wrapping.
func renderPostErrorBlock(err error, width int) string {
	if err == nil {
		return ""
	}
	var b strings.Builder
	body := err.Error()
	for _, line := range strings.Split(body, "\n") {
		for _, wl := range strings.Split(util.WrapForViewport(line, width), "\n") {
			b.WriteString(styles.ErrStyle.Render("✗ "+wl) + "\n")
		}
	}
	// Hint banner (only when we recognise the cause).
	if _, ok := gh.IsHeadDrift(err); ok {
		b.WriteString(styles.DimStyle.Render("→ press R or click below to refresh the PR & retry") + "\n")
	} else if gh.IsLineUnresolvable(err) {
		b.WriteString(styles.DimStyle.Render("→ GitHub couldn't anchor the comment to the diff. Press F to post as a file-level comment, R to refresh and retry, or s to skip this finding.") + "\n")
	}
	refresh := zone.Mark(zones.StagedRefresh, styles.BoldStyle.Render(" Refresh PR & retry (R) "))
	b.WriteString("\n" + refresh + "\n\n")
	return b.String()
}

func renderVerdictBanner(canonical, shortLabel string, maxW int) string {
	var border lipgloss.Color
	switch canonical {
	case review.VibeVerdictApprove:
		border = lipgloss.Color("#9ECE6A")
	case review.VibeVerdictRequestChanges:
		border = lipgloss.Color("#E0AF68")
	case review.VibeVerdictComment:
		border = lipgloss.Color("#888888")
	default:
		border = lipgloss.Color("#888888")
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		styles.DimStyle.Render("Merge recommendation · vibe-coach"),
		"",
		styles.BoldStyle.Render(shortLabel),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(max(20, maxW-4))
	return box.Render(inner)
}

func (m *Model) renderSummaryBody() string {
	rowW := max(8, m.vp.Width)
	var b strings.Builder
	b.WriteString(styles.BoldStyle.Render("Final step — post the review summary") + "\n\n")
	if banner := formatPriorActivityBanner(m.priorActivity); banner != "" {
		b.WriteString(banner + "\n\n")
	}
	if m.draft != nil {
		m.syncUserSkipsToDraft()
		// Use the reconciled verdict so the banner matches the GitHub event
		// PostEvent will actually send (request_changes can downgrade to
		// comment when the user skipped every blocker).
		v := review.NormalizeVibeVerdict(m.draft.ReconciledMergeVerdict())
		if lbl := review.VibeVerdictShortLabel(v); lbl != "" {
			b.WriteString(renderVerdictBanner(v, lbl, rowW))
			b.WriteString("\n\n")
		}
	}
	onPR, sessPosted, skippedOnly := m.tallyCardKinds()
	b.WriteString(fmt.Sprintf("Inline comments: %s already on PR, %s posted this session, %s skipped (%d total)\n\n",
		styles.OkStyle.Render(fmt.Sprintf("%d", onPR)),
		styles.OkStyle.Render(fmt.Sprintf("%d", sessPosted)),
		styles.DimStyle.Render(fmt.Sprintf("%d", skippedOnly)),
		len(m.cards)))

	if m.summaryPhaseOfferApproveWithoutSummary() {
		b.WriteString(styles.DimStyle.Render("You have not posted any inline comments this session. Submit GitHub APPROVE with an empty body (a) to approve without publishing the summary below, or post the summary as usual (y).") + "\n\n")
	}

	if m.draft == nil {
		b.WriteString(styles.ErrStyle.Render("(no draft loaded)") + "\n")
		return b.String()
	}
	body := m.draft.RenderBody()
	b.WriteString(styles.DimStyle.Render("Markdown body GitHub will publish (scroll with wheel or ↑/↓):") + "\n")
	b.WriteString("\n")
	// The body is markdown that's about to be POSTed to GitHub as-is; render
	// it through glamour so the reviewer previews approximately what the PR
	// author will see (headings, lists, fenced code blocks) rather than the
	// raw source. The 2-column extra indent keeps it visually grouped under
	// the header line above.
	b.WriteString(util.RenderMarkdownIndented(body, rowW, 2) + "\n")

	if m.summaryDryMsg != "" {
		b.WriteString("\n" + styles.OkStyle.Render("✓ "+m.summaryDryMsg) + "\n")
	}
	if m.summaryErr != nil {
		b.WriteString("\n" + renderPostErrorBlock(m.summaryErr, rowW))
	}
	if m.refreshing {
		b.WriteString("\n" + styles.DimStyle.Render("⟳ refreshing PR…") + "\n")
	} else if m.refreshNote != "" {
		b.WriteString("\n" + styles.OkStyle.Render("✓ "+m.refreshNote) + "\n")
	}
	b.WriteString("\n")
	yes := zone.Mark(zones.StagedSummaryYes, styles.OkStyle.Render(" Post summary (y) "))
	no := zone.Mark(zones.StagedSummaryNo, styles.DimStyle.Render(" Skip summary (n) "))
	// "Approve only" is always offered at phaseSummary so the human
	// reviewer can submit GitHub APPROVE without publishing the
	// summary body, regardless of how the AI verdict came out or
	// whether they already posted inline comments. The contextual hint
	// paragraph above (gated on summaryPhaseOfferApproveWithoutSummary)
	// only nudges them toward this path in the specific "you posted no
	// inline comments" case; the button stays available the rest of the
	// time too.
	approveOnly := ""
	if m.summaryPhaseAllowApproveOnly() {
		approveOnly = zone.Mark(zones.StagedSummaryApproveOnly, styles.OkStyle.Render(" Approve only (a) "))
	}
	q := zone.Mark(zones.StagedQuit, styles.ErrStyle.Render(" Abort (q) "))
	b.WriteString(yes + "  " + no)
	if approveOnly != "" {
		b.WriteString("  " + approveOnly)
	}
	b.WriteString("  " + q + "\n")
	if m.dryRun {
		b.WriteString("\n" + styles.ErrStyle.Render("DRY-RUN") + styles.DimStyle.Render(" — Post shows the body payload only.") + "\n")
	}
	return b.String()
}

// renderConfirmApproveBody is shown when the AI verdict is approve and the
// user has nothing left to walk: a single, deliberately small confirmation
// card. No markdown summary, no per-specialist recap — just an Approve button.
//
// The "no issues found" variant (m.noFindingsApprove) replaces the body with
// a clean "every agent passed" message and posts the rendered body so the
// GitHub APPROVE explains itself instead of looking like a content-free
// thumbs-up.
func (m *Model) renderConfirmApproveBody() string {
	rowW := max(8, m.vp.Width)
	var b strings.Builder
	if m.draft != nil {
		b.WriteString(renderVerdictBanner(review.VibeVerdictApprove, "Approve", rowW))
		b.WriteString("\n\n")
	}

	if m.noFindingsApprove {
		b.WriteString(styles.OkStyle.Render("✓ No issues found by any agent.") + "\n\n")
		b.WriteString(styles.DimStyle.Render("Every configured specialist reviewed this diff and produced no actionable feedback to leave on the diff or in a written summary. It recommends approving this pull request.") + "\n\n")
		b.WriteString(styles.DimStyle.Render("appr-ai-sal is an assistive tool — your APPROVE signal still represents your own review of this PR, not the AI's.") + "\n\n")
		b.WriteString(styles.BoldStyle.Render("Submit GitHub APPROVE on this pull request?") + "\n\n")
		// Two paths into the same APPROVE event:
		//   • "Approve PR (y)" attaches the rendered "no issues found by any
		//     agent" body so the review on GitHub explains the thumbs-up.
		//   • "Approve only (a)" posts APPROVE with no body at all, for
		//     reviewers who don't want any AI-authored text published on the
		//     PR alongside their approval.
		b.WriteString(styles.DimStyle.Render("Pressing ") +
			styles.OkStyle.Render("y") +
			styles.DimStyle.Render(" submits event ") +
			styles.OkStyle.Render("APPROVE") +
			styles.DimStyle.Render(" with a brief body explaining no issues were found.") + "\n")
		b.WriteString(styles.DimStyle.Render("Pressing ") +
			styles.OkStyle.Render("a") +
			styles.DimStyle.Render(" submits event ") +
			styles.OkStyle.Render("APPROVE") +
			styles.DimStyle.Render(" with no comment body — a content-free approval.") + "\n\n")

		yes := zone.Mark(zones.StagedSummaryYes, styles.OkStyle.Render(" Approve PR (y) "))
		approveOnly := zone.Mark(zones.StagedSummaryApproveOnly, styles.OkStyle.Render(" Approve only (a) "))
		q := zone.Mark(zones.StagedQuit, styles.ErrStyle.Render(" Abort (q) "))
		b.WriteString(yes + "  " + approveOnly + "  " + q + "\n")
	} else {
		onPR, sessPosted, _ := m.tallyCardKinds()
		if len(m.cards) > 0 {
			b.WriteString(fmt.Sprintf("%s already on PR · %s posted this session.\n\n",
				styles.OkStyle.Render(fmt.Sprintf("%d", onPR)),
				styles.OkStyle.Render(fmt.Sprintf("%d", sessPosted))))
		} else {
			b.WriteString(styles.DimStyle.Render("No inline findings to post.") + "\n\n")
		}
		if m.approveAfterSkipDisagree {
			b.WriteString(styles.DimStyle.Render("You skipped every suggested inline comment — if you disagree with those objections, you can still submit an APPROVE here (or press n to post the written review instead).") + "\n\n")
		}
		b.WriteString(styles.BoldStyle.Render("Submit GitHub APPROVE on this pull request?") + "\n\n")
		b.WriteString(styles.DimStyle.Render("The review will be submitted with event ") +
			styles.OkStyle.Render("APPROVE") +
			styles.DimStyle.Render(" and an empty body — no summary text, no per-agent recap.") + "\n\n")

		yes := zone.Mark(zones.StagedSummaryYes, styles.OkStyle.Render(" Approve PR (y) "))
		no := zone.Mark(zones.StagedSummaryNo, styles.DimStyle.Render(" No, leave a comment-only review (n) "))
		q := zone.Mark(zones.StagedQuit, styles.ErrStyle.Render(" Abort (q) "))
		b.WriteString(yes + "  " + no + "  " + q + "\n")
	}

	if m.summaryDryMsg != "" {
		b.WriteString("\n" + styles.OkStyle.Render("✓ "+m.summaryDryMsg) + "\n")
	}
	if m.summaryErr != nil {
		b.WriteString("\n" + renderPostErrorBlock(m.summaryErr, rowW))
	}
	if m.refreshing {
		b.WriteString("\n" + styles.DimStyle.Render("⟳ refreshing PR…") + "\n")
	} else if m.refreshNote != "" {
		b.WriteString("\n" + styles.OkStyle.Render("✓ "+m.refreshNote) + "\n")
	}
	if m.dryRun {
		b.WriteString("\n" + styles.ErrStyle.Render("DRY-RUN") + styles.DimStyle.Render(" — Approve shows the GitHub payload only.") + "\n")
	}
	return b.String()
}

func (m *Model) renderPostedBody() string {
	var b strings.Builder
	onPR, sessPosted, skippedOnly := m.tallyCardKinds()
	switch {
	case m.summarySkip:
		b.WriteString(styles.BoldStyle.Render("Done — summary not posted.") + "\n\n")
	case m.noFindingsApprove:
		b.WriteString(styles.BoldStyle.Render(styles.OkStyle.Render("✓ Approved — no issues found by any agent.")) + "\n\n")
	default:
		b.WriteString(styles.BoldStyle.Render(styles.OkStyle.Render("✓ Review posted to GitHub")) + "\n\n")
	}
	if !m.noFindingsApprove {
		b.WriteString(fmt.Sprintf("Inline comments: %s already on PR, %s posted this session, %s skipped (%d total)\n",
			styles.OkStyle.Render(fmt.Sprintf("%d", onPR)),
			styles.OkStyle.Render(fmt.Sprintf("%d", sessPosted)),
			styles.DimStyle.Render(fmt.Sprintf("%d", skippedOnly)),
			len(m.cards)))
	}
	b.WriteString("\n")
	b.WriteString(zone.Mark(zones.PostedOK, styles.DimStyle.Render(" Close (enter) ")) + "\n")
	return b.String()
}

func (m *Model) tallyCardKinds() (onPR, posted, skipped int) {
	for _, c := range m.cards {
		// Demoted (opt-in) cards aren't part of the AI's at-floor finding
		// set: they start skipped by default, so counting them here would
		// distort the summary routing (e.g. "skipped every objection →
		// offer approve") and the verdict the arbiter/vibe-coach settled on.
		if c.demoted {
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

// reviewBodyStyle is the inner padding for the review modal body. The
// rounded purple border is now drawn by the bubble-overlay WindowChrome
// (which also owns the draggable tab and resize handles), so this style
// only adds the breathing room between that chrome's box border and the
// title / viewport / help lines.
//
// Width and Height are deliberately set per-render so the body always
// fills the chrome's expected content rect — the chrome locks
// layer.ContentWidth/Height at Push time from the first View(), and any
// later size mismatch shows up as truncation or trailing space.
var reviewBodyStyle = lipgloss.NewStyle().Padding(1, 2)

func (m *Model) View() string {
	title := styles.BoldStyle.Render(m.titleForPhase()) + "  " + m.spinnerForPhase()
	tabBar := m.renderTabBar(max(8, m.outerW-reviewChromeFrameW-reviewBodyPadW))
	help := styles.DimStyle.Render(m.helpForPhase())
	body := lipgloss.JoinVertical(lipgloss.Left, title, tabBar, "", m.vp.View(), "", help)
	// Render at the chrome's expected content dims (outerW-2 × outerH-4 —
	// box border + chrome rows). Setting an explicit Height pads the body
	// to fill the area when the running phase has few rows so the modal's
	// initial size matches its steady-state size and the user doesn't see
	// the chrome shrink-then-grow as agents complete.
	return reviewBodyStyle.
		Width(max(8, m.outerW-2)).
		Height(max(4, m.outerH-4)).
		Render(body)
}

func (m *Model) titleForPhase() string {
	switch m.phase {
	case phaseRunning:
		if m.done {
			return "Review · overview"
		}
		return "Review in progress"
	case phaseApprove:
		name := m.activeAgent()
		if name != "" {
			return "Review · " + overlayAgentLabel(name)
		}
		return "Review · agent"
	case phaseGeneratingSummary:
		return "Review · refining summary"
	case phaseSummary:
		return "Review · post summary"
	case phaseConfirmApprove:
		return "Review · approve PR"
	case phasePosted:
		return "Review complete"
	}
	return "Review"
}

func (m *Model) spinnerForPhase() string {
	if m.phase == phaseRunning || m.phase == phaseGeneratingSummary {
		return m.sp.View()
	}
	return ""
}

func (m *Model) helpForPhase() string {
	switch m.phase {
	case phaseRunning:
		return "tab/[ ] switch tab · j/k focus row · space expand · q abort · ↑/↓ scroll · wheel"
	case phaseApprove:
		if !m.done {
			return "tab/[ ] switch tab · ↑/↓ scroll · q abort · wheel"
		}
		return "tab/[ ] switch tab · y post · n/s skip · ←/→ finding · R refresh PR · q abort · wheel"
	case phaseGeneratingSummary:
		return "refining summary with your final selections… · q abort"
	case phaseSummary:
		// "a approve only" is always part of the help line at phaseSummary
		// because the Approve only button is always rendered there. The
		// suggestive variant ("approve without summary") is reserved for
		// the case where we also surface the contextual nudge paragraph.
		if m.summaryPhaseOfferApproveWithoutSummary() {
			return "y post summary · a approve without summary · R refresh PR · n skip · q abort · ↑/↓ scroll · wheel"
		}
		return "y post summary · a approve only · R refresh PR · n skip · q abort · ↑/↓ scroll preview · wheel"
	case phaseConfirmApprove:
		if m.noFindingsApprove {
			return "y APPROVE with body · a APPROVE without body · R refresh PR · q abort"
		}
		return "y APPROVE · n leave a comment-only review · R refresh PR · q abort"
	case phasePosted:
		return "enter close"
	}
	return ""
}

// MarkSummaryPosted is called by root after a successful (non-dry-run) summary
// post — moves into phasePosted with the success state.
func (m *Model) MarkSummaryPosted() {
	m.summaryDone = true
	m.posted = true
	// The receipt lives on the summary tab; focus it so the posted body
	// renders and the summary tab's glyph flips to ✓.
	m.activeTab = m.summaryTabIndex()
	m.phase = phasePosted
	m.rebuildBody()
}

// MarkPostError records an error (for inline post, summary post, or approve confirmation).
func (m *Model) MarkPostError(err error) {
	switch m.phase {
	case phaseApprove:
		if m.idx >= 0 && m.idx < len(m.cards) {
			m.cards[m.idx].state = cardError
			m.cards[m.idx].err = err
		}
	case phaseSummary, phaseConfirmApprove:
		m.summaryErr = err
	}
	m.rebuildBody()
}

// renderHunkSnippet draws a small diff window around the target line. Width
// is the viewport content width.
func renderHunkSnippet(h *review.Hunk, target, window, width int) string {
	if h == nil {
		return ""
	}
	lines := review.HunkSnippet(h, target, window)
	var b strings.Builder
	for _, ln := range lines {
		// Build a 6-char gutter: "  NNN " or "+/- NNN" with the correct sign.
		var gutter, body string
		switch ln.Kind {
		case review.DiffAdded:
			gutter = styles.SevWarning.Render("+ ")
			body = ln.Text
		case review.DiffRemoved:
			gutter = styles.SevError.Render("- ")
			body = ln.Text
		case review.DiffNoNewline:
			gutter = styles.DimStyle.Render("  ")
			body = ln.Text
		default:
			gutter = styles.DimStyle.Render("· ")
			body = ln.Text
		}
		focus := ""
		if ln.NewNo == target && ln.Kind != review.DiffRemoved {
			focus = styles.BoldStyle.Render(" ◀ here")
		}
		full := gutter + body + focus
		for _, wl := range strings.Split(util.WrapForViewport(full, width), "\n") {
			b.WriteString(wl + "\n")
		}
	}
	return b.String()
}

func (m *Model) RebuildIfVisible() {
	m.rebuildBody()
}

// SetDryRun lets root toggle dry-run after the overlay was constructed (rare).
func (m *Model) SetDryRun(b bool) { m.dryRun = b }

// Phase enumerates the overlay's screen state. Root reads this via Phase()
// for routing decisions; tests in sibling packages match against the
// exported constants below.
type Phase = overlayPhase

// Exported phase constants mirroring the package-private ones used by the
// overlay state machine. They are aliases (not redeclarations) so root and
// tests can compare against m.Phase() without exporting the iota directly.
const (
	PhaseRunning           = phaseRunning
	PhaseApprove           = phaseApprove
	PhaseGeneratingSummary = phaseGeneratingSummary
	PhaseSummary           = phaseSummary
	PhaseConfirmApprove    = phaseConfirmApprove
	PhasePosted            = phasePosted
)

// CardState mirrors approvalCardState for cross-package consumers (tests).
type CardState = approvalCardState

const (
	CardPending     = cardPending
	CardPosted      = cardPosted
	CardSkipped     = cardSkipped
	CardError       = cardError
	CardAlreadyOnPR = cardAlreadyOnPR
)

// Phase returns the current phase. Useful for root to decide message routing.
func (m *Model) Phase() Phase { return m.phase }

// HasDraft reports whether the overlay has adopted a draft (running phase has
// finished). Root uses this to decide whether to forward data.StagedFindingPostedMsg.
func (m *Model) HasDraft() bool { return m.draft != nil }

// Draft returns the underlying draft (may be nil during running phase).
func (m *Model) Draft() *review.Draft { return m.draft }

// Ref returns the gh.Ref for the in-flight review (zero value if none).
func (m *Model) Ref() gh.Ref {
	if m.draft == nil {
		return gh.Ref{}
	}
	return m.draft.Ref
}

// CoachInFlight reports whether a vibe-coach LLM call is currently
// scheduled. Root tests use this to assert phase transitions advance the
// goroutine state machine.
func (m *Model) CoachInFlight() bool { return m.coachInFlight }

// CardCount returns the number of approval cards. Tests use this to verify
// adoption converted a draft's flat findings into per-card state correctly.
func (m *Model) CardCount() int { return len(m.cards) }

// AgentCursor returns the currently focused agent row index in the running
// phase. Test-only accessor used to observe whether mouse clicks delivered
// through the chrome overlay actually reach handleMouse — clicking on
// agent N's bubblezone region should make AgentCursor() return N.
func (m *Model) AgentCursor() int { return m.cursor }

// AgentZoneName returns the bubblezone marker name for agent row i so
// tests can correlate zone.Get lookups with the row indices used inside
// the package. The underlying helper (zoneOverlayAgent) is unexported
// because it's the only producer of these names; this is its read-only
// cross-package mirror.
func AgentZoneName(i int) string { return zoneOverlayAgent(i) }

// SetCardState mutates the i-th approval card's state. Test-only helper for
// arranging a specific approve-card scenario without exercising the public
// post / skip actions.
func (m *Model) SetCardState(i int, state CardState) {
	if i < 0 || i >= len(m.cards) {
		return
	}
	m.cards[i].state = state
}

// CardStateAt returns the i-th approval card's state. Test-only accessor
// for cross-package tests that drive posting through the root model and
// need to assert the result.
func (m *Model) CardStateAt(i int) CardState {
	if i < 0 || i >= len(m.cards) {
		return cardPending
	}
	return m.cards[i].state
}

// SelectAgentTab focuses the named agent's tab (a specialist key,
// overlayAgentRepoArbiter, or review.SpecVibeCoach). Test-only helper so
// cross-package tests can exercise the per-agent posting flow.
func (m *Model) SelectAgentTab(name string) {
	for i, tb := range m.tabs {
		if tb.kind == tabAgent && tb.agent == name {
			m.focusTab(i)
			return
		}
	}
}

// SetForcedCoachInFlight rigs the overlay's coach-in-flight guard for tests
// that need to simulate "vibe-coach already running against an old skip
// set". Production code sets this implicitly via enterSummary().
func (m *Model) SetForcedCoachInFlight(b bool) { m.coachInFlight = b }

// SetForcedPhase rewrites m.phase. Test-only helper; production transitions
// go through advanceCard / enterSummary / actPost*.
func (m *Model) SetForcedPhase(p Phase) { m.phase = p }

// SkipSetHash exposes the package-private skipSetHash for cross-package
// tests that need to construct a stable hash for a UserSkipPostKeys map.
func SkipSetHash(skips map[string]struct{}) string { return skipSetHash(skips) }
