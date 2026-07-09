package model

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	overlay "github.com/madicen/bubble-overlay"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/repoconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/overlays"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
	"github.com/madicen/appr-ai-sal/internal/tui/util"

	detailtab "github.com/madicen/appr-ai-sal/internal/tui/tabs/detail"
)

func (m *Model) detailTab() *detailtab.Model {
	m.ensureDetailTab()
	tab := m.tabs[modeDetail]
	if tab == nil {
		return nil
	}
	a, ok := tab.(*tabAdapter)
	if !ok {
		return nil
	}
	dm, ok := a.inner.(*detailtab.Model)
	if !ok {
		return nil
	}
	return dm
}

func (m *Model) ensureDetailTab() {
	if m.tabs == nil {
		m.tabs = map[mode]Tab{}
	}
	if m.tabs[modeDetail] == nil {
		tab := newTab(detailtab.New(m, m.keys))
		m.tabs[modeDetail] = tab
		if m.width > 0 && m.height > 0 {
			tab.Resize(m.width, m.chromeBodyHeight())
			tab.SetContentOrigin(m.headerHeight())
		}
	}
}

// --- detail.Host ---

func (m *Model) Width() int        { return m.width }
func (m *Model) Height() int       { return m.height }
func (m *Model) Keys() keys.Map    { return m.keys }
func (m *Model) Demo() bool        { return m.opts.Demo }
func (m *Model) DryRun() bool      { return m.opts.DryRun }
func (m *Model) SetDryRun(v bool)  { m.opts.DryRun = v }
func (m *Model) MouseYAdjust() int { return m.opts.MouseYAdjust }
func (m *Model) DebugMouse() bool  { return m.opts.DebugMouse }

func (m *Model) CurrentPR() *gh.PR             { return m.currentPR }
func (m *Model) ParsedDiff() []review.FileDiff { return m.parsedDiff }
func (m *Model) Draft() *review.Draft          { return m.draft }
func (m *Model) SetDraft(d *review.Draft)      { m.draft = d }
func (m *Model) AIConfig() *aiconfig.Config    { return m.opts.AIConfig }

func (m *Model) SetStrictness(level aiconfig.ReviewStrictness) {
	if m.opts.AIConfig == nil {
		m.opts.AIConfig = aiconfig.DefaultConfig()
	}
	m.opts.AIConfig.ReviewStrictness = level
}

func (m *Model) BackToList() {
	m.dismissAllOverlaysSilent()
	m.blurPanelInputs()
	m.mode = modeList
	delete(m.tabs, modeList) // root-native; must never hold a tab adapter
	m.relayout()
}

func (m *Model) Relayout()             { m.relayout() }
func (m *Model) ChromeBodyHeight() int { return m.chromeBodyHeight() }
func (m *Model) HeaderHeight() int     { return m.headerHeight() }
func (m *Model) RenderHeader() string  { return m.renderHeader() }
func (m *Model) RenderStatus() string  { return m.renderStatus() }

func (m *Model) OpenSettings(section settings.StartSection) tea.Cmd {
	return m.openSettings(section)
}
func (m *Model) OpenRepoAgentsForCurrentPR(regen bool) tea.Cmd {
	return m.openRepoAgentsForCurrentPR(regen)
}
func (m *Model) OpenLangAgents() tea.Cmd     { return m.openLangAgents() }
func (m *Model) StartReviewOverlay() tea.Cmd { return m.startReviewOverlay() }
func (m *Model) ReopenApproval() tea.Cmd     { return m.reopenApproval() }

func (m *Model) OpenBrowser() tea.Cmd {
	if m.currentPR != nil {
		if u := strings.TrimSpace(m.currentPR.URL); u != "" {
			return util.OpenInBrowserCmd(u)
		}
	}
	return nil
}

func (m *Model) CopyURL() tea.Cmd {
	if m.currentPR != nil {
		if u := strings.TrimSpace(m.currentPR.URL); u != "" {
			return util.CopyPlainTextCmd(u)
		}
	}
	return nil
}

func (m *Model) BulkConfirm() tea.Cmd {
	if m.draft == nil {
		return nil
	}
	modal := overlays.NewBulkConfirmOverlay(m.draft.Ref.String())
	cfg := overlay.DefaultOverlayConfig()
	return tea.Batch(
		m.overlayStack.Push(modal, cfg),
		func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} },
	)
}

func (m *Model) ToggleParallelSpecialists() error { return m.toggleParallelSpecialists() }
func (m *Model) ToggleParallelPRAgents() error    { return m.toggleParallelPRAgents() }

func (m *Model) RepoAgentsFreshness(owner, repo string) repoagentsstore.Freshness {
	return m.repoAgentsFreshness(owner, repo)
}
func (m *Model) LangAgentsFreshness(owner, repo string, number int) langagentsstore.Freshness {
	return m.langAgentsFreshness(owner, repo, number)
}

