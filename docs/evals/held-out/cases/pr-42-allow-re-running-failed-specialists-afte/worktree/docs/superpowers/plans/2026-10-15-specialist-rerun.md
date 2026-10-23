# Specialist / synthesis re-run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After a review pipeline finishes, let the reviewer re-run failed specialists/PR agents and manually re-run arbiter/vibe-coach from the overlay (keyboard + mouse), with stale synthesis flags and a soft summary banner.

**Architecture:** Capture a non-serialized `Draft.Rerun *RerunContext` at pipeline end with the exact agent inputs plus a pre-arbiter specialist snapshot. Expose `RunSpecialistForDraft` / `RunPRAgentForDraft` / `RunRepoArbiterForDraft` (vibe already has `RunVibeCoachForDraft`). TUI issues `tea.Cmd`s, replaces results, marks `ArbiterStale`/`VibeStale`, and offers `r`/`R` plus bubblezone click targets.

**Tech Stack:** Go, Bubble Tea, bubblezone, existing `internal/review` stage retry helpers.

## Global Constraints

- Replace failed results in place; do not keep prior failed findings for synthesis.
- Only failed specialists/PR agents are re-runnable; arbiter/vibe always when their rows exist and `Rerun != nil`.
- Do not auto-cascade arbiter/vibe after specialist re-run — mark stale only.
- Soft summary gate (banner, posting still allowed).
- Circuit breaker off for manual re-runs; stage retries stay on.
- Skipped (breaker) agents are not re-runnable in v1.
- Restored drafts without `RerunContext` disable re-run actions.
- YAGNI / lazy-ladder: smallest code that works; verify via naitv `build`/`vet`/`test`.
- Do not commit unless the user asks.

---

## File structure

| File | Role |
| --- | --- |
| `internal/review/rerun.go` | `RerunContext`, stale helpers, `ReplaceSpecialist`, `Run*ForDraft` |
| `internal/review/rerun_test.go` | Unit tests for replace/stale/helpers |
| `internal/review/draft.go` | `Rerun`, `ArbiterStale`, `VibeStale` fields |
| `internal/review/runner.go` | Fill `RerunContext` + pre-arbiter snapshot before finalize |
| `internal/tui/tabs/review/rerun.go` | Msgs, cmds, key/mouse handlers for re-run |
| `internal/tui/tabs/review/rerun_test.go` | TUI key/mouse/banner tests |
| `internal/tui/tabs/review/view.go` | Stale status, Re-run / Re-run all failed zones |
| `internal/tui/tabs/review/model.go` | Wire keys + mouse into Update |
| `internal/tui/tabs/review/summary.go` / progress help | Soft-gate banner + help text |

---

### Task 1: Draft fields + RerunContext + replace/stale helpers

**Files:**
- Create: `internal/review/rerun.go`
- Create: `internal/review/rerun_test.go`
- Modify: `internal/review/draft.go` (add fields on `Draft`)
- Modify: `internal/review/pragents.go` or keep `PRAgentInput` referenced from `rerun.go`

**Interfaces:**
- Produces: `type RerunContext struct`, `Draft.Rerun`, `Draft.ArbiterStale`, `Draft.VibeStale`, `(*Draft).ReplaceAgent(name string, res SpecialistResult)`, `(*Draft).MarkSynthesisStale()`, `(*Draft).CanRerunAgent(name string) bool`, `(*Draft).FailedAgentNames() []string`

- [ ] **Step 1: Write the failing test**

```go
package review

import (
	"errors"
	"testing"
)

func TestReplaceAgentAndMarkSynthesisStale(t *testing.T) {
	d := &Draft{
		Specialists: []SpecialistResult{
			{Specialist: SpecFormatting, Findings: []Finding{{Path: "a.go", Line: 1, Severity: SeverityWarning, Title: "x"}}},
			{Specialist: SpecDesign, Err: errors.New("boom"), Outcome: OutcomeFailed},
		},
		Rerun: &RerunContext{
			PreArbiterSpecialists: []SpecialistResult{
				{Specialist: SpecFormatting, Findings: []Finding{{Path: "a.go", Line: 1, Severity: SeverityWarning, Title: "x"}}},
				{Specialist: SpecDesign, Err: errors.New("boom"), Outcome: OutcomeFailed},
			},
		},
		DemotedHidden: []FlatFinding{{Specialist: SpecFormatting}},
	}
	if !d.CanRerunAgent(SpecDesign) {
		t.Fatal("failed design should be re-runnable")
	}
	if d.CanRerunAgent(SpecFormatting) {
		t.Fatal("clean formatting must not be re-runnable")
	}
	ok := SpecialistResult{Specialist: SpecDesign, Findings: []Finding{{Path: "b.go", Line: 2, Severity: SeverityWarning, Title: "y"}}}
	if err := d.ReplaceAgent(SpecDesign, ok); err != nil {
		t.Fatal(err)
	}
	if d.Specialists[1].Err != nil || len(d.Specialists[1].Findings) != 1 {
		t.Fatalf("replace failed: %+v", d.Specialists[1])
	}
	d.MarkSynthesisStale()
	if !d.ArbiterStale || !d.VibeStale {
		t.Fatalf("stale flags = arbiter:%v vibe:%v", d.ArbiterStale, d.VibeStale)
	}
	if len(d.DemotedHidden) != 0 {
		t.Fatalf("DemotedHidden should clear on stale, got %d", len(d.DemotedHidden))
	}
}

func TestStaleTransitionsOnArbiterAndVibe(t *testing.T) {
	d := &Draft{ArbiterStale: true, VibeStale: true}
	d.ClearArbiterStale() // sets VibeStale
	if d.ArbiterStale || !d.VibeStale {
		t.Fatalf("after arbiter clear: arbiter=%v vibe=%v", d.ArbiterStale, d.VibeStale)
	}
	d.ClearVibeStale()
	if d.VibeStale {
		t.Fatal("vibe should be clear")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/review/ -run 'TestReplaceAgentAndMarkSynthesisStale|TestStaleTransitionsOnArbiterAndVibe' -count=1`
