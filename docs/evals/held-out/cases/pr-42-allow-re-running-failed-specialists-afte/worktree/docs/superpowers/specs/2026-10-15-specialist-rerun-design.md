# Specialist / synthesis re-run (post-pipeline repair)

## Goal

After an automated review finishes, the reviewer can re-run failed specialists (and PR agents) and manually re-run the repo arbiter and vibe-coach, so a transient agent failure does not force either a full re-review or posting with a permanently missing lane.

## Decisions (locked)

| Topic | Choice |
| --- | --- |
| When | Full pipeline runs first (today’s behavior, including stage retries). Repair is post-pipeline. |
| Failed agent result | Replace in place — prior failed `SpecialistResult` is discarded. |
| Who is re-runnable | Failed specialists / PR agents only; arbiter and vibe-coach whenever their overlay rows exist for this run (repo expert panel on for arbiter; vibe row present even if the initial pass skipped/failed) and `RerunContext` exists. |
| Synthesis after specialist re-run | Mark arbiter + vibe **stale**; do **not** auto-re-run them. |
| Kickoff UX | Per-agent re-run **and** “re-run all failed”; keyboard **and** mouse. |
| Summary gate | Soft gate: banner when synthesis is stale; posting still allowed. |
| Approach | Extend the existing vibe-coach lazy re-run pattern (`RunVibeCoachForDraft` + TUI `tea.Cmd`). |

## Out of scope

- Pausing the pipeline before arbiter / vibe-coach.
- Auto-cascading arbiter / vibe after a specialist re-run.
- Re-running specialists that finished clean (no “force” path in v1).
- Re-running agents skipped by the initial-run circuit breaker.
- Re-run after session/draft restore when `RerunContext` was not persisted.

## Architecture

```
Initial Run (unchanged)
  specialists / PR agents → witness → arbiter → vibe → Draft + RerunContext

Overlay repair
  r / click  → Run*ForDraft → replace agent slot → mark synthesis stale
  R / click  → batch failed specialists/PR agents → mark synthesis stale
  r / click on arbiter → refresh witness if needed → RunRepoArbiterForDraft → clear ArbiterStale, set VibeStale
  r / click on vibe    → RunVibeCoachForDraft → clear VibeStale
```

### `RerunContext` (on `Draft`)

Non-serialized sidecar (`json:"-"`) captured at the end of the pipeline with the exact inputs the agents used:

- Specialist / PR-agent shaped diffs (as used in the run, including incremental reduced diff when applicable)
- Per-agent repo briefs, PR evidence, static section + clean-file set
- Lang / tech / intent / expanded sections
- Rejected-memory section for the arbiter
- Anything else required so `runReviewSpecialist` / PR-agent / `RunRepoArbiter` paths are byte-faithful to the original run

If `Draft.Rerun == nil` (e.g. restored cache without sidecar), re-run actions are disabled with a short reason: “re-run unavailable for restored session.”

### Review-package helpers

Extract the existing per-agent `runOne` bodies behind:

- `RunSpecialistForDraft(ctx, cfg, d, name, notify) SpecialistResult`
- `RunPRAgentForDraft(ctx, cfg, d, name, notify) SpecialistResult`
- `RunRepoArbiterForDraft(ctx, cfg, d, notify) *RepoArbiterResult` — wraps `RunRepoArbiter` + `FinalizeRepoArbiter`
- Keep `RunVibeCoachForDraft` as today

Shared behavior:

- Same `stageWithRetry` + per-stage timeout / ensemble routing as the pipeline.
- Circuit breaker **off** for manual re-runs (user-initiated; do not skip the rest of a batch because one failed again).
- Replace-in-place via `Draft.ReplaceSpecialist(name, res)` (or equivalent).
- After a specialist/PR-agent replace: re-apply cross-specialist inline dedupe + reviewer-memory suppression so the draft stays consistent before the next arbiter pass.
- When arbiter is marked stale: clear demotion/suppress bookkeeping that depended on the old arbiter result; arbiter re-run rebuilds via `FinalizeRepoArbiter`.

### Stale flags

On `Draft` (preferred over overlay-only so summary/post can see them):

