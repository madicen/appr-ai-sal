# bubble-dropdown integration — design

Date: 2026-06-24
Status: approved (design); pending implementation plan

## Goal

Replace several "pick one of N options" affordances in the appr-ai-sal TUI with
the `madicen/bubble-dropdown` component (latest release **v0.0.4**), focusing on
the settings tab. The dropdown renders a `[ Label ▼ ]` trigger that opens a
scrollable selection panel as a `bubble-overlay` modal, with full keyboard and
mouse (bubblezone) support.

The integration follows the pattern already proven in the sibling project
`jj-tui`, which integrated the same component into an equivalently-structured
settings tab. jj-tui is the reference implementation for all mechanics below.

## Constraints and decisions

- **Component version:** `github.com/madicen/bubble-dropdown v0.0.4`.
- **Styling: neutral.** Dropdowns inherit the terminal's colors (plain rounded
  border, reverse-video highlight, bold focused arrow). We do **not** call
  `WithAccentColor`/`SetAccentColor`, and we do **not** add a new theme slot.
  (This differs from jj-tui, which themes the dropdown with `styles.ColorPrimary`.)
- **Zone manager:** use the global bubblezone manager via `zone.DefaultManager`,
  matching the existing `bubble-color-picker` integration in the Theme tab.
  (jj-tui passes a per-instance `*zone.Manager`; appr-ai-sal uses the global one.)
- **Overlay-in-viewport handling: Approach B** — keep scrolling, compute
  scroll-adjusted bounds. The affected tabs move from `viewport.Model` to a
  line-list + manual scroll-slice render so per-line indices are available for
  `SetBounds` (see "Viewport refactor").

## Targets

### a) Provider — Review & AI tab (`internal/tui/tabs/settings/`)
Replace the free-text `provider` `textinput` (placeholder
`claude | gemini | ollama | openai_compatible`) with a 4-option dropdown.

- Options (label == value here): `claude`, `gemini`, `ollama`, `openai_compatible`
  (the `aiconfig.Provider*` constants).
- Selecting an option sets the provider on the in-edit profile via the existing
  `commitEditorToSelectedProfile` / `ParseProvider` path. A dropdown removes the
  possibility of an invalid free-text provider.
- Removes: the `fieldProvider` text input, its `ZoneAIFieldProvider`
  click-to-focus handling, and provider's slot in the tab/focus cycle (replaced
  by the dropdown trigger).

### b) Review strictness — Review & AI tab
Replace the 4-row radio list with a single dropdown.

- Options: `critical-only`, `lenient`, `balanced`, `strict`.
- Wired through the existing `strictnessAt(i)` / `strictnessIndex(rs)` helpers;
  the chosen index maps to `aiconfig.ReviewStrictness`.
- Removes: `renderStrictnessRows`, the four `ZoneStrict*` zones, the `1`–`4`
  and `j/k` strictness key handling, and the `fieldStrictness` radio behaviour.

### c) AI profile picker — Review & AI tab (full dropdown-driven)
Replace the profile **row-list table** with a dropdown that selects which
profile is being edited/viewed.

- Options: the profile names; the active profile is marked in its label
  (e.g. `sonnet (active)`).
- Selecting an option switches the edited profile: commit current editor fields
  to the previously-selected profile, then load the newly-selected one
  (reuses `commitEditorToSelectedProfile` + `loadEditorFromSelectedProfile`).
- The dropdown's `WithOptions` is rebuilt and `SetSelectedIndex` re-synced
  whenever the profile list changes (add/delete/rename).
- **Actions stay as buttons** below the dropdown: `Set active`, `+ Add`,
  `Delete` (existing `ZoneProfile*` zones), because a closed trigger has no room
  for per-row actions. The inline "Edit profile" fields (name, base URL, model,
  API key, timeout) remain.
- Removes: the per-row `ZoneProfileRow(i)` table rendering and the
  `fieldProfilePicker` ↑/↓/enter/n/d list navigation (selection now via the
  dropdown; n/d/enter actions move onto the buttons).