Expected: FAIL (undefined types/methods)

- [ ] **Step 3: Minimal implementation**

Add to `Draft` in `draft.go`:

```go
Rerun        *RerunContext `json:"-"`
ArbiterStale bool          `json:"-"`
VibeStale    bool          `json:"-"`
```

In `rerun.go`, define `RerunContext` with at least:

```go
type RerunContext struct {
	SpecialistDiff   string
	PRAgentDiff      string
	PerAgent         map[string]string
	PREvidence       string
	StaticSection    string
	StaticCleanFiles map[string]bool
	LangSection      string
	TechSection      string
	IntentSection    string
	ExpandedSection  string
	RejectedMemory   string
	PRAgentInput     PRAgentInput
	// PreArbiterSpecialists is the specialist+PR-agent slice after dedupe +
	// memory suppression and before FinalizeRepoArbiter. Re-runs rebase from here.
	PreArbiterSpecialists []SpecialistResult
	ParallelSpecialists   bool
	ParallelPRAgents      bool
	ConventionWitness     bool
	RepoExpertPanel       bool
	RepoArbiterDemotions  bool
}
```

Implement `CanRerunAgent` (failed specialist/PR only; `overlayAgentRepoArbiter` / `SpecVibeCoach` handled in TUI), `FailedAgentNames`, `ReplaceAgent` (swap in both `Specialists` and `PreArbiterSpecialists` by name; then set `Specialists` from a copy of pre-arbiter with replace applied — re-run dedupe+memory in Task 2), `MarkSynthesisStale`, `ClearArbiterStale`, `ClearVibeStale`.

For Task 1, `ReplaceAgent` may simply replace by name in both slices and clear `DemotedHidden` when `MarkSynthesisStale` is called. Dedupe/memory refresh lands in Task 2.

- [ ] **Step 4: Run tests — expect PASS**

- [ ] **Step 5: Commit only if user asks**

---

### Task 2: Run*ForDraft helpers + fill RerunContext in runner

**Files:**
- Modify: `internal/review/rerun.go`
- Modify: `internal/review/runner.go` (capture inputs; set `final.Rerun`; snapshot pre-arbiter specialists)
- Modify: `internal/review/rerun_test.go`
- Optionally extract shared `runOne` from `runSpecialistsPhase` / `runPRAgentsPhase` into unexported helpers called by both phase and ForDraft

**Interfaces:**
- Consumes: `RerunContext`, `runReviewSpecialist`, `runPRAgent`, `RunRepoArbiter`, `FinalizeRepoArbiter`, `stageWithRetry`
- Produces: `RunSpecialistForDraft`, `RunPRAgentForDraft`, `RunRepoArbiterForDraft`, `RefreshDraftAfterAgentReplace`

- [ ] **Step 1: Failing test with fake provider** (reuse patterns from `retry_budget_test.go` / agent tests)

```go
func TestRunSpecialistForDraftUsesRerunDiff(t *testing.T) {
	// Build Draft with Rerun.SpecialistDiff containing a unique marker path;
	// use a recording fake Complete that fails if user prompt lacks marker.
	// Call RunSpecialistForDraft(... SpecDocs ...) and assert Err==nil or that
	// the fake saw the marker (match existing fake-provider style in package).
}
```

- [ ] **Step 2: Run — expect FAIL**

- [ ] **Step 3: Implement helpers**

`RunSpecialistForDraft`: require `d.Rerun != nil`; run same retry/ensemble path as `runSpecialistsPhase`’s `runOne` **without** circuit breaker; no Progress channel required (optional notify only).

`RunPRAgentForDraft`: same for PR agents using `Rerun.PRAgentDiff` + `PRAgentInput`.

`RunRepoArbiterForDraft`: if `!Rerun.RepoExpertPanel` return nil result with err; else optionally refresh convention witness (reuse `runConventionWitnessPhase` logic with `breaker=nil` / no skip); call `RunRepoArbiter` + `FinalizeRepoArbiter`; `ClearArbiterStale()` (which sets `VibeStale`).

