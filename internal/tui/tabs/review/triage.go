package review

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/madicen/appr-ai-sal/internal/review"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
)

// triage.go implements Phase 5 item 5 "finding triage": a view-only
// filter/sort layer over the approval-card list plus the per-severity counts
// shown in the review tab bar. It never mutates the Draft — filtering and
// sorting reorder/hide cards for display and navigation only, so the posted
// review is unaffected by how the reviewer chose to browse it.

// triageSortMode selects the ordering applied to an agent's cards.
type triageSortMode int

const (
	// sortFinding is the natural card order (draft order) — the historical
	// behaviour and the default.
	sortFinding triageSortMode = iota
	// sortSeverityDesc orders by severity, most severe first.
	sortSeverityDesc
	// sortConfidenceDesc orders by the model's self-reported confidence
	// (Q3.4), highest first; findings without a confidence sort last.
	sortConfidenceDesc
	// sortFile orders by path then line, grouping a file's findings together.
	sortFile
)

// triageSortLabel is the short human label for a sort mode (status bar / help).
func triageSortLabel(m triageSortMode) string {
	switch m {
	case sortSeverityDesc:
		return "severity"
	case sortConfidenceDesc:
		return "confidence"
	case sortFile:
		return "file"
	default:
		return "default"
	}
}

// nextTriageSort cycles to the next sort mode (wrapping).
func nextTriageSort(m triageSortMode) triageSortMode {
	return (m + 1) % 4
}

// triageMinSevLabel is the short label for the active severity floor.
func triageMinSevLabel(min review.Severity) string {
	if min == "" {
		return "all"
	}
	return string(min) + "+"
}

// nextTriageMinSev cycles the severity floor: all → warning → error → critical
// → all. info is skipped as a floor (it would be identical to "all" for most
// real diffs); the cycle mirrors the strictness ladder the reviewer already
// knows.
func nextTriageMinSev(min review.Severity) review.Severity {
	switch min {
	case "":
		return review.SeverityWarning
	case review.SeverityWarning:
		return review.SeverityError
	case review.SeverityError:
		return review.SeverityCritical
	default:
		return ""
	}
}

// triageOrder applies the severity floor and the sort mode to a base ordering
// of card indices (typically an agent's cards in draft order), returning the
// filtered + sorted index list. It is pure: cards is read-only, base is not
// mutated, and the returned slice is fresh.
//
// keep is an optional index that is never filtered out even if it is below the
// floor — used to keep the currently-focused card reachable so a filter change
// can't strand the cursor on a hidden card. Pass -1 for no exception.
func triageOrder(cards []approvalCard, base []int, mode triageSortMode, minSev review.Severity, keep int) []int {
	floor := findingSeverityRank(minSev)
	out := make([]int, 0, len(base))
	for _, gi := range base {
		if gi < 0 || gi >= len(cards) {
			continue
		}
		if floor > 0 && gi != keep {
			if findingSeverityRank(cards[gi].finding.Finding.Severity) < floor {
				continue
			}
		}
		out = append(out, gi)
	}
	sortCardIndices(cards, out, mode)
	return out
}

// sortCardIndices sorts idxs in place per the mode. It uses a stable sort so
// cards that compare equal keep their draft order (a sensible tiebreak).
func sortCardIndices(cards []approvalCard, idxs []int, mode triageSortMode) {
	if mode == sortFinding {
		return
	}
	sort.SliceStable(idxs, func(i, j int) bool {
		a, b := cards[idxs[i]].finding.Finding, cards[idxs[j]].finding.Finding
		switch mode {
		case sortSeverityDesc:
			return findingSeverityRank(a.Severity) > findingSeverityRank(b.Severity)
		case sortConfidenceDesc:
			return confidenceValue(a) > confidenceValue(b)
		case sortFile:
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			return a.Line < b.Line
		}
		return false
	})
}

// confidenceValue returns a finding's confidence for sorting; a nil confidence
// sorts as -1 so findings the model didn't score fall to the bottom of a
// confidence-descending sort.
func confidenceValue(f review.Finding) float64 {
	if f.Confidence == nil {
		return -1
	}
	return *f.Confidence
}

// severityTally counts findings by severity across the current card set,
// excluding opt-in demoted / memory-suppressed cards (they aren't part of the
// at-floor set the reviewer is triaging). Cards already on the PR still count —
// the reviewer wants the true severity mix of the review, not just what's left
// to act on.
func severityTally(cards []approvalCard) map[review.Severity]int {
	out := map[review.Severity]int{}
	for _, c := range cards {
		if c.demoted || c.memorySuppressed {
			continue
		}
		sev := c.finding.Finding.Severity
		if sev == "" {
			continue
		}
		out[sev]++
	}
	return out
}

