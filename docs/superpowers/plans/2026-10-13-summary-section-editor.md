# Summary Section Editor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let reviewers include/exclude and edit individual summary body sections (including each AI prompt topic and PR-wide note) on the final post-summary screen before posting to GitHub.

**Architecture:** Split `Draft.RenderBody()` into ordered `BodySection` units via `BodySections()` + `ComposeBody(sections, overrides)`. The TUI `phaseSummary` shows a scroll of section cards (expand, toggle, edit) with keyboard + bubblezone mouse; post sets a session body override then uses the existing verdict post command.

**Tech Stack:** Go, Bubble Tea, bubbles/textarea, bubblezone, existing `internal/review` + `internal/tui/tabs/review`.

**Spec:** [`docs/superpowers/specs/2026-10-13-summary-section-editor-design.md`](../specs/2026-10-13-summary-section-editor-design.md)

## Global Constraints

- Caution is always included; not editable; not removable (`Fixed: true`).
- Fine-grained units: each prompt topic and each PR-wide / demoted note.
- Session-only overrides on review `Model`; do not persist; do not mutate draft findings to reshape the body.
- No section reordering; no split-pane composed preview.
- Expanded card body is raw markdown (no glamour).
- `enter` expands/collapses a card; **only `y` posts** (behavior change: today `enter` also posts).
- Verification: `go build ./...`, `go vet ./...`, `go test ./...` (naitv `build` / `vet` / `test` with `project_root`).

---

## File structure

| File | Responsibility |
|------|----------------|
| Create: `internal/review/body_sections.go` | `BodySection`, kinds, `BodySectionOverride`, `BodySections()`, `ComposeBody()` |
| Modify: `internal/review/render.go` | `RenderBody()` becomes `ComposeBody(BodySections(), nil)` (+ keep HasNoFindings special case) |
| Create: `internal/review/body_sections_test.go` | Round-trip, exclude, edit, Caution fixed |
| Modify: `internal/review/draft.go` | Optional `PostedBodyOverride string` for TUI post |
| Modify: `internal/tui/zones/zones.go` | `SummarySectionHeader` / `Check` / `Edit` helpers |
| Modify: `internal/tui/tabs/review/model.go` | Session state fields; key/mouse routing |
| Modify: `internal/tui/tabs/review/summary.go` | Card render + help line; drop single glamour blob |
| Create: `internal/tui/tabs/review/summary_sections.go` | Sync/override helpers, edit open/close, effective markdown |
| Modify: `internal/tui/tabs/review/actions.go` | `enterSummary` sync; `actPostSummary` set override |
| Create: `internal/tui/tabs/review/summary_sections_test.go` | Keyboard/mouse/compose post tests |

---

### Task 1: BodySection types + ComposeBody round-trip

**Files:**
- Create: `internal/review/body_sections.go`
- Create: `internal/review/body_sections_test.go`
- Modify: `internal/review/render.go` (`RenderBody` only at end of this task — after BodySections works)

**Interfaces:**
- Produces:
  - `type BodySectionKind string` + constants from the spec
  - `type BodySection struct { ID, Kind, Title, Markdown string; Fixed bool }`
  - `type BodySectionOverride struct { Included bool; Edited string }`
  - `func (d *Draft) BodySections() []BodySection`
  - `func ComposeBody(sections []BodySection, overrides map[string]BodySectionOverride) string`

- [ ] **Step 1: Write the failing round-trip test**

