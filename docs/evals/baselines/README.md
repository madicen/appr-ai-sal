# Eval control baselines

Frozen control-arm snapshots live here **before** the first plugin treatment run
(see `docs/evals/plugin-treatment-gate.json` → `baseline_protocol`).

Committed files: `<model-tag>-ctx32768-balanced-v1.json` (one per model), each
holding three repetitions of unsupported-claim, suggestion-breakage,
original-12 recall, and token totals.

Produce a per-run metrics file with:

```bash
make evals PROVIDER=ollama \
  EVAL_FLAGS="--model qwen2.5-coder:7b --strictness balanced --metrics-json reports/baselines/qwen2.5-coder-7b-r1.json" \
  OUT=reports/baselines/qwen2.5-coder-7b-r1.md
```

Raw markdown / per-run JSON stay under `reports/` (gitignored).
