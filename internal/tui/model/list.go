package model

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/madicen/appr-ai-sal/internal/gh"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
	"github.com/madicen/appr-ai-sal/internal/tui/zones"
)

type prItem struct{ pr gh.PR }

func (i prItem) FilterValue() string { return i.pr.Title + " " + i.pr.Repository }
func (i prItem) Title() string       { return fmt.Sprintf("#%d  %s", i.pr.Number, i.pr.Title) }
func (i prItem) Description() string {
	parts := []string{
		i.pr.Repository,
		"@" + i.pr.Author,
		humanSince(i.pr.UpdatedAt),
	}
	if i.pr.IsDraft {
		parts = append(parts, styles.DimStyle.Render("draft"))
	}
	if badge := reviewStateBadge(i.pr.ReviewState); badge != "" {
		parts = append(parts, badge)
	}
	if hint := viewerActionBadge(i.pr.ReviewState); hint != "" {
		parts = append(parts, hint)
	}
	return strings.Join(parts, " · ")
}

// reviewStateBadge returns the PR-wide approval-state chip ("approved",
// "changes requested", "no review") or empty when we have no review data
// yet. Color is applied with lipgloss so the default list delegate can
// pass it through unmodified.
func reviewStateBadge(rs gh.ReviewState) string {
	switch {
	case strings.EqualFold(rs.Decision, gh.ReviewDecisionApproved):
		return styles.OkStyle.Render("approved")
	case rs.ChangesRequested > 0 || strings.EqualFold(rs.Decision, gh.ReviewDecisionChangesRequested):
		return styles.ErrStyle.Render("changes requested")
	case rs.Approvals > 0:
		// Has at least one approval but GitHub still wants more (typical
		// branch-protection "2 approvals required" case).
		return styles.WarnStyle.Render(fmt.Sprintf("approved x%d · more needed", rs.Approvals))
	case strings.EqualFold(rs.Decision, gh.ReviewDecisionReviewRequired):
		return styles.DimStyle.Render("no review")
	default:
		return ""
	}
}