```go
package review

import (
	"errors"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/gh"
)

func TestComposeBodyNilOverridesMatchesLegacyRenderBody(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{
			{Specialist: "design", Findings: []Finding{
				{Path: "a.go", Line: 1, Comment: "nit", Severity: SeverityInfo},
			}},
			{Specialist: "security", Err: errors.New("boom"), Outcome: OutcomeFailed},
			{Specialist: "docs", Findings: []Finding{
				{Path: "", Line: 0, Comment: "readme gap", Severity: SeverityWarning},
			}},
		},
		VibeCoach: &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "Mostly fine."},
	}
	got := ComposeBody(d.BodySections(), nil)
	if strings.Count(got, "> [!CAUTION]") != 1 {
		t.Fatalf("want exactly one CAUTION:\n%s", got)
	}
	if !strings.HasPrefix(got, "## appr-ai-sal summary — 💬 Comment only") {
		t.Fatalf("headline:\n%s", got)
	}
	if !strings.Contains(got, "**1 inline comment** on the diff · **1 PR-wide note** below") {
		t.Fatalf("stats:\n%s", got)
	}
	if !strings.Contains(got, "### PR-wide notes") || !strings.Contains(got, "readme gap") {
		t.Fatalf("PR-wide note missing:\n%s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/review -run TestComposeBodyNilOverridesMatchesLegacyRenderBody -count=1`

Expected: FAIL (`BodySections` / `ComposeBody` undefined)

- [ ] **Step 3: Implement types + BodySections + ComposeBody**

In `internal/review/body_sections.go`:

1. Define kinds, `BodySection`, `BodySectionOverride`.
2. Implement `BodySections()` by extracting the gathering loops from current `RenderBody` (PR-wide list, demoted, failures, vibe coach) and appending sections in spec order. Reuse helpers: `bodyHeadline`, reconciliation note + summary → headline markdown; `diffTruncationNote`; `bodyCaution`; `bodyStatsLine` + request-changes warning → stats; prompt topics via `filterAuthorPrompts` + `synthesizeFallbackPrompts` with each topic’s `Markdown` = one `combineAuthorPrompts` segment; prompt disclosure → `prompt_note`; each PR-wide/demoted bullet; `repoExpertPanelSection`; process `<details>` block.
3. Implement `ComposeBody`:
   - Default include when override missing.
   - Skip non-fixed when `Included == false`.
   - Always include `Fixed` (Caution); ignore Edited.
   - Use `Edited` when non-empty for non-fixed.
   - For prompt topics: collect included topic markdowns, if any wrap with heading + italics + fence joined by `\n\n---\n\n` (mirror `vibeCoachSuggestedPromptsSection` copy, topic count from survivors).
   - For `pr_wide_note` / `demoted_note`: emit group heading + intro once before first included bullet of that kind.
   - Concatenate other kinds’ markdown in order as-is.

Keep `HasNoFindings` path out of `BodySections` — `RenderBody` still special-cases it.

- [ ] **Step 4: Point RenderBody at ComposeBody**

```go
func (d *Draft) RenderBody() string {
	if d != nil && strings.TrimSpace(d.PostedBodyOverride) != "" {
		return d.PostedBodyOverride
	}
	if d != nil && d.HasNoFindings() {
		return bodyHeadline(VibeVerdictApprove) +
			"**No issues found by any agent** — every configured specialist reviewed this diff and produced no actionable feedback to leave on the diff or in a written summary. It recommends Approving this pull request.\n\n" +
			d.diffTruncationNote() +
			bodyCaution
	}
	return ComposeBody(d.BodySections(), nil)
}
```

Add `PostedBodyOverride string` on `Draft` in `draft.go` (empty by default).

Delete the old inline assembly from `RenderBody` once `BodySections` covers it (keep tiny helpers).

- [ ] **Step 5: Run review package tests**

Run: `go test ./internal/review -count=1`

Expected: PASS (including existing `render_test.go`)

- [ ] **Step 6: Commit**

```bash
git add internal/review/body_sections.go internal/review/body_sections_test.go internal/review/render.go internal/review/draft.go
git commit -m "$(cat <<'EOF'
refactor(review): split summary body into composable sections

EOF
)"
```

---

### Task 2: Exclude / edit ComposeBody behavior

**Files:**
- Modify: `internal/review/body_sections_test.go`
- Modify: `internal/review/body_sections.go` (only if Task 1 gaps)

**Interfaces:**
- Consumes: `BodySections`, `ComposeBody`, `BodySectionOverride`

- [ ] **Step 1: Write failing exclude/edit tests**

