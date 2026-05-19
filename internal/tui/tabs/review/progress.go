package review

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/review"
)

// CloseMsg is the signal the overlay sends to the root model when
// the user is done (or chose to abort). Root then pops the stack.
type CloseMsg struct{}

// ChromeTitleFallback is the static tab title the bubble-overlay stack
// renders when our OverlayTitle() method returns "" (which it never does
// in practice — it always reports a phase-specific label). Lives on the
// reviewtab package so detail.go can pass the same string into
// EnableWindowChrome without re-declaring it.
const ChromeTitleFallback = "appr-ai-sal · review"

// OnOverlayClose fires when the bubble-overlay WindowChrome [x] button
// is clicked (or any other library-driven Pop). The library has already
// removed this entry from the stack by the time we're called, so we
// only need to emit a CloseMsg so the root model can run the same
// cleanup it does when the user presses esc/q (clear m.currentReviewOverlay,
// refresh detail views, etc.). The redundant overlayStack.Pop() in the
// CloseMsg handler is a no-op on the empty stack — Pop is idempotent —
// so we don't double-pop.
func (m *Model) OnOverlayClose() tea.Cmd {
	return func() tea.Msg { return CloseMsg{} }
}

// OverlayTitle satisfies bubble-overlay's OverlayTitler interface so the
// chrome's tab tracks the review's phase ("running", "approving",
// "summary", …) instead of staying frozen on the static
// ChromeTitleFallback the OverlayConfig was constructed with. Returning
// "" would fall back to that static title, but every phase has a
// well-defined label so we always return a non-empty string.
//
// While the modal is minimized AND the pipeline is still working, we
// splice the spinner's current frame into the tab — the tab strip is
// the only visible affordance in that state, so animating it tells the
// user the review hasn't stalled. When the modal is expanded the
// running-phase body already shows per-agent spinners, so we keep the
// title plain to avoid a second source of motion competing for
// attention. The frame comes from the same spinner.Model that drives
// the body's indicators, so they animate in lockstep without a second
// ticker.
//
// Peruse mode is reflected by titleForPhase via its "PERUSE · " prefix,
// so we don't need to special-case it here.
func (m *Model) OverlayTitle() string {
	subtitle := chromeSubtitleForPhase(m)
	working := m.phase == phaseRunning || m.phase == phaseGeneratingSummary
	if working && m.chromeMinimized {
		return ChromeTitleFallback + "  " + m.sp.View() + " · " + subtitle
	}
	return ChromeTitleFallback + " · " + subtitle
}

// chromeSubtitleForPhase keeps OverlayTitle's switch small. The strings
// are intentionally shorter than titleForPhase's body labels — those
// live inside the modal where there's room — because the tab strip
// truncates aggressively when the modal is narrow.
func chromeSubtitleForPhase(m *Model) string {
	switch m.phase {
	case phaseRunning:
		return "running"
	case phaseApprove:
		if m.peruse {
			return "browsing"
		}
		return "approving"
	case phaseGeneratingSummary:
		return "refining"
	case phaseSummary:
		return "summary"
	case phaseConfirmApprove:
		return "approve PR"
	case phasePosted:
		return "complete"
	}
	return "review"
}

// OnOverlayMinimize satisfies bubble-overlay's OverlayMinimizer interface
// and is invoked whenever the user clicks the chrome's minimize /
// restore toggle. We mirror the new state onto chromeMinimized so
// OverlayTitle() can decide whether to splice the spinner glyph into
// the tab (only when minimized — see the OverlayTitle docstring for
// why we don't double up motion when the body is visible).
//
// The library handles the painted rendering switch on its own, and the
// spinner keeps ticking in the background so the title stays animated
// for as long as the user has the modal tucked away.
func (m *Model) OnOverlayMinimize(minimized bool) tea.Cmd {
	m.chromeMinimized = minimized
	return nil
}

// OnOverlayResize satisfies bubble-overlay's OverlayResizer interface so
// the modal's viewport reflows live as the user drags a resize handle
// (or Alt+Shift+arrow grows the chrome).
//
// The library reports the new content rect (chrome border + tab
// already subtracted) and has independently applied its own
// WindowChrome.MinWidth / MinHeight clamps, so we can feed those dims
// straight into ResizeContent — no need to round-trip through
// resizeFromScreen and re-apply the terminal-budget clamp.
func (m *Model) OnOverlayResize(contentW, contentH int) tea.Cmd {
	m.ResizeContent(contentW, contentH)
	m.rebuildBody()
	return nil
}