### d) Active repository — repoagents tab (`internal/tui/tabs/repoagents/`)
Replace the `← prev / next →` repo cycler (`ZonePrevRepo` / `ZoneNextRepo`,
`left/h` / `right/l`) with a dropdown over the (dynamic) repo list.

- Options: the sorted repo list (`m.repos`); dynamic, so `WithOptions` is
  rebuilt and `SetSelectedIndex(m.repoIdx)` re-synced when the list changes.
- Selecting an option sets `m.repoIdx` and triggers the same reload the cycler
  did.

## Shared integration mechanic (ported from jj-tui)

A uniform, minimal surface wherever a dropdown lives:

- **Construction:** `bubbledropdown.New(WithOptions(labels), WithPlaceholder(...))`,
  then `SetZoneManager(zone.DefaultManager)`. No accent color.
- **Bounds (scroll-adjusted):** while building the tab body as a `[]string`
  line list, record each trigger's absolute `(lineIndex, col)`. After the
  per-tab scroll `start` is computed, call
  `dd.SetBounds(lineIndex-start, col, tw, th)` where `tw, th = dd.TriggerSize()`.
  Join the lines and slice the visible `[start : start+visibleHeight]` window.
- **Compositing:** in the tab's `View()`, after the base render:
  `if dd := activeDropdown(); dd != nil && dd.Open() { out = dd.ViewWithOverlay(out, w, h) }`
  (before the root `zone.Scan`).
- **Routing (in `Update`):**
  - Handle `bubbledropdown.ItemChosenMsg` / `ItemCanceledMsg` first; apply the
    chosen value to the draft/config.
  - While a dropdown is open, forward all key/mouse input to it and swallow
    stray `zone.MsgZoneInBounds` release events so background zones (tab strip,
    Save) don't fire.
  - On left mouse press inside a trigger zone, forward the press to the dropdown
    so it opens (bubblezone only emits on release).

## Viewport refactor (Approach B prerequisite)

The Review & AI tab and the repoagents tab currently scroll via
`viewport.Model` (`m.vp`), which hides the per-line indices that `SetBounds`
needs. Both move to the **line-list + manual scroll-slice** render that jj-tui
uses and that the appr-ai-sal Theme tab already uses for color-picker overlay
positioning:

- A per-tab integer `yOffset`; mouse wheel adjusts it (clamped to
  `[0, maxOffset]` where `maxOffset = max(0, totalLines - visibleHeight)`).
- Body assembled as `[]string`; dropdown bounds recorded during assembly;
  visible window sliced after `start` is known.

The Theme tab and the Repo-context tab are otherwise unchanged.

## Out of scope

- Repo-context ON/OFF toggles (kept as toggles).
- langagents language list and repoagents specialist/tech rows (dynamic data
  rows / dashboards, not fixed single-select pickers).
- Any theme/accent color work (dropdowns stay neutral).

## Testing

Per the existing table-test style in each package:

- Selecting a dropdown item updates the draft/config: provider value, strictness
  level, edited/active profile, active repo index.
- `SetBounds` math is correct under scroll (trigger on-screen row ==
  `lineIndex - start`).
- Left-press on a trigger zone opens the panel; an open panel captures input and
  swallows stray zone-release events.
- Neutral styling renders (no accent color applied).
- Provider/strictness persist correctly through `aiconfig.Save` and reload.

## Reference

- Component README/API: `madicen/bubble-dropdown` @ v0.0.4.
- jj-tui integration:
  - `internal/tui/tabs/settings/ai/model.go` (sub-model dropdown API:
    `ProviderDropdown`, `DropdownOpen`, `SetZoneManager`, `UpdateDropdown`).
  - `internal/tui/tabs/settings/model.go` (parent routing: `activeDropdown`,
    `activeDropdownOpen`, open-on-press, ViewWithOverlay compositing,
    `panelYOffset` wheel scroll).
  - `internal/tui/tabs/settings/view_helpers.go` (`recordDropdown`, scroll-slice
    + `SetBounds(lineIndex-start, col, …)`).
