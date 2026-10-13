# Summary section editor — design

Date: 2026-10-13
Status: approved (design); pending implementation plan

## Goal

Replace the read-only, single-blob markdown preview on the final **Review · post
summary** screen (`phaseSummary`) with a structured section editor. Reviewers
can include/exclude and edit individual summary sections (including each AI
prompt topic and each PR-wide note) before posting to GitHub.

The GitHub body remains markdown assembled from the same logical content as
today's `Draft.RenderBody()`, but composition is driven by explicit section
units plus session overrides.

## Constraints and decisions

- **Edit model:** include/exclude per unit, plus textarea edit of that unit's
  markdown (same family as the finding challenge input).
- **Granularity:** coarse blocks for Headline+TL;DR, Stats, Repo expert panel,
  Process details; **fine-grained** units for each AI prompt topic and each
  PR-wide note (including demoted-included notes).
- **Caution:** always included, not editable, not removable. Preserves the
  `produced by **appr-ai-sal**` marker used by
  `gh.DetectPriorAprrAISalActivity`.
- **Architecture:** structured body sections (Approach 1). Do **not** parse the
  rendered markdown string, and do **not** mutate draft findings/prompts just to
  reshape the posted body.
- **TUI layout:** single scroll of section cards (no split pane). Cards collapse
  to a short snippet; expand shows the full section markdown.
- **Input:** keyboard and mouse (bubblezone), consistent with the rest of the
  review TUI.
- **Overrides:** session-only on the review `Model`; not persisted across runs.
- **YAGNI:** no reordering sections; no "preview composed body" pane; no
  persistence of edits; no editing of Caution.

## Architecture

```
Draft.BodySections()  →  []BodySection
         │
         ├─ RenderBody()          // all included, no edits (compat)
         │
         └─ ComposeBody(sections, overrides)  // TUI post path
                    ▲
                    │
         Model.summaryOverrides  (include + edited markdown by ID)
```

### Body section model

New types next to the renderer (in `internal/review/render.go` or a sibling
file such as `internal/review/body_sections.go`):

```go
type BodySectionKind string

const (
    BodySectionHeadline        BodySectionKind = "headline"
    BodySectionDiffTruncation  BodySectionKind = "diff_truncation"
    BodySectionCaution         BodySectionKind = "caution"
    BodySectionStats           BodySectionKind = "stats"
    BodySectionPromptTopic     BodySectionKind = "prompt_topic"
    BodySectionPromptNote      BodySectionKind = "prompt_note" // arbiter/skip dropped-prompt disclosure
    BodySectionPRWideNote      BodySectionKind = "pr_wide_note"
    BodySectionDemotedNote     BodySectionKind = "demoted_note"
    BodySectionRepoExperts     BodySectionKind = "repo_experts"
    BodySectionProcess         BodySectionKind = "process"
)

type BodySection struct {
    ID       string
    Kind     BodySectionKind
    Title    string // TUI label
    Markdown string // fragment joined into the GitHub body
    Fixed    bool   // true only for Caution
}
```

Stable IDs:

- `headline`, `diff_truncation`, `caution`, `stats`, `prompt_note`,
  `repo_experts`, `process`
- `prompt:<index>` for each kept/synthesized author prompt topic (index in the
  post-filter + synthesized list order)
- `prwide:<specialist>:<index>` for each PR-wide note
- `demoted:<specialist>:<index>` for demoted-included notes

### Building sections

`Draft.BodySections()` produces the same content `RenderBody()` does today,
split into ordered units. Existing helpers stay the source of truth where
possible (`bodyHeadline`, vibe summary / reconciliation note,
`diffTruncationNote`, `bodyCaution`, `bodyStatsLine`, `filterAuthorPrompts` +
`synthesizeFallbackPrompts`, `repoExpertPanelSection`, process `<details>`
block).

Order matches today's `RenderBody`: Headline → Diff truncation (if any) →
Caution → Stats (+ request-changes-without-prompts warning folded into Stats
when present) → Prompt topics… → Prompt note (if any) → PR-wide notes… →
Demoted notes… → Repo experts → Process.

**Prompt topics:** each unit's `Markdown` is exactly one
`combineAuthorPrompts` segment (`## <title>\n\n<text>` or bare text).
`ComposeBody` rebuilds the outer `### 🤖 Prompt for your AI assistant`
heading, helper italics (including topic count from included survivors), and
a single ` ```text ` fence joined with `\n\n---\n\n`. If zero prompt topics
remain included, omit the heading/fence entirely.

**Prompt note:** the existing arbiter/skip “dropped paste-ready prompts”
disclosure (when present) is its own card — include/exclude + editable. It is
independent of topic includes so a reviewer can keep the disclosure even when
editing topics, or drop it.

**PR-wide / demoted notes:** each unit is one bullet's markdown (including the
leading `- **severity · specialist:** …` form). `ComposeBody` emits the section
heading + intro line only when at least one note of that group is included.

**Caution:** always emitted in its current position; `Fixed: true` forces
include and ignores exclude/edit overrides.

**Optional empty sections:** if Diff truncation / Stats / Repo experts /
Process / Prompt note would be empty for this draft, omit those units from
`BodySections()` entirely (same as today not rendering them).

### RenderBody compatibility

`RenderBody()` becomes:

1. `sections := d.BodySections()`
2. return `ComposeBody(sections, nil)` // all non-fixed included; no edits

Existing `render_test.go` / payload tests that assert on full body strings
should keep passing with minimal churn. New tests cover exclude/edit paths.

### ComposeBody

```go
type BodySectionOverride struct {
    Included bool
    Edited   string // empty means use section.Markdown
}

