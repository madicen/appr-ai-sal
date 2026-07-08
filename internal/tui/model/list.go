package model

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/madicen/appr-ai-sal/internal/gh"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	"github.com/madicen/appr-ai-sal/internal/tui/data"
	"github.com/madicen/appr-ai-sal/internal/tui/styles"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"
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
	if stats := diffStatsChip(i.pr); stats != "" {
		parts = append(parts, stats)
	}
	if checks := checksRollupChip(i.pr.ChecksState); checks != "" {
		parts = append(parts, checks)
	}
	if badge := reviewStateBadge(i.pr.ReviewState); badge != "" {
		parts = append(parts, badge)
	}
	if hint := viewerActionBadge(i.pr.ReviewState); hint != "" {
		parts = append(parts, hint)
	}
	return strings.Join(parts, " · ")
}

// diffStatsChip renders the queue's "+N/-M · K files" segment. Returns "" when
// every counter is zero — typically because the GraphQL endpoint declined to
// fill them in for some PRs (older self-hosted GitHub installs) — so the row
// stays neutral instead of advertising a phantom "+0/-0 · 0 files".
func diffStatsChip(pr gh.PR) string {
	if pr.Additions == 0 && pr.Deletions == 0 && pr.ChangedFiles == 0 {
		return ""
	}
	add := styles.OkStyle.Render(fmt.Sprintf("+%d", pr.Additions))
	del := styles.ErrStyle.Render(fmt.Sprintf("-%d", pr.Deletions))
	files := styles.DimStyle.Render(fmt.Sprintf("%d files", pr.ChangedFiles))
	if pr.ChangedFiles == 1 {
		files = styles.DimStyle.Render("1 file")
	}
	return add + "/" + del + " · " + files
}

