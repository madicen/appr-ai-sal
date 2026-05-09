package repoagents

import "fmt"

// Stable zone IDs for the repo-agents tab. zoneAgentX(spec, kind) keeps the
// per-row chips unique without collisions across specialists.
const (
	ZoneClose        = "zone:repoagents:close"
	ZoneRegenAll     = "zone:repoagents:regen-all"
	ZonePrevRepo     = "zone:repoagents:repo:prev"
	ZoneNextRepo     = "zone:repoagents:repo:next"
	ZoneAddRepoOpen  = "zone:repoagents:repo:add"
	ZoneAddRepoSave  = "zone:repoagents:repo:add:save"
	ZoneAddRepoCancel = "zone:repoagents:repo:add:cancel"
	ZoneRemoveRepo   = "zone:repoagents:repo:remove"
	ZoneEditSave     = "zone:repoagents:edit:save"
	ZoneEditCancel   = "zone:repoagents:edit:cancel"
)

// zoneAgentRegen returns the click zone id for the per-specialist Regenerate chip.
func zoneAgentRegen(specialist string) string {
	return fmt.Sprintf("zone:repoagents:agent:%s:regen", specialist)
}

// zoneAgentEdit returns the click zone id for the per-specialist Edit chip.
func zoneAgentEdit(specialist string) string {
	return fmt.Sprintf("zone:repoagents:agent:%s:edit", specialist)
}

// zoneAgentDelete returns the click zone id for the per-specialist Delete chip.
func zoneAgentDelete(specialist string) string {
	return fmt.Sprintf("zone:repoagents:agent:%s:delete", specialist)
}

// zoneAgentRow returns the click zone id for the per-specialist row body
// (used to expand the preview).
func zoneAgentRow(specialist string) string {
	return fmt.Sprintf("zone:repoagents:agent:%s:row", specialist)
}