`RefreshDraftAfterAgentReplace(d, name, res)`:
1. Replace in `PreArbiterSpecialists`
2. Copy → `dedupeInlineFindingsAcrossSpecialists` → `ApplyMemorySuppression` (reload memory via `LoadRepoMemory(d.PR)` or store memory section only — fail-open empty if needed)
3. Assign to `d.Specialists`
4. `MarkSynthesisStale()`

Wire runner: before `FinalizeRepoArbiter`, snapshot `allSpecialists` into `Rerun.PreArbiterSpecialists`. After building `final`, set `final.Rerun = &RerunContext{...}` with all captured locals. Keep filling even when downstream skipped so failed-specialist re-run still works.

- [ ] **Step 4: Tests PASS + `go test ./internal/review/ -count=1`**

---

### Task 3: TUI re-run commands (keyboard)

**Files:**
- Create: `internal/tui/tabs/review/rerun.go`
- Create: `internal/tui/tabs/review/rerun_test.go`
- Modify: `internal/tui/tabs/review/model.go` (Update key handling when draft ready / not `phaseRunning`)
- Modify: help/footer strings in view or progress

**Interfaces:**
- Produces: `AgentRerunDoneMsg`, `startAgentRerunCmd`, `startFailedAgentsRerunCmd`, model methods `rerunFocusedAgent() tea.Cmd`, `rerunAllFailed() tea.Cmd`

- [ ] **Step 1: Failing TUI test**

```go
func TestKeyRTriggersRerunOnFailedAgent(t *testing.T) {
	// Build Model with completed draft, design row failed, cursor on design.
	// Update with KeyMsg "r"; assert coach/rerun in-flight flag or that a Cmd is returned (non-nil).
}

func TestKeyRNoopOnCleanAgent(t *testing.T) {
	// Cursor on clean formatting → Update "r" returns nil cmd / no in-flight.
}
```

- [ ] **Step 2: Implement msgs/cmds mirroring `runVibeCoachCmd`**

```go
type AgentRerunDoneMsg struct {
	Name   string
	Result review.SpecialistResult
	Kind   string // "specialist" | "pr-agent" | "arbiter" | "vibe"
	Err    error
	Batch  bool
}

func (AgentRerunDoneMsg) OverlayBound() {}
```

On done: `RefreshDraftAfterAgentReplace` or arbiter/vibe finalize; merge overlay row phase; `rebuildBody`.

`r` → focused agent if `CanRerun` / arbiter / vibe.  
`R` → all `FailedAgentNames()` (specialists parallel per `Rerun.ParallelSpecialists`, PR agents per `ParallelPRAgents` — simple v1: run all failed sequentially in one cmd to avoid complexity, or errgroup parallel; prefer parallel when flag set).

Cancel/Esc: cancel context stored on model for in-flight re-run; restore snapshot.

- [ ] **Step 3: Tests PASS**

---

### Task 4: Mouse affordances + stale UI + soft summary banner

**Files:**
- Modify: `internal/tui/tabs/review/view.go`
- Modify: `internal/tui/tabs/review/model.go` (mouse zone hits)
- Modify: `internal/tui/tabs/review/summary.go` or summary render path
- Modify: `internal/tui/tabs/review/rerun_test.go`

**Zones:** use package-local ids like `zoneOverlayAgentRerun(i)` and `zoneOverlayRerunAllFailed` (same pattern as `zoneOverlayAgent`).

- [ ] **Step 1: Failing tests for zone names + banner string when `ArbiterStale`**

- [ ] **Step 2: Render**
  - Failed rows: append `zone.Mark(..., " Re-run (r) ")`
  - Arbiter/vibe always (when rerun available): same control
  - Stage header when `FailedAgentNames` non-empty: `Re-run all failed (R)`
  - `agentStatusDetail`: show `stale` warning when draft flags set for arbiter/vibe
  - Summary/approve entry: banner text from spec

- [ ] **Step 3: Mouse handler** — if click in re-run zone, same as `r`/`R`

- [ ] **Step 4: Tests PASS**

---

### Task 5: Integration verification

- [ ] **Step 1:** `go test ./internal/review/ ./internal/tui/tabs/review/ -count=1`
- [ ] **Step 2:** naitv `build` / `vet` / `test` on project root
- [ ] **Step 3:** Manual checklist in PR description (failed design → `r` → stale arbiter/vibe → `r` each → banner clears)

---

## Spec coverage check

| Spec item | Task |
| --- | --- |
| Post-pipeline repair | 3–4 |
| Replace in place | 1–2 |
| Failed-only + arbiter/vibe | 1, 3 |
| Stale, no auto-cascade | 1–2 |
| Per-agent + batch | 3–4 |
| Keyboard + mouse | 3–4 |
| Soft summary gate | 4 |
| RerunContext / restore disable | 1–2, 3 |
| Convention witness on arbiter re-run | 2 |
| Circuit breaker off on manual | 2 |
| Cancel restores snapshot | 3 |
| Usage metering | 3 (best-effort attach existing usage if trivial; else overlay counter) |

## Execution note

Prefer sequential tasks in one session (inline). Commit only on user request.