func (m *Model) mergeProgress(p review.Progress) tea.Cmd {
	switch p.Stage {
	case "checkout":
		if p.Err != nil {
			m.log = append(m.log, "checkout: "+p.Err.Error())
		} else {
			m.log = append(m.log, "worktree: "+p.Detail)
		}
	case "diff":
		if p.Err != nil {
			m.log = append(m.log, "diff: "+p.Err.Error())
		} else {
			m.log = append(m.log, "diff: "+p.Detail)
		}
	case "repo-context":
		m.log = append(m.log, "repo context: "+p.Detail)
	case "context-summary":
		m.log = append(m.log, "context vs change: "+p.Detail)
	case "lang-agents":
		m.applyContextInjection(overlayAgentLangBriefs, p)
	case "tech-agents":
		m.applyContextInjection(overlayAgentTechExperts, p)
	case "repo-agents":
		m.applyContextInjection(overlayAgentRepoExperts, p)
	case "repo-evidence":
		m.log = append(m.log, "repo evidence: "+p.Detail)
	case "convention-witness":
		m.log = append(m.log, "convention witness: "+p.Detail)
	case "specialist":
		// runner emits "<name>:start", "<name>:done", or "<name>:retry N (...)".
		parts := strings.SplitN(p.Detail, ":", 2)
		if len(parts) != 2 {
			return nil
		}
		name, detail := parts[0], parts[1]
		m.applyAgentDetail(name, detail, p)
	case "vibe-coach":
		// runner emits Detail = "start" / "done" / "retry N (...)".
		m.applyAgentDetail(review.SpecVibeCoach, p.Detail, p)
	case "repo-arbiter":
		// runner emits Detail = "start" / "done" / "skipped".
		m.applyAgentDetail(p.Stage, p.Detail, p)
	case "fetch-pr":
		if p.Err != nil {
			m.log = append(m.log, "fetch PR: "+p.Err.Error())
		}
	case "done":
		// Root model receives the same progress message and sets m.draft. The
		// overlay also adopts it directly so we can compute approval cards.
		if p.Final != nil {
			adoptCmd := m.AdoptDraft(p.Final)
			fetchCmd := m.CmdAfterAdoptIfNeeded()
			return tea.Batch(adoptCmd, fetchCmd)
		}
	}
	return nil
}

// applyAgentDetail handles the "start", "done", and "retry N (...)" sub-states
// uniformly across every agent type. It does NOT demote other running agents.
// Multiple agents may be running when parallel dispatch is enabled in repo-context.json.
func (m *Model) applyAgentDetail(name, detail string, p review.Progress) {
	i := m.agentIndex(name)
	if i < 0 {
		return
	}
	row := &m.agents[i]
	switch {
	case detail == "start":
		row.phase = oaRunning
		row.startedAt = time.Now()
		row.finishedAt = time.Time{}
		row.err = nil
		// Move keyboard focus to the most recently started agent so j/k
		// hovering tracks "what just happened", but don't override the user's
		// explicit selection if they've pressed j/k already.
		if m.cursor < 0 || m.cursor >= len(m.agents) {
			m.cursor = i
		}
	case detail == "skipped":
		now := time.Now()
		row.phase = oaSkipped
		row.startedAt = now
		row.finishedAt = now
		row.err = nil
	case detail == "done":
		row.finishedAt = time.Now()
		row.expanded = false
		switch {
		case p.Result != nil && p.Result.Err != nil:
			row.phase = oaErr
			row.err = p.Result.Err
		case p.Result != nil:
			row.phase = oaDone
			row.summary = p.Result.Summary
			row.findingsN = len(p.Result.Findings)
		case p.Vibe != nil && p.Vibe.Err != nil:
			row.phase = oaErr
			row.err = p.Vibe.Err
		case p.Vibe != nil:
			row.phase = oaDone
			row.summary = p.Vibe.Summary
			row.findingsN = len(p.Vibe.Prompts)
		case p.Arbiter != nil && p.Arbiter.Err != nil:
			row.phase = oaErr
			row.err = p.Arbiter.Err
		case p.Arbiter != nil:
			row.phase = oaDone
			row.summary = formatArbiterRowSummary(p.Arbiter)
			row.findingsN = len(p.Arbiter.Suppressed) + len(p.Arbiter.Demoted)
		default:
			row.phase = oaDone
		}
	case strings.HasPrefix(detail, "retry"):
		// detail looks like "retry 2 (parse specialist output: ...)"
		row.retries++
		row.lastRetry = strings.TrimSpace(detail)
	}
}