// sortPRsByActionability returns prs reordered so the PRs most in need of
// the viewer's attention rise to the top. The tiers are:
//
//  0. needs you, direct request — your approval would unblock merge.
//  1. needs you, only via a team request — same urgency but ambiguous owner.
//  2. you've reviewed (commented but not approved) — already weighed in.
//  3. changes requested by someone, you haven't reviewed — author's turn.
//  4. you've already approved — done on your side.
//  5. PR is fully approved — least actionable.
//
// Within each tier we keep the most-recently-updated PR first so an active
// PR doesn't get buried under stale ones. The returned slice is a fresh
// allocation; the input is not mutated, which makes the call cheap to
// embed in the data.PRListMsg handler without surprising callers.
func sortPRsByActionability(prs []gh.PR) []gh.PR {
	out := append([]gh.PR(nil), prs...)
	sort.SliceStable(out, func(i, j int) bool {
		ti := actionabilityTier(out[i].ReviewState)
		tj := actionabilityTier(out[j].ReviewState)
		if ti != tj {
			return ti < tj
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func actionabilityTier(rs gh.ReviewState) int {
	switch {
	case strings.EqualFold(rs.Decision, gh.ReviewDecisionApproved):
		return 5
	case rs.ViewerHasApproved:
		return 4
	case rs.ChangesRequested > 0 && !rs.ViewerHasReviewed:
		return 3
	case rs.ViewerHasReviewed:
		return 2
	case rs.NeedsViewerReview() && rs.ViewerStillRequested:
		return 0
	case rs.NeedsViewerReview():
		return 1
	default:
		return 2
	}
}

// viewerActionBadge surfaces what the viewer specifically should do with the
// PR. "needs you" is the strongest signal (direct request still pending,
// viewer hasn't reviewed). The team-fallback variant covers PRs that landed
// in our queue via team membership only.
func viewerActionBadge(rs gh.ReviewState) string {
	switch {
	case rs.ViewerHasApproved:
		return styles.DimStyle.Render("you approved")
	case rs.ViewerHasReviewed:
		return styles.DimStyle.Render("you reviewed")
	case rs.NeedsViewerReview() && rs.ViewerStillRequested:
		return styles.BoldStyle.Foreground(lipgloss.Color("#7AA2F7")).Render("needs you")
	case rs.NeedsViewerReview():
		return styles.DimStyle.Render("needs you (team)")
	default:
		return ""
	}
}

// Model is the root TUI model.
func (m *Model) resetListClickTracking() {
	m.listClickArmed = false
	m.listClickIndex = 0
	m.listClickAt = time.Time{}
}

func (m *Model) listHandleItemClick(gi int) (tea.Model, tea.Cmd) {
	now := time.Now()
	if m.listClickArmed &&
		m.listClickIndex == gi &&
		!m.listClickAt.IsZero() &&
		now.Sub(m.listClickAt) <= m.listDoubleClickWin {
		m.resetListClickTracking()
		return m, m.listLoadDetailAtGlobalIndex(gi)
	}
	m.list.Select(gi)
	m.listClickArmed = true
	m.listClickIndex = gi
	m.listClickAt = now
	return m, nil
}

func (m *Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.resetListClickTracking()
	switch msg.String() {
	case "q":
		util.FlushMouse()
		return m, tea.Quit
	case "f":
		m.explicitReviewerOnly = !m.explicitReviewerOnly
		m.updateListTitle()
		return m, m.refreshPRListCmd()
	case "u":
		m.mode = modeURLInput
		m.urlInput.Reset()
		m.urlInput.Focus()
		return m, textinput.Blink
	case "R":
		return m, m.refreshPRListCmd()
	case "o":
		return m, m.openSettings(settings.StartAI)
	case ",", "ctrl+@":
		return m, m.openSettings(settings.StartReview)
	case "ctrl+g":
		return m, m.openSettings(settings.StartRepoContext)
	case "ctrl+r":
		// From the list view we can't pre-focus a single repo (the highlight
		// might not even point at a PR), so open the tab as-is.
		return m, m.openRepoAgents("", false)
	case "ctrl+l":
		return m, m.openLangAgents()
	case "ctrl+b":
		// Build/refresh repo agents for the highlighted PR's repo, if any.
		if it, ok := m.list.SelectedItem().(prItem); ok {
			focus := it.pr.Owner + "/" + it.pr.Repo
			return m, m.openRepoAgents(focus, true)
		}
		return m, m.openRepoAgents("", false)
	case "O":
		if it, ok := m.list.SelectedItem().(prItem); ok {
			if u := strings.TrimSpace(it.pr.URL); u != "" {
				return m, util.OpenInBrowserCmd(u)
			}
		}
		return m, nil
	case "enter":
		it, ok := m.list.SelectedItem().(prItem)
		if !ok {
			return m, nil
		}
		ref := gh.Ref{Owner: it.pr.Owner, Repo: it.pr.Repo, Number: it.pr.Number}
		return m, data.LoadPRDetailCmd(ref, m.opts.Demo)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) updateListTitle() {
	if m.explicitReviewerOnly {
		m.list.Title = "PRs · you are explicitly requested"
	} else {
		m.list.Title = "PRs · review requested (@me, incl. teams)"
	}
}

// refreshPRListCmd flips the list back into its loading state and returns the
// fetch command. Centralised so the keyboard binding (R), the filter toggle
// (f / chip click), and the refresh chip click all behave identically — in
// particular, all three flip prsLoaded=false so renderBody swaps the list out
// for the spinner and renderFilterLine flips the chip to "refreshing…".
func (m *Model) refreshPRListCmd() tea.Cmd {
	m.prsLoaded = false
	return data.LoadPRsCmd(m.explicitReviewerOnly, m.opts.Demo)
}

func renderFilterLine(explicit, refreshing bool) string {
	label := "filter: teams+you"
	if explicit {
		label = "filter: explicit reviewer only"
	}
	filterChip := zone.Mark(zones.FilterToggle, styles.BoldStyle.Render("  "+label+"  (click or f)  "))
	refreshLabel := "  refresh (click or R)  "
	if refreshing {
		refreshLabel = "  refreshing…  "
	}
	refreshChip := zone.Mark(zones.RefreshList, styles.DimStyle.Render(refreshLabel))
	return styles.AppPadding.Render(filterChip + " " + refreshChip)
}

func (m *Model) repoAgentsFreshnessForListSelection() (owner, repo string) {
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return "", ""
	}
	return it.pr.Owner, it.pr.Repo
}

// listSelectionForLangFreshness is the lang-agents twin of
// repoAgentsFreshnessForListSelection. Returns owner/repo/number for
// the highlighted PR so renderBuildLangAgentsHint can colour itself.
// Empty triple means "no selection" (or fresh load) and the caller
// renders neutrally.
func (m *Model) listSelectionForLangFreshness() (owner, repo string, number int) {
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return "", "", 0
	}
	return it.pr.Owner, it.pr.Repo, it.pr.Number
}

// renderBuildLangAgentsHint is the lang-agents twin of
// renderBuildAgentsHint. Same colouring rules — red for missing, yellow
// for stale, plain otherwise — but driven by a per-PR aggregator instead
// of a per-repo one. The hint also stays neutral when we have no record
// of the PR's languages, which is the common case for un-visited list
// rows: showing a warning we can't ground would be more noisy than
// helpful.
func (m *Model) renderBuildLangAgentsHint(owner, repo string, number int) string {
	const label = "ctrl+l lang experts"
	state := m.langAgentsFreshness(owner, repo, number)
	switch state {
	case langagentsstore.FreshnessMissing:
		return styles.ErrStyle.Render(label + " (missing!)")
	case langagentsstore.FreshnessStale:
		return styles.WarnStyle.Render(label + " (stale)")
	default:
		return label
	}
}

// buildLangAgentsChip is the lang-agents twin of buildRepoAgentsChip.
// Pinned to the right side of the PR detail mini-header so the reviewer
// sees a "this PR has a language with no expert" warning the moment
// they open the PR, not just when they glance at the bottom status bar.
