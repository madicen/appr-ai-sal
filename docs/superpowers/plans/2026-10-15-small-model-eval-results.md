# Small-model eval results

Recorded 2026-10-15. Phase 2 used context length 32768 (`OLLAMA_CONTEXT_LENGTH`).

## Phase 0

Ollama is installed. Server is the Ollama.app binary at version 0.23.0 (`/api/version`). The Homebrew client is 0.40.4 and prints `Warning: client version is 0.40.4` against that server. `OLLAMA_CONTEXT_LENGTH` is unset. The server was not restarted and no Modelfile was edited.

`ollama show <model> --modelfile` has no `PARAMETER num_ctx` on any local model. The context length below is the architecture value `ollama show` prints, not a runtime override.

| Model | `PARAMETER num_ctx` | Architecture context length |
|---|---|---|
| qwen3:8b | absent | 40960 |
| qwen2.5-coder:3b | absent | 32768 |
| qwen2.5-coder:7b | absent | 32768 |
| qwen2.5-coder:14b | absent | 32768 |
| qwen2.5:7b-instruct | absent | 32768 |
| qwen2.5:14b-instruct-q5_K_M | absent | 32768 |
| qwen2.5:32b-instruct | absent | 32768 |
| qwen2.5:32b-instruct-q4_K_M | absent | 32768 |
| phi4-mini:latest | absent | 131072 |

`qwen2.5:32b-instruct` and `qwen2.5:32b-instruct-q4_K_M` share blob id `9f13ba1299af`.

Largest corpus case by `diff.patch` size is `pr-scope-creep` (337 bytes). Every corpus diff is under 400 bytes. That case was run once through `evals.RunCase` against `qwen2.5-coder:7b`, which was already loaded (no pull). The harness reported 9 calls, 44984 input tokens, and 263 output tokens, exit 0. During that run `~/.ollama/logs/server.log` recorded `llama_context: n_ctx = 32768` and nine `POST /v1/chat/completions` responses with status 200. A search of that log for `truncat` found no matches. The server log does not print a per-request prompt-token line; the 44984 figure is the harness usage total.

No context size was selected for later phases.

## Phase 1

`make evals EVAL_FLAGS=--replay OUT=/tmp/phase1-replay.md` exited 0. `diff -q` against `docs/evals-report.md` matched. The committed report was not modified.

`make evals PROVIDER=ollama OUT=/tmp/phase1-ollama.md` exited 0 and wrote a 1722-byte report (`ollama · gemini-3.1-flash-lite`, 12 cases, 79 calls, 0 input tokens, 0 output tokens, JSON first-try 0%). The active profile is Gemini, so `PROVIDER=ollama` switches the provider and keeps model `gemini-3.1-flash-lite`. The Ollama server answered those calls with HTTP 404. Nothing was pulled. The skip path did not run: Ollama validation does not check that the server is up or that the model exists, and the server was up.

## Phase 2

Setup: `OLLAMA_CONTEXT_LENGTH` was unset, so it was set to `32768` via `launchctl setenv` and Ollama.app was restarted. After restart, the serve process env includes `OLLAMA_CONTEXT_LENGTH=32768`. Server `/api/version` is **0.23.0**. No models were pulled.

`ollama show` for the three models (no `PARAMETER num_ctx` in any Modelfile):

| Model | parameters | context length | quantization |
|---|---|---|---|
| qwen2.5-coder:3b | 3.1B | 32768 | Q4_K_M |
| qwen2.5-coder:7b | 7.6B | 32768 | Q4_K_M |
| qwen2.5-coder:14b | 14.8B | 32768 | Q4_K_M |

Strictness: balanced. Each run used an explicit `--model` flag. Corpus-level rates below sum specialist numerators/denominators from each report (n/a cells excluded). All nine runs were **valid**: every `POST /v1/chat/completions` in the Ollama server log window was HTTP 200 (no 404 / non-200). Raw reports are under `reports/` and are not committed.

These metrics do **not** measure unsupported-claim rate or suggestion-compile rate; Phase 4 is not implemented.

| Model | Rep | Recall | Precision | Suggestion survival | Anchor-hit | JSON first-try | Input tokens | Report |
|---|---|---|---|---|---|---|---|---|
| qwen2.5-coder:3b | r1 | 18.2% (2/11) | 100% (2/2) | 0% (0/3) | 0% (0/3) | 100% (78/78) | 479000 | `reports/qwen2.5-coder:3b-r1.md` |
| qwen2.5-coder:3b | r2 | 0% (0/11) | n/a | 100% (2/2) | 50% (1/2) | 100% (78/78) | 479000 | `reports/qwen2.5-coder:3b-r2.md` |
| qwen2.5-coder:3b | r3 | 0% (0/11) | n/a | 0% (0/2) | 0% (0/2) | 100% (78/78) | 478000 | `reports/qwen2.5-coder:3b-r3.md` |
| qwen2.5-coder:7b | r1 | 18.2% (2/11) | 100% (2/2) | 0% (0/2) | 0% (0/2) | 100% (78/78) | 478000 | `reports/qwen2.5-coder:7b-r1.md` |
| qwen2.5-coder:7b | r2 | 18.2% (2/11) | 100% (2/2) | 50% (1/2) | 50% (1/2) | 100% (78/78) | 478000 | `reports/qwen2.5-coder:7b-r2.md` |
| qwen2.5-coder:7b | r3 | 9.1% (1/11) | 100% (1/1) | 75% (3/4) | 50% (2/4) | 100% (78/78) | 479000 | `reports/qwen2.5-coder:7b-r3.md` |
| qwen2.5-coder:14b | r1 | 18.2% (2/11) | 100% (2/2) | 75% (12/16) | 62.5% (10/16) | 100% (78/78) | 483000 | `reports/qwen2.5-coder:14b-r1.md` |
| qwen2.5-coder:14b | r2 | 36.4% (4/11) | 80% (4/5) | 72.7% (8/11) | 36.4% (4/11) | 100% (78/78) | 481000 | `reports/qwen2.5-coder:14b-r2.md` |
| qwen2.5-coder:14b | r3 | 18.2% (2/11) | 100% (2/2) | 81.8% (9/11) | 54.5% (6/11) | 100% (78/78) | 482000 | `reports/qwen2.5-coder:14b-r3.md` |