func (m *Model) agentIndex(name string) int {
	for i := range m.agents {
		if m.agents[i].name == name {
			return i
		}
	}
	return -1
}

// applyContextInjection drives one of the synthetic Context-injection rows
// (language-briefs / tech-experts / repo-experts) from a runner Progress.
//
// Detail strings the runner emits and how this maps them onto row state:
//   - "warning: ..."           → oaErr (load failure; surface to user)
//   - "disabled"               → oaSkipped ("disabled" right-side detail)
//   - "" / "none" / "none ..." → oaSkipped ("none configured" detail)
//   - "injected ..."           → oaDone with the detail as summary and a
//     count of injected briefs in findingsN
//   - "loaded N brief(s)"      → oaDone with N parsed into findingsN
//
// All injection stages are emitted at most once per run, so rows that
// transition straight from oaPending to a terminal phase are expected.
func (m *Model) applyContextInjection(name string, p review.Progress) {
	i := m.agentIndex(name)
	if i < 0 {
		return
	}
	row := &m.agents[i]
	now := time.Now()
	if row.startedAt.IsZero() {
		row.startedAt = now
	}
	row.finishedAt = now
	row.expanded = false
	detail := strings.TrimSpace(p.Detail)
	switch {
	case p.Err != nil:
		row.phase = oaErr
		row.err = p.Err
		row.summary = detail
	case strings.HasPrefix(detail, "warning:"):
		row.phase = oaErr
		row.err = fmt.Errorf("%s", strings.TrimSpace(strings.TrimPrefix(detail, "warning:")))
		row.summary = detail
	case detail == "" || detail == "none" || strings.HasPrefix(detail, "none"):
		row.phase = oaSkipped
		row.summary = detail
		row.findingsN = 0
		// none-injected with a "missing: ..." tail is still useful info
		// for the log so the user notices unconfigured langs/techs.
		if detail != "" && detail != "none" {
			m.log = append(m.log, overlayAgentLabel(name)+": "+detail)
		}
	case detail == "disabled":
		row.phase = oaSkipped
		row.summary = "disabled in repo-context.json"
	case strings.HasPrefix(detail, "loaded "):
		row.phase = oaDone
		row.summary = detail
		row.findingsN = parseLoadedBriefCount(detail)
	case strings.HasPrefix(detail, "injected"):
		row.phase = oaDone
		row.summary = detail
		row.findingsN = countInjectedItems(detail)
	default:
		row.phase = oaDone
		row.summary = detail
	}
}

// parseLoadedBriefCount pulls "N" out of a "loaded N brief(s)" detail
// string. Returns 0 when the format doesn't match (the row still
// renders, just without a count badge).
func parseLoadedBriefCount(detail string) int {
	rest := strings.TrimSpace(strings.TrimPrefix(detail, "loaded"))
	end := strings.Index(rest, " ")
	if end < 0 {
		end = len(rest)
	}
	n := 0
	for _, r := range rest[:end] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// countInjectedItems counts the labels inside a "injected a+b+c" or
// "injected a, b, c" detail string. The runner emits "+" between
// language briefs and tech labels.
func countInjectedItems(detail string) int {
	rest := strings.TrimSpace(strings.TrimPrefix(detail, "injected"))
	rest = strings.TrimSpace(rest)
	// Detail may include "; missing: ..." (lang-agents only); drop it
	// before counting.
	if idx := strings.Index(rest, ";"); idx >= 0 {
		rest = strings.TrimSpace(rest[:idx])
	}
	if rest == "" {
		return 0
	}
	// Normalise "a, b" → "a+b" for a single split path.
	rest = strings.ReplaceAll(rest, ", ", "+")
	rest = strings.ReplaceAll(rest, ",", "+")
	parts := strings.Split(rest, "+")
	n := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}
