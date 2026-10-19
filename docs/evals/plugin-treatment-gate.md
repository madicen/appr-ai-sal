# Plugin treatment gate (Phase 5)

**Status: registered 2026-10-19. Immutable after the first treatment run.**

Machine-readable copy: [`plugin-treatment-gate.json`](./plugin-treatment-gate.json).  
Template: `naitv-mcp-plugins/docs/plan/TESTING.md` §3 (pre-registered L5 gate).  
Plan: `docs/superpowers/plans/2026-10-15-small-model-eval.md` Phase 5.

## Arms

| Arm | Setup |
|---|---|
| Baseline (B) | Core `appr-ai-sal` review / evals pipeline only |
| Treatment (T) | Identical provider, model, context, strictness, corpus — plus the plugin / prompt treatment under test |

## Pinned agent

- Provider: `ollama`
- Models: `qwen2.5-coder:3b`, `7b`, `14b` (pass `--model` explicitly)
- Context: `OLLAMA_CONTEXT_LENGTH=32768`
- Strictness: `balanced`
- Repetitions: 3 per arm per model

If the runtime is unreachable or no model is pinned, record **`pending`**. Do not substitute another model.

## Gate (do not edit after first treatment)

1. **Unsupported-claim rate** must fall by **≥ 10 percentage points** vs the frozen control baseline at the same model and context, and the **95%-style CI on the three-rep delta must exclude zero** (`ci_must_exclude_zero`).
2. **Suggestion-breakage rate** must **not increase** (breakage = `1 − suggestion-compile` among surviving suggestions with a present toolchain).
3. **Recall on the original 12 corpus cases** must not fall by more than **2 percentage points**.
4. **Token count** per review (corpus totals) must not rise by more than **25%**.

## Outcomes

| Result | Meaning |
|---|---|
| `measured-helpful` | All predicates pass |
| `measured-no-uplift` | Live B/T ran; at least one predicate failed — keep the result, do not retune the gate |
| `pending` | No usable live run / no baseline snapshot |

A failed gate is a valid result. Do not tune the treatment toward the threshold and re-run for certification.

## Baseline snapshot

Phase 2’s live grid predates Phase 4’s unsupported-claim and suggestion-compile rates. Before the first treatment:

1. Run three control repetitions per model with the current harness (Phase 4 metrics on).
2. Write a snapshot under `docs/evals/baselines/` (committed).
3. Keep raw reports under `reports/` (gitignored).

Held-out PRs remain human-owned and are not used to tune the gate.
