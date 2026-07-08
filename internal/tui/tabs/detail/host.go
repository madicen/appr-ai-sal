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

// Host is implemented by the root model for cross-cutting services the detail
// tab delegates to (overlays, settings, review launch, shared PR state).
type Host interface {
	Width() int
	Height() int
	Keys() keys.Map
	Demo() bool
	DryRun() bool
	SetDryRun(bool)
	MouseYAdjust() int

	CurrentPR() *gh.PR
	ParsedDiff() []review.FileDiff
	Draft() *review.Draft
	SetDraft(*review.Draft)
	AIConfig() *aiconfig.Config
	SetStrictness(aiconfig.ReviewStrictness)

	BackToList()
	Relayout()
	ChromeBodyHeight() int
	HeaderHeight() int
	RenderHeader() string
	RenderStatus() string

	OpenSettings(settings.StartSection) tea.Cmd
	OpenRepoAgentsForCurrentPR(regen bool) tea.Cmd
	OpenLangAgents() tea.Cmd
	StartReviewOverlay() tea.Cmd
	ReopenApproval() tea.Cmd
	OpenBrowser() tea.Cmd
	CopyURL() tea.Cmd
	BulkConfirm() tea.Cmd

	ToggleParallelSpecialists() error
	ToggleParallelPRAgents() error

	RepoAgentsFreshness(owner, repo string) repoagentsstore.Freshness
	LangAgentsFreshness(owner, repo string, number int) langagentsstore.Freshness

	ForwardControlsProfileDropdown(tea.Msg) tea.Cmd
	ControlsProfileDropdownOpen() bool

	ParallelSpecialists() bool
	ParallelPRAgents() bool

	DebugMouse() bool
	ChecksRollupChip(state string) string

	ReviewStateBadge(gh.ReviewState) string
	ViewerActionBadge(gh.ReviewState) string
}