func ComposeBody(sections []BodySection, overrides map[string]BodySectionOverride) string
```

Rules:

- Missing override → included (unless we later add defaults; v1 default include).
- `Fixed` sections always included; ignore exclude; ignore `Edited`.
- Non-empty `Edited` replaces that unit's markdown fragment.
- Group wrappers (prompt fence, PR-wide heading) are recomputed from survivors.

## TUI design

Primary files: `internal/tui/tabs/review/summary.go`, model/actions key/mouse
handlers, zone helpers under `internal/tui/zones/`.

### Layout

Replace the single glamour-rendered `RenderBody()` block in
`renderSummaryBody` with a vertical list of **section cards** inside the
existing summary viewport.

Each card shows:

- Include checkbox (`[x]` / `[ ]`) or `[lock]` for Caution
- Title (`BodySection.Title`)
- Collapsed: ~2-line snippet of the effective markdown (edited if any)
- Expanded: full effective markdown as **raw** text (no glamour) so what you
  read matches what will be posted / edited

Header chrome above the cards (verdict banner, inline-comment tallies, dry-run
notes, post error block, footer action buttons) stays as today.

### Keyboard

| Key | Action |
|-----|--------|
| `↑` / `↓` | Move focus between section cards |
| `space` | Toggle include/exclude (no-op on Caution) |
| `enter` | Expand / collapse focused card |
| `e` | Open textarea editor for focused section (no-op on Caution) |
| `esc` (in editor) | Apply textarea contents to override, close editor, return to cards |
| `y` / `a` / `n` / `q` / `R` | Unchanged post / approve-only / skip / abort / refresh |

While the editor is open, keys route to the textarea (same pattern as
`handleChallengeKey`); post hotkeys are disabled until the editor closes. No
separate Save/Cancel buttons in v1 — `esc` commits the buffer into
`summaryOverrides` and closes (matching “keep edit”).

### Mouse (bubblezone)

Zone helpers in `internal/tui/zones/` (names):

- `SummarySectionHeader(id)` — focus + expand/collapse
- `SummarySectionCheck(id)` — include/exclude
- `SummarySectionEdit(id)` — open editor

| Target | Action |
|--------|--------|
| Card header / title row | Focus + toggle expand/collapse |
| Checkbox mark | Toggle include/exclude (no-op on Caution) |
| `Edit` affordance | Open textarea editor (omit zone on Caution) |
| Existing footer zones (`StagedSummaryYes` / `No` / `ApproveOnly` / `StagedQuit` / refresh) | Unchanged |
| Wheel over summary viewport | Scroll cards via existing wheel→viewport path |

Editor open: ignore card/footer click zones for post/toggle; only textarea (+
esc) is active.

Poll bubblezone after `Scan` in tests (existing gotcha:
`bubblezone-scan-get-async`).

### Session state on `Model`

```go
summarySections []review.BodySection // snapshot at enterSummary / rebuild
summaryFocus    int
summaryExpanded map[string]bool
summaryOverrides map[string]review.BodySectionOverride
summaryEditing  bool
summaryEditID   string
summaryInput    textarea.Model
```

Rebuild section list when entering summary and when draft reconciliation
changes (e.g. after refresh). Preserve overrides for IDs that still exist;
drop overrides for vanished IDs.

### Post path

`actPostSummary` (and any path that posts the summary body) uses
`ComposeBody(m.summarySections, m.summaryOverrides)` instead of
`m.draft.RenderBody()`.

Unchanged:

- `PostEvent` / `EffectiveReviewEventAndBody` self-author downgrade
- dry-run payload display
- approve-only empty body
- confirm-approve / no-findings flows that do not use the section editor

No-findings auto-approve body can keep using `RenderBody()` (single canned
body; no section editor).

## Error handling

- Editing a section to empty string: treat as empty fragment; if that empties a
  group wrapper, omit the wrapper (same as exclude-all for that group).
- Excluding everything except Caution: allow post; body is Caution (+ any fixed
  ordering neighbors that remain). Do not silently re-add removed content.
- Invalid focus index after rebuild: clamp to `0..len-1`.

## Testing

**`internal/review`**

- `BodySections` + `ComposeBody(nil)` equals current `RenderBody` golden/string
  expectations for representative drafts (approve / request_changes / with
  prompts / PR-wide / repo experts / process details).
- Exclude one prompt topic → fence contains only remaining topics; topic count
  copy updates.
- Exclude all prompt topics → no prompt heading/fence.
- Exclude one PR-wide note → bullet omitted; heading remains if others included.
- Edit override replaces fragment in composed body.
- Caution always present even if override tries to exclude/edit.

**`internal/tui/tabs/review`**

- Keyboard: focus move, space toggle, enter expand, `e` edit + esc keep.
- Mouse: checkbox zone toggles include; header zone expands; Edit zone opens
  editor (poll zones after Scan).
- Post uses composed body (dry-run assertion on payload string).

## Out of scope

- Reordering sections
- Persisting overrides across sessions
- Editing/removing Caution
- Split-pane live composed preview
- Changing inline comment posting (cards walk) — summary body only

## Files likely touched

- `internal/review/render.go` (+ possibly `body_sections.go`)
- `internal/review/render_test.go` (+ new compose tests)
- `internal/tui/tabs/review/summary.go`
- `internal/tui/tabs/review/model.go` / `actions.go` / mouse handlers
- `internal/tui/zones/` (new zone name helpers)
- Review summary / teatest coverage as needed