// checksRollupChip renders the queue's CI summary chip. The four states map
// to the tokens GitHub's status-check rollup uses (post CollapseChecksRollup);
// "" / no rollup data renders nothing rather than misleading "passing".
func checksRollupChip(state string) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "SUCCESS":
		return styles.OkStyle.Render("checks pass")
	case "FAILURE":
		return styles.ErrStyle.Render("checks fail")
	case "ERROR":
		return styles.ErrStyle.Render("checks error")
	case "PENDING":
		return styles.WarnStyle.Render("checks pending")
	default:
		return ""
	}
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

	// Field-focused arms — keys that route directly into the inline
	// search / URL inputs are handled first so the bubbles list never
	// sees a slash a user typed into the URL bar.
	if m.listFocus == focusSearch || m.listFocus == focusURL {
		return m.handlePanelInputKey(msg)
	}

	// Every arm below matches against the central keymap (m.keys) rather
	// than a raw string case, so the status bar / help / palette labels
	// and the handler share one source of truth.
	km := m.keys
	switch {
	case key.Matches(msg, km.ListQuit):
		util.FlushMouse()
		return m, tea.Quit
	case key.Matches(msg, km.ListFilter):
		return m, m.cycleFilterCmd()
	case key.Matches(msg, km.ListSearch):
		return m, m.focusSearchInput()
	case key.Matches(msg, km.ListURL):
		return m, m.focusURLInput()
	case key.Matches(msg, km.ListCycleFocus):
		return m, m.cyclePanelFocusForward()
	case key.Matches(msg, km.ListCycleFocusB):
		return m, m.cyclePanelFocusBackward()
	case key.Matches(msg, km.ListRefresh):
		return m, m.refreshPRListCmd()
	case key.Matches(msg, km.ListQueue):
		return m, m.startQueueCmd()
	case key.Matches(msg, km.CopyURL):
		return m, m.copyListSelectionURLCmd()
	case key.Matches(msg, km.SettingsAI):
		return m, m.openSettings(settings.StartAI)
	case key.Matches(msg, km.SettingsReview):
		return m, m.openSettings(settings.StartReview)
	case key.Matches(msg, km.RepoCtx):
		return m, m.openSettings(settings.StartRepoContext)
	case key.Matches(msg, km.RepoAgents):
		// From the list view we can't pre-focus a single repo (the highlight
		// might not even point at a PR), so open the tab as-is.
		return m, m.openRepoAgents("", false)
	case key.Matches(msg, km.LangAgents):
		return m, m.openLangAgents()
	case key.Matches(msg, km.BuildAgents):
		// Build/refresh repo agents for the highlighted PR's repo, if any.
		if it, ok := m.list.SelectedItem().(prItem); ok {
			focus := it.pr.Owner + "/" + it.pr.Repo
			return m, m.openRepoAgents(focus, true)
		}
		return m, m.openRepoAgents("", false)
	case key.Matches(msg, km.Browser):
		if it, ok := m.list.SelectedItem().(prItem); ok {
			if u := strings.TrimSpace(it.pr.URL); u != "" {
				return m, util.OpenInBrowserCmd(u)
			}
		}
		return m, nil
	case key.Matches(msg, km.ListOpen):
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

// handlePanelInputKey routes a keystroke into the focused inline input
// (search or URL). esc blurs the input and returns focus to the list;
// enter on the URL field parses + loads the PR detail (the legacy
// modeURLInput submit flow); tab cycles focus. Any other key is
// forwarded to the textinput and (for the search field) re-derives the
// visible list items via applySearchFilter.
func (m *Model) handlePanelInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// If the search field has text, the first esc clears it and
		// keeps focus; a second esc (now-empty search) drops focus
		// back to the list. URL field always blurs on esc.
		if m.listFocus == focusSearch && strings.TrimSpace(m.searchInput.Value()) != "" {
			m.searchInput.SetValue("")
			m.searchQuery = ""
			m.applySearchFilter()
			return m, nil
		}
		m.blurPanelInputs()
		return m, nil
	case "tab":
		return m, m.cyclePanelFocusForward()
	case "shift+tab":
		return m, m.cyclePanelFocusBackward()
	case "enter":
		if m.listFocus == focusURL {
			return m.submitURLInput()
		}
		// Enter in the search field just drops focus back to the list
		// so the user can press it again to open the highlighted PR.
		m.blurPanelInputs()
		return m, nil
	}

	if m.listFocus == focusSearch {
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		m.searchQuery = m.searchInput.Value()
		m.applySearchFilter()
		return m, cmd
	}
	var cmd tea.Cmd
	m.urlInput, cmd = m.urlInput.Update(msg)
	return m, cmd
}

// submitURLInput is the URL-bar enter-press handler. Empty input is a
// no-op (so the user can dismiss the field without errors); a parse
// failure surfaces through the standard ErrMsg overlay; success blurs
// the field and kicks off the PR detail loader — same effect the old
// dedicated modeURLInput screen had.
func (m *Model) submitURLInput() (tea.Model, tea.Cmd) {
	v := strings.TrimSpace(m.urlInput.Value())
	if v == "" {
		return m, nil
	}
	ref, err := gh.ParsePRURL(v)
	if err != nil {
		return m, func() tea.Msg { return data.ErrMsg{Err: err} }
	}
	m.urlInput.Reset()
	m.blurPanelInputs()
	return m, data.LoadPRDetailCmd(ref, m.opts.Demo)
}

// focusSearchInput / focusURLInput move keyboard focus into the named
// inline input. Both blur the other field so the caret only blinks in
// the active widget.
func (m *Model) focusSearchInput() tea.Cmd {
	if m.listFocus == focusSearch {
		return nil
	}
	m.urlInput.Blur()
	m.listFocus = focusSearch
	return m.searchInput.Focus()
}

func (m *Model) focusURLInput() tea.Cmd {
	if m.listFocus == focusURL {
		return nil
	}
	m.searchInput.Blur()
	m.listFocus = focusURL
	return m.urlInput.Focus()
}