// formatSeverityCounts renders a compact per-severity summary like
// "2 critical · 5 warning" for the tab bar, most-severe first, omitting
// severities with a zero count. Returns "" when there are no counted findings.
func formatSeverityCounts(counts map[review.Severity]int) string {
	order := []review.Severity{
		review.SeverityCritical,
		review.SeverityError,
		review.SeverityWarning,
		review.SeverityInfo,
	}
	var parts []string
	for _, s := range order {
		n := counts[s]
		if n == 0 {
			continue
		}
		parts = append(parts, severityCountStyle(s).Render(fmt.Sprintf("%d %s", n, string(s))))
	}
	return strings.Join(parts, styles.DimStyle.Render(" · "))
}

// actCycleTriageSort advances the card sort mode (Phase 5 item 5) and keeps
// the focused card in view. It's a view-only reorder — the Draft is untouched.
func (m *Model) actCycleTriageSort() (tea.Model, tea.Cmd) {
	m.triageSort = nextTriageSort(m.triageSort)
	m.copyStatus = "sort: " + triageSortLabel(m.triageSort)
	m.ensureFocusInOrder()
	m.rebuildBody()
	return m, nil
}

// actCycleTriageFilter advances the severity floor (Phase 5 item 5). The
// focused card is kept reachable (triageOrder's keep exception) so cycling the
// filter can never strand the cursor on a hidden card.
func (m *Model) actCycleTriageFilter() (tea.Model, tea.Cmd) {
	m.triageMinSev = nextTriageMinSev(m.triageMinSev)
	m.copyStatus = "filter: " + triageMinSevLabel(m.triageMinSev)
	m.ensureFocusInOrder()
	m.rebuildBody()
	return m, nil
}

// actJumpToDiff asks the root model to scroll the PR-detail diff pane to the
// focused finding's anchor (Phase 5 item 4). A no-op when no anchored card is
// focused. The overlay itself doesn't own the diff pane, so it emits a
// JumpToDiffMsg the root handles.
func (m *Model) actJumpToDiff() (tea.Model, tea.Cmd) {
	if m.idx < 0 || m.idx >= len(m.cards) {
		return m, nil
	}
	f := m.cards[m.idx].finding.Finding
	if f.Path == "" || f.Line <= 0 {
		return m, nil
	}
	path, line := f.Path, f.Line
	return m, func() tea.Msg { return JumpToDiffMsg{Path: path, Line: line} }
}

// ensureFocusInOrder makes sure m.idx points at a card present in the current
// agent's triaged order; if the focused card fell out (shouldn't happen given
// the keep exception, but defensive), snap to the first shown card.
func (m *Model) ensureFocusInOrder() {
	idxs := m.agentCardOrder(m.activeAgent())
	if len(idxs) == 0 {
		return
	}
	if positionOf(idxs, m.idx) < 0 {
		m.idx = idxs[0]
	}
}

// renderTriageLine renders the per-tab triage status strip: the active sort
// mode, the severity floor, and (when the floor hides cards) how many of the
// agent's cards are shown vs. total. shown/total are the filtered and raw card
// counts for the agent.
func (m *Model) renderTriageLine(total, shown int) string {
	parts := []string{
		styles.DimStyle.Render("sort: ") + styles.BoldStyle.Render(triageSortLabel(m.triageSort)),
		styles.DimStyle.Render("filter: ") + styles.BoldStyle.Render(triageMinSevLabel(m.triageMinSev)),
	}
	if m.triageMinSev != "" && shown != total {
		parts = append(parts, styles.DimStyle.Render(fmt.Sprintf("%d of %d shown", shown, total)))
	}
	parts = append(parts, styles.DimStyle.Render("(S sort · f filter)"))
	return strings.Join(parts, styles.DimStyle.Render("  ·  "))
}

// severityCountStyle picks the themed foreground style for a severity count
// chip, matching the diff/tab severity colours.
func severityCountStyle(s review.Severity) interface{ Render(...string) string } {
	switch s {
	case review.SeverityCritical:
		return styles.SevCritical
	case review.SeverityError:
		return styles.SevError
	case review.SeverityWarning:
		return styles.SevWarning
	default:
		return styles.SevInfo
	}
}
