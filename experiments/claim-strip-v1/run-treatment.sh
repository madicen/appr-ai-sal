#!/usr/bin/env bash
# Corpus 3×3 for claim-strip-v1 (Phase 5 gate vs docs/evals/baselines/).
set -uo pipefail
export PROVIDER=ollama OLLAMA_CONTEXT_LENGTH=32768
export APPR_AI_SAL_EVAL_TREATMENT=claim-strip-v1
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
OUT_DIR=reports/treatments/claim-strip-v1
mkdir -p "$OUT_DIR"
for model in qwen2.5-coder:3b qwen2.5-coder:7b qwen2.5-coder:14b; do
  slug=${model/qwen2.5-coder:/qwen2.5-coder-}
  for r in 1 2 3; do
    echo "=== $(date -u +%Y-%m-%dT%H:%M:%SZ) START $slug r$r ==="
    if ! make evals PROVIDER=ollama \
      EVAL_FLAGS="--model $model --strictness balanced --metrics-json ${OUT_DIR}/${slug}-r${r}.json" \
      OUT="${OUT_DIR}/${slug}-r${r}.md" \
      > "${OUT_DIR}/${slug}-r${r}.run.log" 2>&1; then
      echo "=== $(date -u +%Y-%m-%dT%H:%M:%SZ) FAIL $slug r$r ==="
      exit 1
    fi
    echo "=== $(date -u +%Y-%m-%dT%H:%M:%SZ) DONE $slug r$r ==="
  done
done
echo "=== claim-strip-v1 corpus COMPLETE $(date -u +%Y-%m-%dT%H:%M:%SZ) ==="
