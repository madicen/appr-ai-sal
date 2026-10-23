# Treatment: inventory-v1

Eval-only treatment: inject a worktree **file + identifier inventory** into
every specialist's repo-context brief (`APPR_AI_SAL_EVAL_TREATMENT=inventory-v1`).

Unlike grounding-v1 (instruction-only), this supplies facts the unsupported-claim
resolver also checks.

```bash
export OLLAMA_CONTEXT_LENGTH=32768
export APPR_AI_SAL_EVAL_TREATMENT=inventory-v1
# see reports/treatments/inventory-v1/run-treatment.sh
```
