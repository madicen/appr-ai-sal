#!/usr/bin/env bash
# Held-out 3×3 for claim-strip-v1 (compare to docs/evals/held-out/*-v1.json).
set -uo pipefail
export PROVIDER=ollama OLLAMA_CONTEXT_LENGTH=32768
export APPR_AI_SAL_EVAL_TREATMENT=claim-strip-v1
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
OUT_DIR=reports/treatments/claim-strip-v1/held-out
mkdir -p "$OUT_DIR"
for model in qwen2.5-coder:3b qwen2.5-coder:7b qwen2.5-coder:14b; do
  slug=${model/qwen2.5-coder:/qwen2.5-coder-}
  for r in 1 2 3; do
    echo "=== $(date -u +%Y-%m-%dT%H:%M:%SZ) START held-out $slug r$r ==="
    if ! make evals PROVIDER=ollama \
      EVAL_FLAGS="--model $model --strictness balanced --corpus-dir docs/evals/held-out/cases --metrics-json ${OUT_DIR}/${slug}-r${r}.json" \
      OUT="${OUT_DIR}/${slug}-r${r}.md" \
      > "${OUT_DIR}/${slug}-r${r}.run.log" 2>&1; then
      echo "=== $(date -u +%Y-%m-%dT%H:%M:%SZ) FAIL held-out $slug r$r ==="
      exit 1
    fi
    echo "=== $(date -u +%Y-%m-%dT%H:%M:%SZ) DONE held-out $slug r$r ==="
  done
done
echo "=== claim-strip-v1 held-out COMPLETE $(date -u +%Y-%m-%dT%H:%M:%SZ) ==="
