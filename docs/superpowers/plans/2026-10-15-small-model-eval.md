# Test plan: are small local models producing hallucinated review findings in appr-ai-sal?

Status: draft for review. Read-only analysis of madicen/appr-ai-sal as of 2026-10-15. Nothing in the repo has been changed.

## Goal

Measure how often a small local model (served through Ollama, via the `ollama` provider) produces review findings that are unsupported by the code, and find out how much of that comes from context truncation versus model weakness versus the prompt. Then set a baseline that the naitv-mcp plugins must beat, using the same pre-registered-gate method as `naitv-mcp/TESTING.md` section 3.

## What we already know from the code

- The pipeline already has deterministic gates for anchor drift, suggestion breakage, bare-deficiency comments, language-convention errors, and IaC schema errors. A finding that survives all of them is still unverified against the rest of the repo.
- Ollama requests send no `num_ctx` (`internal/ai/openai.go`). The server's default context applies. The diff budget for Ollama is 256 KiB (`internal/review/diff_budget.go`), which is about 60–75k tokens of diff alone, before the system prompt, briefs, and output contract. If a prompt exceeds the server's context, the server truncates it silently. That can drop the output contract or the front of the diff, and the model then guesses.
- The output contract alone is roughly 2–3k tokens by the code's own comments.
- Expanded context (enclosing functions, callers) is Go-only.
- The eval harness (`internal/evals`, `make evals`) runs the real pipeline against 12 corpus cases and reports recall, precision, suggestion survival, anchor-hit rate, JSON first-try rate, and token cost. The committed report is replay-only. No live small-model numbers are in the repo.
- Backtick identifiers in comments are checked only against the hunk, not the repo.

## Phase 0: check the context setting (30 minutes, no model quality involved)

1. For each model you plan to use, run `ollama show <model> --modelfile` and note any `num_ctx` parameter.
2. Start the server with a logged context. Recent Ollama versions read `OLLAMA_CONTEXT_LENGTH`. Older versions need a Modelfile with `PARAMETER num_ctx`. Use whichever your version supports.
3. Run one large PR through `appr-ai-sal --headless` (or the eval harness) and search the Ollama server log for `truncat`. Record the prompt token count the server reports.
4. Decide the context size to use for the rest of the plan (for example 16k or 32k). Record it with every result. Results at different context sizes are not comparable.

Exit criterion: no truncation warnings on the largest corpus case at the chosen context size.

## Phase 1: harness sanity (1 hour)

- Run `make evals EVAL_FLAGS=--replay`. It should be green and match `docs/evals-report.md`.
- Run `make evals PROVIDER=ollama` on one model with the default settings. Confirm the report is written and that the skip logic works when Ollama is down (the harness should exit 0 with a skip message).

## Phase 2: live baseline grid

Models (pick one from each row, same family where possible):
- a 3B model, for example `llama3.2:3b`
- a 7–8B coder model, for example `qwen2.5-coder:7b`
- a larger reference model, for example `qwen2.5-coder:14b` or better, used only to check that the corpus expectations are achievable

Settings per model: the chosen context size from Phase 0, and a second run at the Ollama default context so the truncation effect is measured directly.

Runs: three repetitions per setting, because the harness runs each agent once and local sampling varies. Report the mean and the range.

Commands (from `docs/EVALS.md`):

```
make evals PROVIDER=ollama EVAL_FLAGS="--model qwen2.5-coder:7b --strictness balanced" OUT=reports/q7b-ctx16k-r1.md
```

Record for each run: model, context size, strictness, the three repetitions, token counts, and the report path.

## Phase 3: add hallucination-specific cases

The current 12 cases are small and mostly contain one seeded issue each. They can't show unsupported claims well. Add about 10 new cases under `internal/evals/corpus/`:

- Comments that name an identifier, function, or path that doesn't exist anywhere in the worktree. These are negatives. Put the invented name in `must_not_appear`.
- Comments that correctly name an identifier that exists in another file. These are positives.
- Suggestions that compile in isolation but break the file when applied (for example, a Go suggestion that uses a variable not in scope). These are negatives for suggestion survival.
- Changes in a non-Go language (Python, TypeScript, Terraform), where the expanded context is empty. These show the Go-only limitation.
- A large diff near the budget, with the relevant change at the end, to test truncation.

