# Treatment: grounding-v1

First Phase 5 treatment arm for the plugin treatment gate.

**Change:** every specialist / generator system prompt is the embedded default
plus an **Evidence grounding** section that forbids inventing identifiers,
paths, and `path:line` refs not present in the diff/context.

**How to run** (compare to frozen control baselines, do not retune the gate):

```bash
export OLLAMA_CONTEXT_LENGTH=32768
export APPR_AI_SAL_CONFIG_DIR=$PWD/experiments/grounding-v1
make evals PROVIDER=ollama \
  EVAL_FLAGS="--model qwen2.5-coder:14b --strictness balanced --metrics-json reports/treatments/grounding-v1/qwen2.5-coder-14b-r1.json" \
  OUT=reports/treatments/grounding-v1/qwen2.5-coder-14b-r1.md
```

Or: `reports/treatments/grounding-v1/run-treatment.sh`
