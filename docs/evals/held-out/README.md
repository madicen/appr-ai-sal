# Held-out eval set

Human-owned real PRs. Label findings **before** any model run. Corpus cases
tune prompts/gates; only this set decides whether a treatment helps in the wild
(see `docs/superpowers/plans/2026-10-15-small-model-eval.md`).

## Layout

```
docs/evals/held-out/cases/<id>/
  case.json
  diff.patch
  expectations.json   # must_appear / must_not_appear — fill by hand
  LABEL_STATUS.md     # labelled | pending
  worktree/           # optional; required for unsupported-claim / compile rates
```

## Import a PR skeleton (no model, no labels)

```bash
./scripts/heldout-import.sh 45          # madicen/appr-ai-sal#45
./scripts/heldout-import.sh owner/repo 99
```

Then edit `expectations.json` and set `LABEL_STATUS.md` to `labelled` **before**:

```bash
make evals PROVIDER=ollama \
  EVAL_FLAGS="--model qwen2.5-coder:14b --strictness balanced --corpus-dir docs/evals/held-out/cases --metrics-json reports/held-out/r1.json" \
  OUT=reports/held-out/r1.md
```

Do **not** retune the Phase 5 gate from held-out results.