```go
func TestComposeBodyExcludesPromptTopic(t *testing.T) {
	d := &Draft{
		PR: &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{
			Specialist: "docs",
			Findings:   []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityError}},
		}},
		VibeCoach: &VibeCoachResult{
			Verdict: VibeVerdictRequestChanges,
			Summary: "Needs work.",
			Prompts: []AuthorPrompt{
				{Title: "One", AgentPrompt: "do one"},
				{Title: "Two", AgentPrompt: "do two"},
			},
		},
	}
	sections := d.BodySections()
	var promptIDs []string
	for _, s := range sections {
		if s.Kind == BodySectionPromptTopic {
			promptIDs = append(promptIDs, s.ID)
		}
	}
	if len(promptIDs) < 2 {
		t.Fatalf("need 2 prompt topics, got %v", promptIDs)
	}
	ov := map[string]BodySectionOverride{
		promptIDs[0]: {Included: false},
	}
	body := ComposeBody(sections, ov)
	if strings.Contains(body, "do one") {
		t.Fatalf("excluded topic still present:\n%s", body)
	}
	if !strings.Contains(body, "do two") {
		t.Fatalf("kept topic missing:\n%s", body)
	}
	if !strings.Contains(body, "### 🤖 Prompt for your AI assistant") {
		t.Fatalf("prompt wrapper missing:\n%s", body)
	}
}

func TestComposeBodyExcludesAllPromptTopicsOmitsWrapper(t *testing.T) {
	// same draft; exclude every prompt:* ID; assert no Prompt heading/fence
}

func TestComposeBodyExcludesPRWideNote(t *testing.T) {
	// draft with 2 PR-wide notes; exclude one; heading remains; bullet gone
}

func TestComposeBodyEditOverride(t *testing.T) {
	d := &Draft{
		PR:          &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{Specialist: "docs", Findings: []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo}}}},
		VibeCoach:   &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "Original summary."},
	}
	sections := d.BodySections()
	ov := map[string]BodySectionOverride{
		"headline": {Included: true, Edited: "## appr-ai-sal summary — 💬 Comment only\n\nEdited TL;DR.\n\n"},
	}
	body := ComposeBody(sections, ov)
	if !strings.Contains(body, "Edited TL;DR.") || strings.Contains(body, "Original summary.") {
		t.Fatalf("edit not applied:\n%s", body)
	}
}

func TestComposeBodyCautionAlwaysIncluded(t *testing.T) {
	d := &Draft{
		PR:          &gh.PR{HeadSHA: "abc"},
		Specialists: []SpecialistResult{{Specialist: "docs", Findings: []Finding{{Path: "a.go", Line: 1, Comment: "x", Severity: SeverityInfo}}}},
		VibeCoach:   &VibeCoachResult{Verdict: VibeVerdictComment, Summary: "S"},
	}
	ov := map[string]BodySectionOverride{
		"caution": {Included: false, Edited: "nope"},
	}
	body := ComposeBody(d.BodySections(), ov)
	if !strings.Contains(body, "> [!CAUTION]") || !strings.Contains(body, "produced by **appr-ai-sal**") {
		t.Fatalf("caution must remain:\n%s", body)
	}
	if strings.Contains(body, "nope") {
		t.Fatalf("caution edit must be ignored:\n%s", body)
	}
}
```

Fill in the two abbreviated tests with the same style (assert on substrings).

- [ ] **Step 2: Run tests — expect FAIL if ComposeBody incomplete, else PASS**

Run: `go test ./internal/review -run 'TestComposeBody' -count=1`

- [ ] **Step 3: Fix ComposeBody until all pass**

- [ ] **Step 4: Commit**

```bash
git add internal/review/body_sections.go internal/review/body_sections_test.go
git commit -m "$(cat <<'EOF'
test(review): cover summary section exclude and edit composition

EOF
)"
```

---

### Task 3: Zone ID helpers

**Files:**
- Modify: `internal/tui/zones/zones.go`
- Create: `internal/tui/zones/summary_sections_test.go` (tiny)

**Interfaces:**
- Produces:
  - `func SummarySectionHeader(id string) string`
  - `func SummarySectionCheck(id string) string`
  - `func SummarySectionEdit(id string) string`

- [ ] **Step 1: Write failing test**