func (m *Model) ParallelSpecialists() bool {
	p, _, _ := repoParallelExecutionFlags()
	return p
}
func (m *Model) ParallelPRAgents() bool {
	_, _, p := repoParallelExecutionFlags()
	return p
}

func (m *Model) ForwardControlsProfileDropdown(msg tea.Msg) tea.Cmd {
	if dt := m.detailTab(); dt != nil {
		return dt.ForwardControlsProfileDropdown(msg)
	}
	return nil
}

func (m *Model) ControlsProfileDropdownOpen() bool {
	if dt := m.detailTab(); dt != nil {
		return dt.ControlsProfileDropdownOpen()
	}
	return false
}

func (m *Model) detailBulkConfirmCmd() tea.Cmd { return m.BulkConfirm() }
func (m *Model) detailOpenBrowserCmd() tea.Cmd { return m.OpenBrowser() }

func (m *Model) beginDiffSearch() tea.Cmd {
	if dt := m.detailTab(); dt != nil {
		return dt.BeginDiffSearch()
	}
	return nil
}

func (m *Model) jumpDiffForward() {
	if dt := m.detailTab(); dt != nil {
		dt.JumpDiffForward()
	}
}

func (m *Model) jumpDiffBackward() {
	if dt := m.detailTab(); dt != nil {
		dt.JumpDiffBackward()
	}
}

func (m *Model) toggleThreads() tea.Cmd {
	if dt := m.detailTab(); dt != nil {
		return dt.ToggleThreads()
	}
	return nil
}

func (m *Model) openReviewHistory() tea.Cmd {
	if dt := m.detailTab(); dt != nil {
		return dt.OpenReviewHistory()
	}
	return nil
}

func (m *Model) ReviewStateBadge(rs gh.ReviewState) string  { return reviewStateBadge(rs) }
func (m *Model) ViewerActionBadge(rs gh.ReviewState) string { return viewerActionBadge(rs) }
func (m *Model) ChecksRollupChip(state string) string       { return checksRollupChip(state) }

func (m *Model) JumpToFinding(path string, line int) bool {
	if dt := m.detailTab(); dt != nil {
		return dt.JumpToFinding(path, line)
	}
	return false
}

func (m *Model) refreshDetailViews() {
	if dt := m.detailTab(); dt != nil {
		dt.RefreshViews()
	}
}

func (m *Model) ensureCenterDataLoaded() tea.Cmd {
	if dt := m.detailTab(); dt != nil {
		return dt.EnsureCenterDataLoaded()
	}
	return nil
}

func (m *Model) recomputeTreeRows() {
	if dt := m.detailTab(); dt != nil {
		dt.OnDraftUpdated(m.parsedDiff, m.draft)
	}
}

func (m *Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.ensureDetailTab()
	if tab := m.tabs[modeDetail]; tab != nil {
		updated, cmd := tab.Update(msg)
		m.tabs[modeDetail] = updated
		return m, cmd
	}
	return m, nil
}

func (m *Model) detailHandleMouse(msg tea.MouseMsg, wheel bool) (tea.Model, tea.Cmd) {
	m.ensureDetailTab()
	if tab := m.tabs[modeDetail]; tab != nil {
		updated, cmd := tab.Update(msg)
		m.tabs[modeDetail] = updated
		return m, cmd
	}
	return m, nil
}

func (m *Model) detailBackToList() {
	if dt := m.detailTab(); dt != nil {
		dt.BackToList()
	}
}

func (m *Model) detailToggleDescription() tea.Cmd {
	m.ensureDetailTab()
	if dt := m.detailTab(); dt != nil {
		return dt.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	}
	return nil
}

func (m *Model) detailToggleDiffOnly() {
	m.ensureDetailTab()
	if dt := m.detailTab(); dt != nil {
		_ = dt.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	}
}

func (m *Model) detailToggleControls() {
	m.ensureDetailTab()
	if dt := m.detailTab(); dt != nil {
		_ = dt.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	}
}

func (m *Model) toggleParallelSpecialists() error {
	cfg, err := repoconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = repoconfig.Default()
	}
	cfg.ParallelSpecialists = !cfg.ParallelSpecialists
	if err := repoconfig.Save(cfg, ""); err != nil {
		return err
	}
	m.refreshDetailViews()
	return nil
}

func (m *Model) toggleParallelPRAgents() error {
	cfg, err := repoconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = repoconfig.Default()
	}
	cfg.ParallelPRAgents = !cfg.ParallelPRAgents
	if err := repoconfig.Save(cfg, ""); err != nil {
		return err
	}
	m.refreshDetailViews()
	return nil
}
