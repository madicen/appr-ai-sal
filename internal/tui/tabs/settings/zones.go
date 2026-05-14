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

	ZoneRepoToggleIncludePR = "zone:settings:repo:toggle:include_pr"
	ZoneRepoToggleCulture   = "zone:settings:repo:toggle:culture"
	ZoneRepoToggleCtxVs     = "zone:settings:repo:toggle:ctx_vs"
	ZoneRepoToggleExpert          = "zone:settings:repo:toggle:expert_panel"
	ZoneRepoToggleParallelSpecs   = "zone:settings:repo:toggle:parallel_specialists"
	ZoneRepoToggleParallelExperts = "zone:settings:repo:toggle:parallel_repo_experts"

	// AI profile editor zones. Profile rows get a per-index zone so click
	// selection works for any number of profiles; the action buttons sit
	// below the row list.
	ZoneProfileAdd       = "zone:settings:profile:add"
	ZoneProfileDelete    = "zone:settings:profile:delete"
	ZoneProfileSetActive = "zone:settings:profile:set-active"
)

// ZoneProfileRow returns the bubblezone ID for the profile row at idx.
func ZoneProfileRow(idx int) string {
	return "zone:settings:profile:row:" + strconv.Itoa(idx)
}