```go
func TestSummarySectionZoneNames(t *testing.T) {
	if SummarySectionHeader("prompt:0") != "zone:staged:summary:section:prompt:0:header" {
		t.Fatal(SummarySectionHeader("prompt:0"))
	}
	if SummarySectionCheck("headline") != "zone:staged:summary:section:headline:check" {
		t.Fatal(SummarySectionCheck("headline"))
	}
	if SummarySectionEdit("stats") != "zone:staged:summary:section:stats:edit" {
		t.Fatal(SummarySectionEdit("stats"))
	}
}
```

- [ ] **Step 2: Implement helpers** next to other staged constants in `zones.go`

```go
func SummarySectionHeader(id string) string {
	return "zone:staged:summary:section:" + id + ":header"
}
func SummarySectionCheck(id string) string {
	return "zone:staged:summary:section:" + id + ":check"
}
func SummarySectionEdit(id string) string {
	return "zone:staged:summary:section:" + id + ":edit"
}
```

- [ ] **Step 3: `go test ./internal/tui/zones -count=1`** — PASS

- [ ] **Step 4: Commit**

```bash
git add internal/tui/zones/zones.go internal/tui/zones/summary_sections_test.go
git commit -m "$(cat <<'EOF'
feat(tui): add bubblezone IDs for summary section cards

EOF
)"
```

---

### Task 4: Model session state + sync on enterSummary

**Files:**
- Modify: `internal/tui/tabs/review/model.go` (struct fields)
- Create: `internal/tui/tabs/review/summary_sections.go`
- Modify: `internal/tui/tabs/review/actions.go` (`enterSummary`)
- Create: `internal/tui/tabs/review/summary_sections_test.go`

**Interfaces:**
- Produces on `Model`:
  - `summarySections []review.BodySection`
  - `summaryFocus int`
  - `summaryExpanded map[string]bool`
  - `summaryOverrides map[string]review.BodySectionOverride`
  - `summaryEditing bool`
  - `summaryEditID string`
  - `summaryInput textarea.Model`
- Produces: `func (m *Model) syncSummarySections()`
- Produces: `func (m *Model) effectiveSectionMarkdown(s review.BodySection) string`
- Produces: `func (m *Model) composedSummaryBody() string`

- [ ] **Step 1: Write failing sync test**

```go
func TestSyncSummarySectionsPreservesOverridesForAliveIDs(t *testing.T) {
	ro := newTestReviewOverlay(t) // use existing test helper if present; else construct Model+draft
	// Set draft with vibe prompts; call syncSummarySections
	// Set override excluding prompt:0; change draft prompts; sync again
	// Assert override for surviving IDs kept; vanished IDs dropped
}
```

Use the package’s existing test constructors (`newTestReviewOverlay` / similar from `summary_test.go` / `vibe_defer_test.go`). If none fit, build a minimal `Model{draft: ...}` and call `syncSummarySections` directly.

- [ ] **Step 2: Implement `summary_sections.go`**

```go
func (m *Model) syncSummarySections() {
	if m.draft == nil {
		m.summarySections = nil
		return
	}
	next := m.draft.BodySections()
	alive := map[string]struct{}{}
	for _, s := range next {
		alive[s.ID] = struct{}{}
	}
	if m.summaryOverrides != nil {
		for id := range m.summaryOverrides {
			if _, ok := alive[id]; !ok {
				delete(m.summaryOverrides, id)
			}
		}
	} else {
		m.summaryOverrides = map[string]review.BodySectionOverride{}
	}
	if m.summaryExpanded == nil {
		m.summaryExpanded = map[string]bool{}
	}
	m.summarySections = next
	if m.summaryFocus >= len(m.summarySections) {
		m.summaryFocus = max(0, len(m.summarySections)-1)
	}
}

func (m *Model) effectiveSectionMarkdown(s review.BodySection) string {
	if s.Fixed {
		return s.Markdown
	}
	if ov, ok := m.summaryOverrides[s.ID]; ok && strings.TrimSpace(ov.Edited) != "" {
		return ov.Edited
	}
	return s.Markdown
}

func (m *Model) sectionIncluded(s review.BodySection) bool {
	if s.Fixed {
		return true
	}
	if ov, ok := m.summaryOverrides[s.ID]; ok {
		return ov.Included
	}
	return true
}

func (m *Model) composedSummaryBody() string {
	return review.ComposeBody(m.summarySections, m.summaryOverrides)
}
```

