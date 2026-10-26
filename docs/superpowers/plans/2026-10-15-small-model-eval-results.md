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

### Control baselines (2026-10-20)

Setup: `OLLAMA_CONTEXT_LENGTH=32768` (confirmed on serve). Server `/api/version` **0.23.0**. Full embedded corpus (22 cases). Strictness balanced. Explicit `--model`. Three reps each. Per-run markdown + `--metrics-json` under `reports/baselines/` (gitignored). Frozen snapshots:

- `docs/evals/baselines/qwen2.5-coder-3b-ctx32768-balanced-v1.json`
- `docs/evals/baselines/qwen2.5-coder-7b-ctx32768-balanced-v1.json`
- `docs/evals/baselines/qwen2.5-coder-14b-ctx32768-balanced-v1.json`

Wall clock (sequential): 3b ~18m/rep, 7b ~36m/rep, 14b ~96m/rep; total ~7h.

| Model | Rep | Unsupported-claim | Suggestion-breakage | Recall (original 12) | Tokens (in+out) |
|---|---|---|---|---|---|
| qwen2.5-coder:3b | r1 | 33.3% (1/3) | 100% (1/1) | 0% (0/11) | 852802 |
| qwen2.5-coder:3b | r2 | n/a (0/0) | n/a (0/0) | 0% (0/11) | 849343 |
| qwen2.5-coder:3b | r3 | 50% (1/2) | 66.7% (2/3) | 9.1% (1/11) | 852744 |
| qwen2.5-coder:7b | r1 | 0% (0/1) | 66.7% (2/3) | 0% (0/11) | 851010 |
| qwen2.5-coder:7b | r2 | 0% (0/2) | n/a (0/0) | 9.1% (1/11) | 848467 |
| qwen2.5-coder:7b | r3 | 0% (0/2) | 75% (3/4) | 0% (0/11) | 847831 |
| qwen2.5-coder:14b | r1 | 33.3% (1/3) | 66.7% (2/3) | 27.3% (3/11) | 858855 |
| qwen2.5-coder:14b | r2 | 0% (0/5) | 40% (2/5) | 18.2% (2/11) | 865661 |
| qwen2.5-coder:14b | r3 | 45.5% (5/11) | 33.3% (1/3) | 27.3% (3/11) | 862567 |

Means (n/a reps excluded from rate means):

| Model | Unsupported-claim | Suggestion-breakage | Recall (original 12) | Tokens |
|---|---|---|---|---|
| qwen2.5-coder:3b | 41.7% | 83.3% | 3.0% | 851630 |
| qwen2.5-coder:7b | 0.0% | 70.8% | 3.0% | 849103 |
| qwen2.5-coder:14b | 26.3% | 46.7% | 24.2% | 862361 |

### Treatment: grounding-v1 (2026-10-21)

Prompt treatment under `experiments/grounding-v1/` (embedded specialist/generator prompts + Evidence grounding section). Same provider/model/ctx/strictness/reps as control. Raw under `reports/treatments/grounding-v1/`. Gate score: `docs/evals/treatments/grounding-v1-ctx32768-balanced-v1.json`.

| Model | Unsupported-claim (mean Δ pp) | Breakage Δ pp | Recall orig-12 Δ pp | Token infl. % | Outcome |
|---|---|---|---|---|---|
| qwen2.5-coder:3b | 0.0 (CI not excl. 0) | +16.7 | −6.1 (improved) | +2.0 | `measured-no-uplift` |
| qwen2.5-coder:7b | −16.7 (worse; baseline was 0%) | −37.5 | −6.1 | +2.1 | `measured-no-uplift` |
| qwen2.5-coder:14b | +15.4 (≥10) but CI includes 0 | −30.0 | +12.1 (regressed) | +1.8 | `measured-no-uplift` |

**Overall outcome: `measured-no-uplift`.** Gate not retuned. 14b moved claim rate in the right direction on average but failed CI-exclude-zero and the recall guard.

### Treatment: inventory-v1 (2026-10-22)

Eval-only treatment (`APPR_AI_SAL_EVAL_TREATMENT=inventory-v1`): inject worktree file + identifier inventory into every specialist's repo-context brief. Harness: `internal/evals/inventory.go`; EvalRun now threads `RepoContext`. Raw under `reports/treatments/inventory-v1/`. Gate score: `docs/evals/treatments/inventory-v1-ctx32768-balanced-v1.json`.

| Model | Unsupported-claim (mean Δ pp) | Breakage Δ pp | Recall orig-12 Δ pp | Token infl. % | Outcome |
|---|---|---|---|---|---|
| qwen2.5-coder:3b | −3.3 (worse) | +33.3 | +3.0 (regressed) | +2.2 | `measured-no-uplift` |
| qwen2.5-coder:7b | −50 (worse; baseline 0%) | −66.7 | −6.1 | +2.3 | `measured-no-uplift` |
| qwen2.5-coder:14b | +19.6 (≥10) but CI includes 0 | −20.5 | +3.0 (regressed) | +2.0 | `measured-no-uplift` |

**Overall outcome: `measured-no-uplift`.** Gate not retuned. Inventory moved 14b claim rate more than grounding-v1 on average but still failed CI-exclude-zero and the recall guard.

### Treatment: claim-strip-v1 (2026-10-25)

Hard / deterministic eval-only treatment (`APPR_AI_SAL_EVAL_TREATMENT=claim-strip-v1`): after `EvalRun`, drop findings with ≥1 worktree-unresolvable claim ref. Harness: `internal/evals/claim_strip.go`. Scripts: `experiments/claim-strip-v1/`. Gate score pending live 3×3 corpus + held-out grids (vs control baselines + held-out v2 floor).

## Held-out

