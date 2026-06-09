package settings

import "strconv"

// bubblezone IDs for the settings pane (must match Mark calls in View).
const (
	ZoneStrictCriticalOnly = "zone:settings:strict:critical_only"
	ZoneStrictLenient      = "zone:settings:strict:lenient"
	ZoneStrictBalanced     = "zone:settings:strict:balanced"
	ZoneStrictStrict       = "zone:settings:strict:strict"
	ZoneSettingsSave       = "zone:settings:save"
	ZoneSettingsCancel     = "zone:settings:cancel"
	ZoneSettingsTabReview  = "zone:settings:tab:review-ai"
	ZoneSettingsTabRepoCtx = "zone:settings:tab:repo-context"

	ZoneRepoToggleIncludePR        = "zone:settings:repo:toggle:include_pr"
	ZoneRepoToggleCulture          = "zone:settings:repo:toggle:culture"
	ZoneRepoToggleCtxVs            = "zone:settings:repo:toggle:ctx_vs"
	ZoneRepoToggleExpert           = "zone:settings:repo:toggle:expert_panel"
	ZoneRepoToggleParallelSpecs    = "zone:settings:repo:toggle:parallel_specialists"
	ZoneRepoToggleParallelPRAgents = "zone:settings:repo:toggle:parallel_pr_agents"
	ZoneRepoToggleParallelExperts  = "zone:settings:repo:toggle:parallel_repo_experts"

	// AI profile editor zones. Profile rows get a per-index zone so click
	// selection works for any number of profiles; the action buttons sit
	// below the row list.
	ZoneProfileAdd       = "zone:settings:profile:add"
	ZoneProfileDelete    = "zone:settings:profile:delete"
	ZoneProfileSetActive = "zone:settings:profile:set-active"

	// AI profile text-field zones — clicking a field focuses it (the
	// click equivalent of tab/shift+tab cycling), so the form is fully
	// mouse-navigable. Typing still needs the keyboard.
	ZoneAIFieldName     = "zone:settings:ai:field:name"
	ZoneAIFieldProvider = "zone:settings:ai:field:provider"
	ZoneAIFieldBaseURL  = "zone:settings:ai:field:base-url"
	ZoneAIFieldModel    = "zone:settings:ai:field:model"
	ZoneAIFieldAPIKey   = "zone:settings:ai:field:api-key"
	ZoneAIFieldTimeout  = "zone:settings:ai:field:timeout"

	// Repo-context text-field zones — same click-to-focus treatment for
	// the repo-context tab's text/number inputs (the toggles already
	// have their own zones).
	ZoneRepoFieldRoots       = "zone:settings:repo:field:roots"
	ZoneRepoFieldMaxBytes    = "zone:settings:repo:field:max-bytes"
	ZoneRepoFieldTTL         = "zone:settings:repo:field:ttl"
	ZoneRepoFieldPRHistLimit = "zone:settings:repo:field:pr-hist-limit"
	ZoneRepoFieldExpertPRs   = "zone:settings:repo:field:expert-prs"
	ZoneRepoFieldExpertMaxB  = "zone:settings:repo:field:expert-max-bytes"
	ZoneRepoFieldExpertTTL   = "zone:settings:repo:field:expert-ttl"
)

// ZoneProfileRow returns the bubblezone ID for the profile row at idx.
func ZoneProfileRow(idx int) string {
	return "zone:settings:profile:row:" + strconv.Itoa(idx)
}