Default override semantics: missing key ⇒ included. When toggling exclude, write `BodySectionOverride{Included: false}` (preserve `Edited` if set).

- [ ] **Step 3: Call `syncSummarySections()` from `enterSummary`** (and any path that lands in `phaseSummary` with a refreshed draft — same places that already rebuild summary UI).

- [ ] **Step 4: Run** `go test ./internal/tui/tabs/review -run TestSyncSummarySections -count=1` — PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/tabs/review/model.go internal/tui/tabs/review/summary_sections.go internal/tui/tabs/review/actions.go internal/tui/tabs/review/summary_sections_test.go
git commit -m "$(cat <<'EOF'
feat(tui): track summary section overrides on the review model

EOF
)"
```

---

### Task 5: Render section cards (replace glamour blob)

**Files:**
- Modify: `internal/tui/tabs/review/summary.go` (`renderSummaryBody`, help line)
- Modify: `internal/tui/tabs/review/summary_sections.go` (render helpers)

**Interfaces:**
- Consumes: `summarySections`, focus/expanded/include, zone helpers
- Produces: card list inside summary viewport

- [ ] **Step 1: Write a render smoke test**

```go
func TestRenderSummaryBodyShowsSectionCards(t *testing.T) {
	// Build model in phaseSummary with draft that has PR-wide + prompts
	// out := m.renderSummaryBody()
	// assert contains "Headline" or section title markers, "[x]", "Caution",
	// and does NOT require the old "Markdown body GitHub will publish" sole blob
	// (that label may be removed or replaced with "Summary sections")
}
```

- [ ] **Step 2: Implement card rendering**

For each section in `m.summarySections`:

- Checkbox / lock via `zone.Mark(zones.SummarySectionCheck(id), …)` (lock text for Fixed; no toggle affordance).
- Title row via `zone.Mark(zones.SummarySectionHeader(id), …)` with `▸`/`▾` for collapsed/expanded and focus marker.
- `Edit` via `zone.Mark(zones.SummarySectionEdit(id), …)` omitted when `Fixed`.
- Body: if expanded, full `effectiveSectionMarkdown`; else first ~2 lines truncated.
- Dim excluded cards (`!sectionIncluded`).

Remove the `util.RenderMarkdownIndented(body, …)` single-blob path from `renderSummaryBody`. Keep verdict banner, tallies, errors, footer buttons.

Update help in `summaryHelp` / phaseSummary help string to:

`↑/↓ focus · space include · enter expand · e edit · y post · a approve · R refresh · n skip · q abort · wheel`

- [ ] **Step 3: Run** `go test ./internal/tui/tabs/review -run TestRenderSummaryBody -count=1` and fix goldens if `golden_test` / summary snapshots break.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/tabs/review/summary.go internal/tui/tabs/review/summary_sections.go internal/tui/tabs/review/summary_sections_test.go
git commit -m "$(cat <<'EOF'
feat(tui): render post-summary as expandable section cards

EOF
)"
```

---

### Task 6: Keyboard — focus, toggle, expand, edit

**Files:**
- Modify: `internal/tui/tabs/review/model.go` (`phaseSummary` key switch)
- Modify: `internal/tui/tabs/review/summary_sections.go` (handlers)
- Modify: `internal/tui/tabs/review/summary_sections_test.go`

**Critical behavior change:** In `phaseSummary`, stop treating `enter` as post. Only `y`/`Y` call `actPostSummary`.

- [ ] **Step 1: Write failing key tests**

```go
func TestSummarySectionKeysToggleExpandEdit(t *testing.T) {
	// land in phaseSummary with ≥2 sections
	// send "down" → summaryFocus==1
	// send " " → section 1 Included false in overrides
	// send "enter" → summaryExpanded[id]==true
	// send "e" on editable section → summaryEditing true
	// send "esc" → editing false; Edited set from textarea value
	// send "enter" must NOT transition to posted / must NOT call post
}
```

