# Eval Hallucination Cases + Phase 4 Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stack ~10 hallucination corpus cases and Phase 4 unsupported-claim / suggestion-compile metrics on `eval/worktree-fixtures` (PR #44).

**Architecture:** Materialized `worktree/` is passed into scoring. Claim resolution and suggestion compile checks run after gates. Soft-skip missing `tsc`/`terraform` locally; install them in `evals.yml`.

**Tech Stack:** Go, embedded corpus FS, gofmt / python3 / tsc / terraform CLI.

**Spec:** `docs/superpowers/specs/2026-10-16-eval-hallucination-cases-design.md`

## Global Constraints

- jj only for version control; bookmark `eval/hallucination-cases` stacked on `eval/worktree-fixtures`
- Do not edit existing 12 cases' expectations or gates
- Do not touch held-out data or Phase 5
- Soft-skip missing compile tools; never fail the suite for absence
- No live model runs in this PR

---

### Task 1: Unsupported-claim resolver (TDD)

**Files:**
- Create: `internal/evals/claims.go`
- Create: `internal/evals/claims_test.go`
- Modify: `internal/evals/score.go` (fields + ScoreCase worktree param)
- Modify: `internal/evals/runner.go` (pass worktree dir into ScoreCase)

**Interfaces:**
- Produces: `extractClaimRefs(comment string) []claimRef`, `resolveClaims(worktree string, findings []review.Finding) (unsupported, checked int)`, fields `UnsupportedClaims`, `ClaimsChecked` on `SpecialistScore`

- [ ] **Step 1:** Write failing tests for extract + resolve (invented id, present id, path:line OOB, no worktree → n/a)
- [ ] **Step 2:** Implement claims.go; wire into scoreCase(c, obs, worktree)
- [ ] **Step 3:** `go test ./internal/evals/ -run 'Claim|Unsupported' -count=1`
- [ ] **Step 4:** jj describe chunk

### Task 2: Suggestion-compile checker (TDD)

**Files:**
- Create: `internal/evals/compile.go`
- Create: `internal/evals/compile_test.go`
- Modify: `internal/evals/score.go` (`CompileOK`, `CompileAttempted`, `CompileSkipped`)

**Interfaces:**
- Produces: `checkSuggestionCompile(worktree string, f review.Finding) compileResult` with statuses ok/fail/skip_tool/skip_no_file

- [ ] **Step 1:** Failing tests: good Go suggestion compiles; bad Go fails; missing tsc soft-skips
- [ ] **Step 2:** Implement apply-line + language dispatch
- [ ] **Step 3:** Wire into scoreCase; report Ratio helpers
- [ ] **Step 4:** jj describe chunk

### Task 3: Report + aggregate columns

**Files:**
- Modify: `internal/evals/score.go` (SpecialistAggregate)
- Modify: `internal/evals/report.go`
- Modify: `internal/evals/evals_test.go` (TestRenderReport)

- [ ] **Step 1:** Add Unsupported-claim and Suggestion-compile columns + A/B metrics
- [ ] **Step 2:** Update TestRenderReport expectations
- [ ] **Step 3:** `go test ./internal/evals/ -count=1`

### Task 4: Corpus cases (~10)

**Files:** Create under `internal/evals/corpus/`:
- `hallucination-invented-ident`, `hallucination-invented-path`
- `crossfile-ident-security`, `crossfile-ident-docs`
- `suggestion-break-go`, `suggestion-break-python`
- `lang-python-typo`, `lang-typescript-any`, `lang-terraform-open-sg`
- `truncation-late-seed`

Each: `case.json`, `diff.patch`, `expectations.json`, `responses/<target>.json`, `worktree/**`

- [ ] **Step 1:** Add cases with canned responses matching expectations + claim/compile pins
- [ ] **Step 2:** `go test ./internal/evals/ -count=1` and `make evals EVAL_FLAGS=--replay`
- [ ] **Step 3:** Refresh `docs/evals-report.md` from replay output

### Task 5: CI tool install

**Files:**
- Modify: `.github/workflows/evals.yml`

- [ ] **Step 1:** Before Run evals: setup-node + npm i -g typescript; install terraform
- [ ] **Step 2:** Confirm ci.yml unchanged

### Task 6: Bookmark, push, PR

- [ ] **Step 1:** `jj bookmark set eval/hallucination-cases -r @`
- [ ] **Step 2:** `jj git push --bookmark eval/hallucination-cases --allow-new`
- [ ] **Step 3:** `gh pr create --base eval/worktree-fixtures --head eval/hallucination-cases`
