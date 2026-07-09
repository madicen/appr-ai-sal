package detail

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
	"github.com/madicen/appr-ai-sal/internal/review"
	langagentsstore "github.com/madicen/appr-ai-sal/internal/review/langagents"
	repoagentsstore "github.com/madicen/appr-ai-sal/internal/review/repoagents"
	"github.com/madicen/appr-ai-sal/internal/tui/keys"
	"github.com/madicen/appr-ai-sal/internal/tui/tabs/settings"
)

// testHost is a minimal Host for unit tests in this package.
type testHost struct {
	width, height int
	pr            *gh.PR
	diff          []review.FileDiff
	draft         *review.Draft
	aiCfg         *aiconfig.Config
	dryRun        bool
}

func newTestHost(w, h int) *testHost {
	return &testHost{width: w, height: h, aiCfg: aiconfig.DefaultConfig()}
}

func (h *testHost) Width() int                    { return h.width }
func (h *testHost) Height() int                   { return h.height }
func (h *testHost) Keys() keys.Map                { return keys.Default() }
func (h *testHost) Demo() bool                    { return true }
func (h *testHost) DryRun() bool                  { return h.dryRun }
func (h *testHost) SetDryRun(v bool)              { h.dryRun = v }
func (h *testHost) MouseYAdjust() int             { return 0 }
func (h *testHost) DebugMouse() bool              { return false }
func (h *testHost) CurrentPR() *gh.PR             { return h.pr }
func (h *testHost) ParsedDiff() []review.FileDiff { return h.diff }
func (h *testHost) Draft() *review.Draft          { return h.draft }
func (h *testHost) SetDraft(d *review.Draft)      { h.draft = d }
func (h *testHost) AIConfig() *aiconfig.Config    { return h.aiCfg }
func (h *testHost) SetStrictness(l aiconfig.ReviewStrictness) {
	if h.aiCfg == nil {
		h.aiCfg = aiconfig.DefaultConfig()
	}
	h.aiCfg.ReviewStrictness = l
}
func (h *testHost) BackToList()                                {}
func (h *testHost) Relayout()                                  {}
func (h *testHost) ChromeBodyHeight() int                      { return max(1, h.height-4) }
func (h *testHost) HeaderHeight() int                          { return 1 }
func (h *testHost) RenderHeader() string                       { return "hdr" }
func (h *testHost) RenderStatus() string                       { return "status" }
func (h *testHost) OpenSettings(settings.StartSection) tea.Cmd { return nil }
func (h *testHost) OpenRepoAgentsForCurrentPR(bool) tea.Cmd    { return nil }
func (h *testHost) OpenLangAgents() tea.Cmd                    { return nil }
func (h *testHost) StartReviewOverlay() tea.Cmd                { return nil }
func (h *testHost) ReopenApproval() tea.Cmd                    { return nil }
func (h *testHost) OpenBrowser() tea.Cmd                       { return nil }
func (h *testHost) CopyURL() tea.Cmd                           { return nil }
func (h *testHost) BulkConfirm() tea.Cmd                       { return nil }
func (h *testHost) ToggleParallelSpecialists() error           { return nil }
func (h *testHost) ToggleParallelPRAgents() error              { return nil }
func (h *testHost) RepoAgentsFreshness(_, _ string) repoagentsstore.Freshness {
	return repoagentsstore.FreshnessUnknown
}
func (h *testHost) LangAgentsFreshness(_, _ string, _ int) langagentsstore.Freshness {
	return langagentsstore.FreshnessUnknown
}
func (h *testHost) ForwardControlsProfileDropdown(tea.Msg) tea.Cmd { return nil }
func (h *testHost) ControlsProfileDropdownOpen() bool              { return false }
func (h *testHost) ParallelSpecialists() bool                      { return false }
func (h *testHost) ParallelPRAgents() bool                         { return false }
func (h *testHost) ReviewStateBadge(gh.ReviewState) string         { return "" }
func (h *testHost) ViewerActionBadge(gh.ReviewState) string        { return "" }
func (h *testHost) ChecksRollupChip(string) string                 { return "" }