- [ ] **Step 2: Implement handlers**

```go
func (m *Model) handleSummarySectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.summaryEditing {
		switch msg.String() {
		case "esc":
			return m.closeSummaryEditor() // writes Edited from summaryInput.Value()
		}
		var cmd tea.Cmd
		m.summaryInput, cmd = m.summaryInput.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "up", "k":
		if m.summaryFocus > 0 {
			m.summaryFocus--
			m.rebuildBody()
		}
		return m, nil
	case "down", "j":
		if m.summaryFocus < len(m.summarySections)-1 {
			m.summaryFocus++
			m.rebuildBody()
		}
		return m, nil
	case " ":
		m.toggleSummaryInclude(m.summaryFocus)
		m.rebuildBody()
		return m, nil
	case "enter":
		m.toggleSummaryExpand(m.summaryFocus)
		m.rebuildBody()
		return m, nil
	case "e":
		return m.openSummaryEditor(m.summaryFocus)
	}
	return m, nil // let caller handle y/a/n/q/R/scroll
}
```

Wire `phaseSummary` so section keys run before viewport scroll; `y` still posts; **remove `enter` from the post case**.

`openSummaryEditor`: no-op if Fixed; else textarea like `newChallengeInput`, prefill `effectiveSectionMarkdown`, `summaryEditing=true`.

`closeSummaryEditor`: set override `{Included: sectionIncluded(...), Edited: summaryInput.Value()}`.

- [ ] **Step 3: Run** `go test ./internal/tui/tabs/review -run TestSummarySectionKeys -count=1` — PASS

- [ ] **Step 4: Commit**

```bash
git add internal/tui/tabs/review/model.go internal/tui/tabs/review/summary_sections.go internal/tui/tabs/review/summary_sections_test.go
git commit -m "$(cat <<'EOF'
feat(tui): keyboard controls for summary section cards

EOF
)"
```

---

### Task 7: Mouse — checkbox, header, edit zones

**Files:**
- Modify: `internal/tui/tabs/review/model.go` (`handleMouse` `phaseSummary` case)
- Modify: `internal/tui/tabs/review/summary_sections_test.go`

**Interfaces:**
- Consumes: `zones.SummarySectionHeader/Check/Edit`

- [ ] **Step 1: Write mouse test** (poll zone after Scan — see fact `bubblezone-scan-get-async` / `tabs_test.go` pattern)

```go
func TestSummarySectionMouseToggleAndExpand(t *testing.T) {
	// render + zone.Scan(view)
	// poll zone.Get(SummarySectionCheck(id)) until non-nil
	// synthesize click InBounds → Included flips
	// click header → Expanded flips
	// click Edit → summaryEditing
}
```

- [ ] **Step 2: In `handleMouse` for `phaseSummary`**, before footer buttons:

```go
if m.summaryEditing {
	return m, nil // swallow card/footer clicks while editing
}
for _, s := range m.summarySections {
	if z := zone.Get(zones.SummarySectionCheck(s.ID)); z != nil && z.InBounds(msg) {
		// find index, toggle include, rebuildBody
		return m, nil
	}
	if z := zone.Get(zones.SummarySectionHeader(s.ID)); z != nil && z.InBounds(msg) {
		// focus index, toggle expand, rebuildBody
		return m, nil
	}
	if !s.Fixed {
		if z := zone.Get(zones.SummarySectionEdit(s.ID)); z != nil && z.InBounds(msg) {
			return m.openSummaryEditor(index)
		}
	}
}
// existing StagedSummaryYes / No / ApproveOnly / Refresh / Quit
```

Wheel: leave existing viewport scroll path (do not special-case unless cards need it).

- [ ] **Step 3: Run** `go test ./internal/tui/tabs/review -run TestSummarySectionMouse -count=1` — PASS

- [ ] **Step 4: Commit**

```bash
git add internal/tui/tabs/review/model.go internal/tui/tabs/review/summary_sections_test.go
git commit -m "$(cat <<'EOF'
feat(tui): mouse zones for summary section cards

EOF
)"
```