- `ArbiterStale bool`
- `VibeStale bool`

Transitions:

| Event | ArbiterStale | VibeStale |
| --- | --- | --- |
| Specialist / PR-agent re-run finishes (success or fail) | set | set |
| Arbiter re-run succeeds or fails | clear | set |
| Vibe re-run succeeds or fails | unchanged | clear |

Convention witness: refreshed as part of **arbiter** re-run when testing/docs/tech findings may have changed (same enablement gates as the pipeline). Specialist-only re-run does not run the witness until arbiter is requested.

## TUI / UX

### When enabled

After the pipeline has produced a draft. Disabled while the initial run is still streaming. Disabled when `RerunContext` is missing.

### Actions

| Action | Keyboard | Mouse |
| --- | --- | --- |
| Re-run focused / target agent | Overview: `r`. Agent tab: `u` (`r`/`R` already mean refresh PR there). | **Re-run** on the overview row / agent-tab header (failed specialist/PR-agent, or arbiter/vibe) |
| Re-run all failed specialists/PR-agents | Overview: `R`. Agent tab: `U`. | **Re-run all failed** on the overview status strip |

Rules:

- `r` / row Re-run: only if the agent is allowed (failed specialist/PR-agent, or arbiter/vibe when that row exists). Clean specialists: no-op. If the arbiter row is absent (`RepoExpertPanel` off), there is nothing to re-run for arbiter.
- `R` / batch: every currently failed specialist/PR-agent; parallel iff repo config `ParallelSpecialists` (same as pipeline). Arbiter/vibe are not included in the batch.
- Ignore duplicate kicks while that agent (or batch) is already running; optional dim “already running” hint.
- Help footer lists `r` / `R` and the mouse affordances.

### Row presentation

- Failed → `failed` (existing).
- Re-running → `running` + retry detail (existing progress merge).
- Success → normal clean / finding-count presentation; prior failure discarded.
- Arbiter/vibe stale → `stale` (warning color); prior content still readable; not shown as failed unless the last attempt failed.
- Batch stale flip: set arbiter/vibe stale when the batch/command **completes** (not on each mid-batch specialist finish) to avoid thrashing.

### Cancel

Esc / context cancel aborts the in-flight re-run command. Restore the pre-re-run agent snapshot (no partial swap).

### Approve / summary (soft gate)

If `ArbiterStale` or `VibeStale`, show a persistent banner, e.g. “Arbiter/vibe out of date — re-run those rows, or continue with stale synthesis.” Posting remains allowed; the current arbiter/vibe values on the draft are what ship (same as today’s degraded runs).

### Usage

Re-run tokens append to the run usage accumulator when still attached; otherwise an overlay-local counter. Do not drop cost signal silently.

## Error handling

1. Re-run still fails → replace with the new failed result; synthesis stays/marks stale; user may retry.
2. Initial-run circuit-breaker **skipped** agents are not failed and are not re-runnable in v1.
3. Restored sessions without `RerunContext` → actions hidden/disabled with reason.

## Testing

Minimal runnable checks:

1. Unit: stale-flag transitions and `ReplaceSpecialist` behavior.
2. Unit: `RunSpecialistForDraft` consumes `RerunContext` (fake provider).
3. TUI: `r` on failed row starts re-run; `r` on clean specialist no-ops; mouse Re-run hits the same path; summary banner when stale.

## Implementation sketch (non-binding order)

1. `RerunContext` + fill at end of `runner` pipeline; stale fields + replace helper.
2. Extract `RunSpecialistForDraft` / `RunPRAgentForDraft` / `RunRepoArbiterForDraft`.
3. Overlay: key + mouse actions, row states, batch, cancel snapshot.
4. Summary soft-gate banner.
5. Tests above.

## Alternatives considered

1. **Extend vibe-coach lazy pattern (chosen)** — smallest surface; matches existing TUI re-run.
2. **Monolithic `review.Rerun(draft, targets)` API** — cleaner boundary, more new surface than needed.
3. **Cached full re-pipeline skipping successes** — poor fit for arbiter-only / vibe-only re-runs.