Scaffolding + labels + worktrees landed in #55 (2026-10-23):

- `docs/evals/held-out/README.md` + `--corpus-dir` on `appr-ai-sal evals`
- `./scripts/heldout-import.sh`; Go worktree fixtures as `*.go.txt`
- Cases (labelled): `pr-42-…`, `pr-43-…`, `pr-45-…` (design must_appear + scar must_not_appear)

### Control baselines v1 (2026-10-24) — superseded

Pre-relabel (fix-PR framing). All models **0/3** full recall. Superseded by broken-parent relabel + v2 freeze below. Raw kept under `reports/held-out/pre-relabel/` (gitignored).

### Zero-recall audit (2026-10-24, 14b `--dump-raw`)

One live dump under `reports/held-out/audit-14b/` (gitignored). Design raw on all three cases was exactly:

```json
{ "summary": "The design of this diff looks sound.", "findings": [] }
```

That string is the empty-success template in `internal/review/prompts/design.md`. Post-gate `findings` were `[]` — **no candidate comments for matchers to miss.** Verdict: **not a matcher issue.**

| Case | Label intent | Audit read |
|---|---|---|
| pr-42 | Residual: `RerunContext` not persisted / restore disables re-run | Fair lingering design note; model still empty → **model empty-bias / ceiling on residuals** |
| pr-43 | `bodyH` vs `host.Height()` overflow | Diff already fixes it; empty design is consistent with the prompt → **label vs fixed-diff mismatch** |
| pr-45 | List wheel must not Y-hit-test | Diff already removes the Y bounds check → **label vs fixed-diff mismatch** |

Corpus audit dumps (`reports/audit/`) show the same design empty-bias (many `looks sound` / `findings: []`). Formatting sometimes emitted `info` nits that balanced strictness dropped — unrelated to design recall.

**Do not** retune matchers or the Phase 5 gate. **Do not** invent another soft prompt. Claim stripper would not move this recall.

**Next (done 2026-10-24):** relabel held-out before any treatment work.

### Relabel: broken-parent for #43/#45 (2026-10-24)

- **pr-43 / pr-45:** `diff.patch` = `git diff <fix-head> <buggy-base>` (revert-of-fix); `worktree/` = files at buggy base (`*.go.txt`). Expectations: design (reintroduced bug) + formatting (deleted regression test).
- **pr-42:** unchanged feature-residual (`RerunContext` not persisted).
- Prior control snapshots (`docs/evals/held-out/*-ctx32768-balanced-v1.json`) marked `superseded: true` — re-run 3×3 after this relabel before comparing treatments.

See `docs/evals/held-out/README.md` framing section.

**14b smoke** (`reports/held-out/relabel-smoke/`, 1 rep): design still empty-template on all three (ceiling). Formatting **did** flag deletion of `TestControlsProfileDropdownOpenFitsTerminalHeight` on #43 → offline match **1/5** must_appear after pointing that label at `formatting`. #45 test deletion still missed.

### Control baselines v2 (2026-10-25, post-relabel)

Setup: same as v1 (`OLLAMA_CONTEXT_LENGTH=32768`, Ollama **0.23.0**, balanced), after broken-parent relabel (5 must_appear rows). Frozen snapshots (replaced v1 files):

- `docs/evals/held-out/qwen2.5-coder-3b-ctx32768-balanced-v1.json` (`label: post-relabel-broken-parent`)
- `docs/evals/held-out/qwen2.5-coder-7b-ctx32768-balanced-v1.json`
- `docs/evals/held-out/qwen2.5-coder-14b-ctx32768-balanced-v1.json`

Wall clock: 3b ~6m/rep, 7b ~12m/rep, 14b ~27–29m/rep; total ~2.3h.

| Model | Rep | Unsupported-claim | Suggestion-breakage | Recall (full) | Tokens (in+out) |
|---|---|---|---|---|---|
| qwen2.5-coder:3b | r1 | n/a (0/0) | n/a (0/0) | 0% (0/5) | 229892 |
| qwen2.5-coder:3b | r2 | n/a (0/0) | n/a (0/0) | 0% (0/5) | 230116 |
| qwen2.5-coder:3b | r3 | n/a (0/0) | n/a (0/0) | 20% (1/5) | 230958 |
| qwen2.5-coder:7b | r1 | 0% (0/1) | 100% (1/1) | 0% (0/5) | 230923 |
| qwen2.5-coder:7b | r2 | 0% (0/2) | n/a (0/0) | 0% (0/5) | 230370 |
| qwen2.5-coder:7b | r3 | n/a (0/0) | n/a (0/0) | 0% (0/5) | 231019 |
| qwen2.5-coder:14b | r1 | 0% (0/2) | n/a (0/0) | 20% (1/5) | 250832 |
| qwen2.5-coder:14b | r2 | 50% (1/2) | n/a (0/0) | 20% (1/5) | 236120 |
| qwen2.5-coder:14b | r3 | 0% (0/1) | n/a (0/0) | 20% (1/5) | 236615 |

Means (n/a reps excluded from rate means):

| Model | Unsupported-claim | Suggestion-breakage | Recall (full) | Tokens |
|---|---|---|---|---|
| qwen2.5-coder:3b | n/a | n/a | 6.7% | 230322 |
| qwen2.5-coder:7b | 0.0% | 100% (1 defined) | 0.0% | 230770 |
| qwen2.5-coder:14b | 16.7% | n/a | 20.0% | 241189 |

**Readout:** relabel gave a non-zero held-out floor (14b stable **1/5** = formatting hit on #43 test deletion). Design must_appear still never hits. Gate not retuned. Soft treatments remain `measured-no-uplift` on corpus; held-out does not certify them. Optional next: hard/deterministic treatment measured against this v2 floor — not another soft prompt.