---

### Task 8: Post path uses composed body

**Files:**
- Modify: `internal/tui/tabs/review/actions.go` (`actPostSummary`)
- Modify: `internal/tui/tabs/review/summary_sections_test.go`
- Modify: `internal/review/github_payload.go` only if override must participate in `EffectiveReviewEventAndBody` beyond `RenderBody`

**Interfaces:**
- Consumes: `composedSummaryBody()`, `Draft.PostedBodyOverride`

- [ ] **Step 1: Write dry-run / post body test**

```go
func TestActPostSummaryUsesComposedBody(t *testing.T) {
	// phaseSummary model; exclude a prompt topic via overrides
	// dryRun=true; invoke actPostSummary; drain DryRunPayloadMsg
	// assert payload body lacks excluded topic and contains caution marker
}
```

- [ ] **Step 2: Implement**

```go
func (m *Model) actPostSummary() (tea.Model, tea.Cmd) {
	if m.draft == nil || m.draft.PR == nil {
		return m, nil
	}
	m.syncUserSkipsToDraft()
	m.syncSummarySections()
	m.draft.PostedBodyOverride = m.composedSummaryBody()
	return m, data.PostReviewWithVerdictCmd(m.draft.Ref, m.draft, m.dryRun, m.demoMode, "")
}
```

Because `RenderBody` / `EffectiveReviewEventAndBody` read `PostedBodyOverride`, self-author downgrade still prepends its note to the composed body when it calls `RenderBody()`.

Clear override when leaving the overlay / starting a new review so stale bodies cannot leak (`closeCmd` / `AdoptDraft`): `m.draft.PostedBodyOverride = ""` and reset summary maps.

- [ ] **Step 3: Run** `go test ./internal/tui/tabs/review -run TestActPostSummaryUsesComposedBody -count=1` and `go test ./internal/review -count=1`

- [ ] **Step 4: Full package verification**

Run: `go test ./internal/review ./internal/tui/tabs/review ./internal/tui/zones -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/tabs/review/actions.go internal/tui/tabs/review/summary_sections.go internal/tui/tabs/review/summary_sections_test.go internal/tui/tabs/review/model.go
git commit -m "$(cat <<'EOF'
feat(tui): post composed summary body from section overrides

EOF
)"
```

---

### Task 9: Final verification + golden/fixture chase

**Files:**
- Any golden / teatest fixtures broken by help-line or summary view changes (e.g. `internal/tui/model/golden_test.go`, `teatest_flow_test.go`)

- [ ] **Step 1: Run full suite**

Run: `go test ./... -count=1`

- [ ] **Step 2: Fix only failures caused by this feature** (update goldens if the summary chrome/help text changed intentionally; do not weaken assertions)

- [ ] **Step 3: `go vet ./...` and `go build ./...`**

- [ ] **Step 4: Commit fixture updates if any**

```bash
git add -A
git commit -m "$(cat <<'EOF'
test: update fixtures for summary section editor

EOF
)"
```

---

## Spec coverage checklist

| Spec requirement | Task |
|------------------|------|
| BodySections + ComposeBody | 1 |
| RenderBody compatibility | 1 |
| Fine-grained prompt/PR-wide units | 1–2 |
| Caution fixed | 2 |
| Exclude / edit composition | 2 |
| Zone IDs | 3 |
| Session overrides + sync | 4 |
| Section cards UI + expand | 5 |
| Keyboard (+ enter≠post) | 6 |
| Mouse | 7 |
| Post composed body | 8 |
| Tests / goldens | 2, 6–9 |
| No reorder / no split preview / no Caution edit | enforced by omission |

## Placeholder / consistency notes (self-review)

- `PostedBodyOverride` is the single bridge into `EffectiveReviewEventAndBody` without changing `data.PostReviewWithVerdictCmd` signature.
- Prompt wrapper rebuild lives only in `ComposeBody`, not in the TUI.
- Override default: missing ⇒ included; explicit `{Included:false}` for excludes.
- `enter` no longer posts from `phaseSummary` — documented as intentional UX change.
