# Treatment: claim-strip-v1

Hard / deterministic eval-only treatment. After `EvalRun`, drop findings that
have ≥1 worktree-unresolvable claim ref (same resolver as the Phase 4
unsupported-claim metric). Findings with no extractable refs are kept.

```bash
export OLLAMA_CONTEXT_LENGTH=32768
export APPR_AI_SAL_EVAL_TREATMENT=claim-strip-v1
# corpus (Phase 5 gate):
./experiments/claim-strip-v1/run-treatment.sh
# held-out (v2 floor check):
./experiments/claim-strip-v1/run-held-out.sh
```

Does not change production review behaviour unless this arm graduates.