// blurPanelInputs returns focus to the bubbles list. Cursors stop
// blinking so the user can tell the inputs are idle.
func (m *Model) blurPanelInputs() {
	m.searchInput.Blur()
	m.urlInput.Blur()
	m.listFocus = focusList
}

// cyclePanelFocusForward / Backward cycles list → search → url → list.
func (m *Model) cyclePanelFocusForward() tea.Cmd {
	switch m.listFocus {
	case focusList:
		return m.focusSearchInput()
	case focusSearch:
		return m.focusURLInput()
	default:
		m.blurPanelInputs()
		return nil
	}
}

func (m *Model) cyclePanelFocusBackward() tea.Cmd {
	switch m.listFocus {
	case focusList:
		return m.focusURLInput()
	case focusURL:
		return m.focusSearchInput()
	default:
		m.blurPanelInputs()
		return nil
	}
}

// cycleFilterCmd advances the filter chip to the next mode and kicks
// off a fresh fetch. Shared by the `f` keybinding and the per-chip
// click handler so the resulting list state is identical regardless
// of how the change was triggered.
func (m *Model) cycleFilterCmd() tea.Cmd {
	m.filter = nextFilterMode(m.filter)
	m.updateListTitle()
	return m.refreshPRListCmd()
}

// setFilterCmd jumps directly to mode (used by chip clicks). A click
// on the active chip is a no-op rather than a wasted refresh.
func (m *Model) setFilterCmd(mode filterMode) tea.Cmd {
	if m.filter == mode {
		return nil
	}
	m.filter = mode
	m.updateListTitle()
	return m.refreshPRListCmd()
}

func (m *Model) updateListTitle() {
	var base string
	switch m.filter {
	case filterReviewExplicit:
		base = "PRs · you are explicitly requested"
	case filterAuthored:
		base = "PRs · authored by you"
	default:
		base = "PRs · review requested (@me, incl. teams)"
	}
	m.list.Title = base + m.queueTitle()
}

// applySearchFilter rebuilds the bubbles/list items from prsAll
// filtered by searchQuery. Matched fields are the same set the panel
// advertises (title, repo, author) plus the bare PR number so users
// can type "#123" to jump to a PR they remember by number.
func (m *Model) applySearchFilter() {
	q := strings.TrimSpace(strings.ToLower(m.searchQuery))
	out := make([]list.Item, 0, len(m.prsAll))
	for _, p := range m.prsAll {
		if q == "" || prMatchesQuery(p, q) {
			out = append(out, prItem{pr: p})
		}
	}
	m.list.SetItems(out)
}

// prMatchesQuery reports whether the substring q appears in any of the
// PR's display-relevant fields. Case is normalised by the caller.
func prMatchesQuery(p gh.PR, q string) bool {
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(p.Title), q) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Repository), q) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Author), q) {
		return true
	}
	if strings.Contains(fmt.Sprintf("#%d", p.Number), q) {
		return true
	}
	return false
}

// copyListSelectionURLCmd copies the highlighted PR's URL to the clipboard
// (Phase 5 item 9). No-op when nothing is highlighted or the PR has no URL.
func (m *Model) copyListSelectionURLCmd() tea.Cmd {
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return nil
	}
	if u := strings.TrimSpace(it.pr.URL); u != "" {
		return util.CopyPlainTextCmd(u)
	}
	return nil
}

// refreshPRListCmd flips the list back into its loading state and returns the
// fetch command. Centralised so the keyboard binding (R), the filter chips
// (f / chip click), and the refresh chip click all behave identically — in
// particular, all three flip prsLoaded=false so renderBody swaps the list out
// for the spinner and the refresh chip flips to "refreshing…".
func (m *Model) refreshPRListCmd() tea.Cmd {
	m.prsLoaded = false
	return data.LoadPRsCmd(m.listMode(), m.opts.Demo)
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
