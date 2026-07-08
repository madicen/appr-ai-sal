# Evals harness (`internal/evals`)

The evals harness is the **quality flywheel**: a corpus of fixture PRs with
golden expectations that runs through the *real* review pipeline
(`review.EvalRun` — the same specialists, PR agents, deterministic gates,
arbiter, and vibe-coach a live review uses) against a pluggable provider, then
scores the result and writes a markdown report. It exists so prompt changes and
gate changes can't regress behaviour without a red signal.

## Running

```bash
make evals PROVIDER=ollama                 # score against a live backend
make evals PROVIDER=ollama OUT=report.md   # write the report to a file
make evals EVAL_FLAGS=--replay             # offline, deterministic (no model / no network)
make evals PROVIDER=ollama EVAL_FLAGS="--model llama3.1 --strictness strict"
make evals PROVIDER=ollama EVAL_FLAGS="--prompts-a . --prompts-b ./experiments/v2"
```

Provider selection reuses `aiconfig` exactly like a normal run: `PROVIDER=`
maps onto `APPR_AI_SAL_AI_PROVIDER`, and the `--model` / `--base-url` /
`--strictness` flags merge the same way the TUI's flags do. When no provider is
configured — or the selected one fails validation (no API key, CLI missing) —
the command **skips with exit 0**. It never depends on a live model.

`--replay` skips provider selection entirely and drives the pipeline from each
case's canned model output, so it always produces a report with no network. The
test suite and the nightly CI job use this path.

## Layout

```
internal/evals/
  corpus.go         # embeds + loads corpus/<id>/ fixtures
  expectations.go   # golden-truth schema + finding-matching rules
  score.go          # pure scorer: precision/recall/survival/anchor/first-try/cost
  replay.go         # deterministic offline ai.Provider (canned per-case output)
  runner.go         # projects a case into review.EvalInput; RunCase / RunCorpus / RunCorpusReplay
  report.go         # single-run + A/B markdown report rendering
  cli.go            # `appr-ai-sal evals` subcommand (make evals)
  corpus/<id>/      # one directory per case (see below)
```

## Corpus format

Each case is a directory under `internal/evals/corpus/<id>/`:

| File | Required | Purpose |
|---|---|---|
| `case.json` | yes | PR metadata + which synthesis stages to run (`CaseMeta`) |
| `diff.patch` | yes | the unified diff the pipeline reviews |
| `expectations.json` | yes | golden truth: must-appear / must-not-appear findings, verdict, JSON-first-try |
| `responses/<agent>.json` | for offline replay | canned raw model output per agent (`security`, `tech`, `checks`, `vibe-coach`, …) |
| `tech.md` | optional | technology-expert brief; presence turns the `tech` specialist on |
| `repo-context.md`, `lang.md`, `evidence.md` | optional | brief blocks injected into specialist prompts |
| `briefs/<specialist>.md` | optional | per-specialist repo-agent brief |

`case.json` (all fields except `id`/`target` are optional):

```json
{
  "id": "security-weak-hash",
  "title": "Add content checksum helper",
  "target": "security",
  "repo": "acme/uploader",
  "number": 101,
  "author": "octocat",
  "body": "Adds a Checksum helper.",
  "strictness": "balanced",
  "run_pr_agents": false,
  "run_witness": false,
  "run_arbiter": false,
  "run_vibe_coach": false
}
```

`expectations.json`:

```json
{
  "expected_verdict": "request_changes",
  "must_appear": [
    {"specialist": "security", "path": "db/user.go", "line": 9,
     "line_tolerance": 1, "pattern": "(?i)sql injection|parameteriz"}
  ],
  "must_not_appear": [
    {"specialist": "tech", "path": "infra/main.tf", "pattern": "(?i)tags"}
  ],
  "expect_json_first_try": {"security": true}
}
```

`responses/<agent>.json` uses the specialist output shape (`summary` +
`findings[]`, each with `path`, `line`, `severity`, `comment`, optional
`suggestion` and `anchor_excerpt`). The `vibe-coach` response uses the
verdict/prompts shape. Agents with no response file return a clean, empty pass.

