# Eval Phase 3–4: hallucination cases + claim/compile metrics

## Goal

Extend the eval corpus so unsupported claims and bad suggestions are measurable, and add two Phase 4 rates (unsupported-claim, suggestion-compile) that treatments must move. Stack on the existing worktree-fixture harness (PR #44); do not change production review behaviour or held-out data.

## Decisions (locked)

| Topic | Choice |
| --- | --- |
| Base | Stack on `eval/worktree-fixtures` (PR #44). Leave #44 harness-only. |
| PR shape | Child bookmark `eval/hallucination-cases` → stacked PR for cases + Phase 4 metrics. |
| Scope | Phase 3 (~10 new cases) + Phase 4 score/report wiring. Not Phase 5 gate. |
| Compile toolchains | Go, Python, TypeScript, Terraform (`gofmt -e`, `python -m py_compile`, `tsc --noEmit`, `terraform validate`). |
| Missing tools | Soft-skip locally (`tool_missing`, exclude from denominator). CI (`evals.yml`) installs `tsc` + Terraform so nightly/manual runs compute full rates. |
| Existing 12 cases | Untouched expectations/gates; replay report updated only for corpus growth + new metric columns. |
| Live models | Not run in this PR. |

## Out of scope

- Phase 5 pre-registered treatment gate.
- Phase 2b findings.csv analysis / matcher pattern edits.
- `--case` CLI filter.
- Held-out PR set.
- Merging or rewriting PR #44’s harness commit.
- Requiring `tsc`/`terraform` on main `ci.yml` (unit tests soft-skip).

## Architecture

```
corpus/<id>/
  case.json, diff.patch, expectations.json, responses/, worktree/

RunCase
  materializeWorktree(c.WorktreeFiles) → tempDir
  EvalRun(..., Worktree: tempDir)
  ScoreCase(c, obs, tempDir)   // tempDir still alive
    → recall/precision/survival (unchanged)
    → unsupported-claim rate (worktree resolve)
    → suggestion-compile rate (apply + language check)
  cleanup tempDir
```

PR #44 already loads `worktree/` into `Case.WorktreeFiles` and materializes it for gates. This work passes that path into scoring and adds fixtures that exercise claim/compile checks.

## Corpus cases (~10)

Each new case includes a `worktree/` tree (post-change file contents claim checks read).

| Count | Category | Scoring intent |
| --- | --- | --- |
| 2 | Invented identifier / path in canned comment | `must_not_appear` on invented name; unsupported-claim numerator |
| 2 | Correct cross-file identifier | Symbol only in another worktree file; `must_appear`; claim resolves |
| 2 | Suggestion breaks when applied | Canned surviving suggestion; compile rate denominator, fail numerator |
| 3 | Non-Go (Python, TypeScript, Terraform) | Language-specific worktree + compile check; Go expander empty is fine |
| 1 | Large diff near budget | Noise first, seeded issue at end; canned response documents hit or intentional miss |

Replay canned outputs fix known expected rates. Refresh `docs/evals-report.md` after corpus growth.

## Phase 4 metrics

### Unsupported-claim rate

- Input: post-gate findings; case worktree directory.
- Extract from each comment: backtick identifiers, file-path-like tokens, `path:line` refs.
- Resolve against worktree: file exists; identifier appears in some file under the tree; line in range for `path:line`.
- Finding with ≥1 unresolvable ref → unsupported.
- Rate = unsupported / findings_with_≥1_extracted_ref. No extractable refs → exclude from denominator. No worktree → `n/a`.

### Suggestion-compile rate

- Input: surviving inline suggestions; worktree file at finding path.
- Copy file (or module root for Terraform), apply suggestion text, run language check.
- Missing binary → `tool_missing`, exclude from denominator.
- Apply/subprocess failure → compile-fail (counts in denominator).
- Rate = compile_ok / (attempted − tool_missing). No worktree/path → skip.

### Report

Add columns (and A/B deltas) for both rates on specialist aggregate and corpus totals. Existing recall/precision/survival/anchor/JSON columns stay.

## Testing

- Unit: claim resolver (invented vs present, path:line OOB).
- Unit: compile helpers with tiny temps; soft-skip when `tsc`/`terraform` absent.
- Replay: ≥1 new case per category with pinned rate numerators/denominators.
- `go test -race ./internal/evals/...`, `make evals EVAL_FLAGS=--replay` matches updated `docs/evals-report.md`.

## CI

- `.github/workflows/evals.yml`: install Node + TypeScript (`tsc`) and Terraform before `make evals`.
- `.github/workflows/ci.yml`: unchanged; hermetic unit tests soft-skip missing tools.

## Error handling

- Materialize failure: case score error title (existing #44 behaviour); no panic.
- Claim/compile helper errors: count as unsupported or compile-fail for that item; suite continues.
- Soft-skip only for absent tool binaries, never for bad suggestions.

## Success criteria

- Child PR stacks cleanly on #44.
- ~10 new cases load in corpus; replay green with updated report.
- Unsupported-claim and suggestion-compile appear in report; rates match unit/replay pins.
- Local run without `tsc`/`terraform` stays green; CI evals job has both tools.