Each new case needs a worktree fixture (the eval harness currently replays from `diff.patch` and canned responses, so the fixture must include the repo files the claims refer to). Check first whether the harness can read a fixture tree; if not, that is the first code change this plan needs.

## Phase 4: new metrics

Add two metrics to `internal/evals/score.go`. They are computed for every live run, not just replay.

1. Unsupported-claim rate: for each finding, extract backtick identifiers, file paths, and `path:line` references from the comment. Check each against the case's worktree fixture. Count findings with at least one unresolvable reference. Report the rate and the number of findings checked.
2. Suggestion-compile rate: for each surviving suggestion, apply it to a temporary copy of the file and run the language's parse or compile check (`gofmt -e`, `python -m py_compile`, `tsc --noEmit` on the file, or `terraform validate` on the module). Report the share that compile.

These two metrics are the ones the plugins are supposed to move. Define them before running any treatment.

## Phase 5: pre-registered gate for the plugins

Use the gate from `naitv-mcp/TESTING.md` section 3 as the template. Write it into the eval config before the first treatment run and do not change it afterwards:

- Treatment must reduce the unsupported-claim rate by at least 10 percentage points against the Phase 2 baseline at the same model and context size, with a confidence interval that excludes zero across three repetitions.
- Treatment must not increase the suggestion-breakage rate.
- Treatment must not reduce recall on the existing 12 cases by more than 2 percentage points.
- Treatment must not add more than 25% to the token count per review.

Outcomes: `measured-helpful`, `measured-no-uplift`, or `pending` (if no model could be run).

## Held-out set

Keep a held-out set of real PRs from your own work, with findings labelled by you before any model runs. The corpus cases are used to tune prompts and gates. Only the held-out set should be used to decide whether a plugin helps.

## Decisions needed from you

1. Which small models to test. You already run some locally; please name them and their quantization.
2. Whether you want the worktree-fixture change in Phase 3 (a code change to the eval harness) reviewed as its own PR first.
3. Who owns the held-out PRs and their labels.

## Rough cost

Phase 0 and 1 are under a day. Phases 2 to 4 depend on local hardware: each live run is roughly 85 model calls on the replay corpus, so expect a run to take a while on a 7B model. Budget for about 3 settings × 3 models × 3 repetitions in Phase 2.

## Phase 2b: raw-output audit

Purpose: find out whether the low recall in Phase 2 is a model miss or a matcher miss, and what the unlabelled findings contain. Precision cannot see unlabelled findings, so this audit is the only current check for hallucinations.

Phase 2b dumps the full 12-case corpus for qwen2.5-coder:3b, 7b, and 14b at one repetition each, with `--dump-raw`. The auditor reads only the three case directories `security-weak-hash`, `testing-error-branch`, and `docs-missing-godoc`. Do not add a `--case` flag.
Models: qwen2.5-coder:3b, 7b, 14b. Use one repetition each, with OLLAMA_CONTEXT_LENGTH=32768 and --model passed explicitly.
Command: make evals PROVIDER=ollama EVAL_FLAGS="--model <tag> --strictness balanced --dump-raw reports/audit/<tag>" OUT=reports/audit/<tag>.md

For each finding in each dumped file under those three case directories, record one row in reports/audit/findings.csv (untracked) with: case, model, agent, path, line, whether the line exists in the diff at that number, every backtick identifier named in the comment and whether it appears on any added or context line of that case's `diff.patch` ("in diff") or else "not in diff (unverified)" — never record an identifier as nonexistent. This is weaker than a repo check and will be replaced in Phase 3 when fixtures exist. Also record whether the comment describes the seeded issue (yes, partly, no), and whether a suggestion was given and whether it compiles when applied (yes, no, not tried).

Then write a short summary in docs/superpowers/plans/2026-10-15-small-model-eval-results.md under "Phase 2b": counts per model of findings, seeded-issue hits, unsupported references, and suggestions that compile. Also list any must-appear finding that was described in different words, so the matcher pattern can be reviewed. Do not edit any expectation file. That results file is created by the audit, not before it.

Stop criteria: if the 14b model's must-appear findings are mostly described in different words, stop and report before changing any matcher pattern.