## Scoring

For every agent on every case the scorer computes:

- **Recall** — of the labelled `must_appear` findings, how many were reported.
- **Precision** — reported findings that match a `must_appear` (true positive)
  vs. one that matches a `must_not_appear` scar (forbidden hit). Findings
  matching neither list are **unlabelled and ignored**, so the corpus doesn't
  have to enumerate every legal finding.
- **Suggestion-survival rate** — of the model's pre-gate inline suggestions,
  how many survive the deterministic gates as GitHub-postable one-click fixes.
  Tool-synthesized / tool-repaired suggestions don't count (they're the tool's
  fix, not the model's).
- **Anchor-hit rate** — of those, how many kept the model's original anchor
  line (no relocation by the excerpt gate).
- **JSON-parse-first-try rate** — did the model's output parse on the first
  attempt (evals run each agent once, no retries, to measure this honestly).
- **Token cost** — calls / input / output tokens / USD, via the review usage
  plumbing.

### Matching rules

An `ExpectFinding` matches a `review.Finding` when **all set fields** hold:
`specialist` (case-insensitive), `path` (`""` = any, including PR-wide), `line`
(`0` = any; `line_tolerance` widens to a `±N` window), and `pattern` (a
case-insensitive Go regexp against the comment, degrading to substring if the
regexp is malformed).

## The corpus (12 cases)

| Case | Target | What it exercises |
|---|---|---|
| `security-weak-hash` | security | seeded MD5 bug; suggestion survives + anchor-hit |
| `security-sql-injection` | security | seeded SQLi (prose only) + `request_changes` verdict via vibe-coach |
| `formatting-spacing` | formatting | legit `gofmt` fix survives; **scar:** snake_case-on-Go stripped by the naming gate |
| `design-deep-nesting` | design | deep-nesting smell (prose) |
| `testing-error-branch` | testing | actionable test-gap survives; **scar:** bare "missing tests" demoted by the actionability gate |
| `docs-missing-godoc` | docs | missing godoc; suggestion survives + anchor-hit |
| `tech-memory-units` | tech | k8s `717M`→`717Mi` fix survives |
| `tech-iac-s3-tags` | tech | **scar:** `tags` on `aws_s3_bucket_policy` stripped by the IaC schema gate |
| `pr-description-empty` | description | empty PR body flagged (PR-wide) |
| `pr-checks-failing` | checks | failing-check note anchored to a changed line |
| `pr-discussion-unresolved` | discussion | unresolved conversation ask (PR-wide) |
| `pr-scope-creep` | scope | unrelated dependency bump bundled with a feature (PR-wide) |

The three "scar" negatives are the documented false positives the deterministic
gates exist to kill: they appear in `must_not_appear` and the harness asserts
the gates strip/demote them below the strictness floor.

## Adding a case

1. `mkdir internal/evals/corpus/<id>/`.
2. Write `diff.patch` — keep it small; a fully-added new file (`@@ -0,0 +1,N @@`)
   makes line numbers easy. Every added line is one greater than the last.
3. Write `case.json` (set `target`, and the `run_*` flags for any synthesis
   stage you want, e.g. `run_vibe_coach` to score a verdict).
4. Add `responses/<agent>.json` for each agent you want to drive, using real
   line numbers and `anchor_excerpt` copied verbatim from the post-image line
   (whitespace is normalised on compare). If you want the `tech` specialist to
   run, add a `tech.md`.
5. Write `expectations.json` with the `must_appear` / `must_not_appear` findings
   and (optionally) `expected_verdict` and `expect_json_first_try`.
6. `go test ./internal/evals/` — `TestCorpusReplayScores` runs your case through
   the pipeline against the ReplayProvider and asserts recall, precision (no
   scar survives), verdict, and JSON-first-try. Iterate until green.

Because the tests replay canned output, they are fully deterministic and never
touch the network.