### Per-model mean and min–max

Rates are the mean of the three per-run percentages (precision for 3b uses the one run that emitted scored findings).

| Model | Recall | Precision | Suggestion survival | Anchor-hit | JSON first-try | Input tokens |
|---|---|---|---|---|---|---|
| qwen2.5-coder:3b | mean 6.1% (0–18.2) | mean 100% (only r1; r2–r3 n/a) | mean 33.3% (0–100) | mean 16.7% (0–50) | mean 100% (100–100) | mean 478667 (478000–479000) |
| qwen2.5-coder:7b | mean 15.2% (9.1–18.2) | mean 100% (100–100) | mean 41.7% (0–75) | mean 33.3% (0–50) | mean 100% (100–100) | mean 478333 (478000–479000) |
| qwen2.5-coder:14b | mean 24.2% (18.2–36.4) | mean 93.3% (80–100) | mean 76.5% (72.7–81.8) | mean 51.1% (36.4–62.5) | mean 100% (100–100) | mean 482000 (481000–483000) |

## Phase 2b

Raw-output audit of the three seeded cases (`security-weak-hash`, `testing-error-branch`, `docs-missing-godoc`) after full-corpus dumps with `--dump-raw`. One repetition each at `OLLAMA_CONTEXT_LENGTH=32768`. Auditor read only those three case directories. Diff-only identifier checks (`in diff` vs `not in diff (unverified)`). Row-level notes: untracked `reports/audit/findings.csv`.

**Setup note:** Before the dump re-run, Ollama.app was restarted so `OLLAMA_CONTEXT_LENGTH=32768` applied. Server `/api/version` after restart reported **0.40.4** (Phase 2 had recorded **0.23.0**). Context env on the serve process was confirmed `32768`. All three dump runs exited 0 with 78 JSON files each (12 cases × agents). Wall clock: 3b ~608s, 7b ~1199s, 14b ~2647s.

### Counts (findings in the three audited case dirs only)

| Model | Findings | Seeded-issue yes | Seeded-issue partly | Seeded-issue no | Unsupported refs (`not in diff`) | Suggestions given | Compile yes / no / not tried |
|---|---|---|---|---|---|---|---|
| qwen2.5-coder:3b | 1 | 1 | 0 | 0 | 0 | 1 | 0 / 0 / 1 |
| qwen2.5-coder:7b | 1 | 1 | 0 | 0 | 0 | 0 | — |
| qwen2.5-coder:14b | 5 | 2 | 0 | 3 | 0 | 5 | 3 / 2 / 0 |

Empty case×model (no findings from any agent in that case dir): 3b×security, 3b×testing, 7b×security, 7b×docs — treated as **model misses** for those seeded issues.

### Must-appear vs wording (matcher review; expectations not edited)

| Model | Case | What happened |
|---|---|---|
| 3b | docs-missing-godoc | Docs agent described missing doc comment (**yes**). Line was 0 (not on a diff line). |
| 7b | testing-error-branch | Testing agent described a zero-divisor test gap in plain English (**semantic yes**), but the expectation pattern `(?i)unit test.*divide\|b == 0\|errBadInput` does **not** match “The Divide function lacks a test…”. Clear **matcher wording miss**. |
| 14b | security-weak-hash | **Design** (not security) named MD5 / weak hash with words that would match `(?i)md5\|sha256\|collision`. Security agent silent. Issue described; wrong specialist for must-appear. |
| 14b | testing-error-branch | Testing agent silent. Design claimed `errBadInput` is undefined (identifier is **in diff**; claim is still wrong). Docs asked for a godoc on `Divide` (not the seeded test gap). |
| 14b | docs-missing-godoc | Docs agent hit with “doc comment” (**yes**, correct specialist). |

### Stop criteria

14b’s must-appear outcomes are **not** “mostly described in different words.” The dominant failure modes are silence on the target specialist, wrong-specialist hits, and one clear wording/pattern miss on 7b testing (`unit test.*divide` order). **Stop before changing any matcher pattern.** Revisit the testing pattern and specialist routing only after Phase 3 fixtures / Phase 4 metrics land.

## Phase 5

Pre-registered plugin treatment gate written 2026-10-19 into:

- `docs/evals/plugin-treatment-gate.json` (immutable machine copy)
- `docs/evals/plugin-treatment-gate.md` (human summary)

Thresholds (locked): unsupported-claim ↓ ≥10pp with CI excluding zero across 3 reps; suggestion-breakage must not rise; original-12 recall regression ≤2pp; token inflation ≤25%. Outcomes: `measured-helpful` | `measured-no-uplift` | `pending`.

**Current outcome: `pending`.** No control baseline snapshot under `docs/evals/baselines/` yet (Phase 2 grid lacked Phase 4 rates). Next: record three-rep control baselines per model at ctx 32768, then run treatments without editing the gate.

