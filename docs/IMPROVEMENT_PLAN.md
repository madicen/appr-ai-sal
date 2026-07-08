# appr-ai-sal — Master Improvement Plan

> **Status (2026-07-08):** Phases 0–6 and deferred Q3/F5 follow-ups are **complete**. Remaining optional items: side-by-side diff view, Q3.4 TUI confidence badges, and library adoptions `go-gitdiff` / `cenkalti/backoff` (larger refactors). See `docs/RELEASE_NOTES_v0.3.0.md` and `docs/evals-report.md`.

> **Audience:** an AI engineer-agent (Claude Opus-class) executing this plan phase by phase, plus human maintainers reviewing the direction.
> **Basis:** a four-track deep audit (review engine + prompts, whole-repo architecture/duplication, AI-provider + gh layers, TUI layer) of the codebase as of 2026-07-06 (~52k LOC Go, 218 files).
> **Goal:** make appr-ai-sal the best terminal PR-review tool available — measurably better review quality, dramatically less duplicated code, and a workflow reviewers prefer over the GitHub web UI.

---

## How to execute this plan

Rules for the executing agent:

1. **Work phase by phase, workstream by workstream.** Each workstream (e.g. `F2`) is designed to be one reviewable PR (or a small stack). Do not combine unrelated workstreams in one change.
2. **Phase 0 lands first, alone.** Everything after it relies on CI existing.
3. **Never regress the deterministic-gate philosophy.** The engine's core differentiator is "never trust the model's line numbers, patches, or JSON — verify deterministically, repair when possible, disclose when repaired." Every change to `internal/review` must preserve or strengthen the gates in `agents.go` (`validateAndPruneSuggestions`, `validateAnchorKind`, `validateAnchorExcerpt`, `validateActionability`, naming-convention gate, IaC gate).
4. **Prompts and code are a paired system.** Several prompts describe the deterministic post-processors to the model (e.g. `testing.md`'s bare-deficiency demotion). When you change a gate, update the prompt that discloses it, and vice versa. When you change an output contract, update every parser and every prompt that references the schema.
5. **Tests:** every workstream lists acceptance criteria. `go build ./... && go vet ./... && go test -race ./...` must pass at every PR boundary. New packages need tests. Behavior-preserving refactors need before/after test evidence (existing tests unmodified and green).
6. **Verify current-state claims before editing.** File/line references below were accurate at audit time; re-grep before relying on them.
7. **Update `README.md` and `docs/` whenever behavior or config surface changes.**

Dependency graph (phases): `0 → 1 → {2, 3} → 4`; `5` depends on 1 only; `6` depends on 1 (F1 specifically). Within a phase, workstreams are independent unless noted.

---

## Current-state assessment (summary)

Strengths to preserve:

- The **deterministic validation pipeline** after every model call (9 gates incl. anchor-excerpt verification with unique-match relocation, suggestion synthesis/repair with disclosure lines) — better than most commercial review tools.
- **Fail-open staging**: evidence, briefs, witness, and arbiter all degrade without killing a run.
- **Scar-tissue-calibrated prompts**: lane-scoping, "empty result is correct" framing, prompt/gate pairings (testing/docs demotion disclosure, lang-convention enum matched to `convention_gate.go`).
- The **demo mode + VHS** infrastructure (reproducible offline; ideal test fixture).
- Clean, acyclic package dependency graph; no layering violations; essentially zero TODO/FIXME debt.

Structural debts (the "big five"):

1. **No CI at all** — the only workflow is manual release. Nothing guards `main`.
2. **God files/packages**: `internal/review/types.go` (1,741 lines, ≥6 concerns), `internal/review` (14.5k LOC), `internal/tui/model` (10k LOC, 440-line `Update` switch, no tab interface).
3. **Triplicated agent subsystem**: `internal/review/{repoagents,techagents,langagents}` re-implement store/freshness/prompt-override/generate (~1k duplicated LOC); `configDir()` cloned 5×; `extractJSONObject` byte-identical 2×; `CompleteFunc` typedef 6×.
4. **No token budgeting, no cost telemetry, no prompt evals**: the entire diff is inlined uncapped into up to ~25 LLM calls per run; usage fields already present in every provider response are discarded; prompt changes are flown blind.
5. **Contract drift bugs in the pipeline**: the vibe-coach `finding_refs` specialist enum omits `tech` and all PR agents; the arbiter roster omits `tech`; dedupe lane priority lets `formatting` swallow `security` findings out from under the arbiter's never-suppress-security guard.

---

## Phase 0 — Safety net (land first, small, independent)

### 0.1 CI workflow

Create `.github/workflows/ci.yml`: on push/PR → `go build ./...`, `go vet ./...`, `go test -race -cover ./...` on ubuntu-latest + macos-latest, plus `golangci-lint` and `govulncheck`. Add `.golangci.yml` (start modest: govet, staticcheck, unused, errcheck, ineffassign, misspell; grow later). Add Makefile targets `lint`, `test-race`, `cover`.

*Acceptance:* CI green on main; branch protection can require it; `make lint` runs locally.

### 0.2 Version stamping + `--version`

Add `var version = "dev"` in `cmd/appr-ai-sal/main.go`, `-version` flag printing it, and goreleaser `ldflags` injection (`-X main.version={{.Version}}`) in `.goreleaser.yml`.

*Acceptance:* `appr-ai-sal -version` prints the release tag on a goreleaser build.

### 0.3 Structured logging

Adopt stdlib `log/slog` writing to a file (`$XDG_STATE_HOME/appr-ai-sal/log/` or cache dir) — TUI apps cannot log to stderr. Env `APPR_AI_SAL_LOG_LEVEL`. Instrument: every LLM call (provider, model, stage, duration, retry count), every `gh` invocation (args, duration, exit), pipeline stage transitions, and post attempts. Redact API keys.

*Acceptance:* a failed review run is diagnosable from the log file alone; keys never appear in logs.

### 0.4 Quick correctness fixes (each tiny; may be one PR)

1. **Vibe contract enum** (`internal/review/agents.go`, `vibeCoachOutputContract`): extend `finding_refs.specialist` enum to include `tech` and the PR agents (`description`, `checks`, `discussion`, `scope`). Without this, prompts referencing tech/PR-agent findings mis-attribute refs, breaking `isAuthorPromptAlive` coverage tracking and causing duplicate fallback prompts.
2. **Arbiter roster** (`internal/review/prompts/repo-arbiter.md` line ~3): add `tech` to the listed specialist roster; fix the internal one-rank vs multi-rank demote contradiction (the code allows multi-rank; make the prose consistent).
3. **Stale digest heading** (`internal/review/repo_experts.go`, `buildSpecialistDigestForRepoExperts`): rename the `"## Specialist + vibe digest"` heading — the arbiter runs *before* vibe-coach; the heading promises content that never exists.
4. **Dedupe lane priority** (`internal/review/finding_dedupe.go`, `specialistLanePriority`): on near-duplicate merge, keep the max severity and the most-protected lane — security must never lose a dedupe to formatting/design, or the arbiter's never-suppress-security guard is bypassed.
5. **Retry the arbiter and witness**: wrap `RunRepoArbiter` and the convention-witness call (`internal/review/runner.go`) in `stageWithRetry` like every other stage (`isRetryableStageError` already lists `"parse repo arbiter"` — currently dead code); include a raw-output excerpt in `parseRepoArbiterJSON` errors; give both parsers the full sanitize ladder (see F2).
6. **Severity normalization at parse time**: unknown severity strings are currently coerced only during filtering but render verbatim in the body (`types.go` `RenderBody` path). Normalize when parsing specialist JSON.
7. **Gemini key in query string** (`internal/review/complete.go`, `completeGemini`): move the API key from `?key=` to the `x-goog-api-key` header.
8. **Worktree GC** (`internal/review/runner.go`, `prepareWorktree`): implement the purge the marker file promises — at startup delete `~/.cache/appr-ai-sal/worktrees/*` dirs bearing `.appr-ai-sal-worktree` older than N days (default 7) or beyond keep-last-K per PR.
9. **Cache viewer login** per session — `graphqlReviewQuery` already returns `viewer{login}` but `ListPRs` discards it and `GetPR` re-execs `gh` for it every call (`internal/gh/gh.go`).
10. **security.md critical gap**: `SeverityCritical` exists in the schema and the `critical_only` strictness floor depends on it, but `security.md` caps guidance at `error` — under `critical_only`, security effectively never fires. Define `critical` for security (RCE, auth bypass, secret exfiltration) in the prompt.
11. **Repair-pass telemetry**: `repairMissingSuggestions` fires a hidden second LLM call per specialist; emit Progress events (fired/succeeded counts).
12. **repoconfig bool-presence detection** (`internal/repoconfig/config.go` `Load`): replace the twelve `bytes.Contains(b, "\"field\"")` raw-JSON scans (lines ~125–172) with `*bool` fields + normalization.

*Acceptance:* each fix has a focused test (e.g. a dedupe test where security + formatting collide and security survives; an arbiter parse-failure test that recovers via retry).

---

## Phase 1 — Foundations: kill duplication, create the seams

These refactors are behavior-preserving and unblock everything later. Order within phase: F1 → F2/F3/F4 (parallel) → F5 → F6/F7.

### F1 — `internal/ai`: a real provider interface

Today inference is one free function (`review.Complete`) switching on provider, with the `CompleteFunc` typedef re-declared 5× (exported in `repoagents/generate.go`, `techagents/generate.go`, `langagents`, `conventionwitness/agent.go`; unexported `completeFunc` in `suggestion_repair.go`) to dodge import cycles, and provider-specific branches leaking into prompt construction (`augmentPromptsForProvider` literal-string rewriting), config helpers, and retry heuristics.

Create leaf package `internal/ai`:

```go
type Request struct {
    System, User string
    Worktree     string          // used only by tool-capable providers
    JSONSchema   json.RawMessage // optional: enables native JSON mode
    Stage        string          // telemetry label
}
type Usage struct{ InputTokens, OutputTokens int; CostUSD float64 }
type Result struct{ Text string; Usage Usage; Model string }
type Capabilities struct{ RepoTools, NativeJSON, Streaming bool }
type Provider interface {
    Complete(ctx context.Context, req Request) (Result, error)
    Capabilities() Capabilities
    Name() string
}
```

Move `complete.go`, `claude_exec.go`, and the transport half of `retry.go` here. Registry keyed by `aiconfig.Provider`. Keep a thin `review.Complete` shim during migration; delete the five `CompleteFunc` typedefs and their import-cycle workarounds. `augmentPromptsForProvider` branches on `Capabilities().RepoTools`, not the provider enum. This is the single seam where later middleware attaches: usage metering (R1), concurrency semaphore (R2), logging (0.3), response caching, multi-model routing (Q7).

*Acceptance:* all existing review tests green; no package outside `internal/ai` imports transport details; five typedefs gone; `augmentPromptsForProvider` has no provider-name comparisons.

### F2 — `internal/llmjson`: one JSON-salvage library

Consolidate: `extractJSONObject` (byte-identical in `review/agents.go` and `conventionwitness/agent.go`), `extractJSONArray` (`techagents/suggest.go`), all of `review/jsonsanitize.go` (fence/comment/triple-quote repair), `langagents`' private `stripOuterMarkdownFence`, and the five divergent parse paths (`tryParseSpecialistJSON`, `parseVibeCoachJSON`, `parseRepoArbiterJSON`, `parseWitnessJSON`, `parseRepairResponse`) into:

```go
func Parse[T any](raw string) (T, error) // full sanitize ladder: fence → extract → comments → triple-quote → variants
```

Critically, this gives the arbiter and witness — currently the *weakest* parsers on the only non-stage-retried paths — the same robustness as specialists. Optionally add schema validation (`santhosh-tekuri/jsonschema`) as a pre-repair check.

*Acceptance:* one parser; table-driven tests covering every sanitize case currently tested; arbiter/witness parse fixtures with fenced/commented JSON now pass.

### F3 — `internal/agentstore`: de-triplicate repo/tech/lang agents

`repoagents/store.go` vs `techagents/store.go` are ~81% identical after rename; `freshness.go` ×3; `sourceHash`/`loadGeneratorPrompt`/`PromptOverridePath`/`readPromptOverride` ×3; `slugify`/`repoDir`/`CacheDir` ×3 (+1 inline in `runner.go`). Build `internal/agentstore` with `Store[T any]` (Load/Save/Delete/ListRepos, owner-repo slugging), a single `Freshness` type (parameterized stale-after — note repoagents/techagents use 30d, langagents 60d; keep per-family config, document it), and a `promptoverride` helper (override-then-embedded resolution). Also extract `internal/appdirs` (ConfigDir/CacheDir XDG resolution honoring `APPR_AI_SAL_CONFIG_DIR`) and delete the five `configDir()` clones — they can all call it today; `theme` and `repoconfig` stop importing `aiconfig` for path helpers.

*Acceptance:* ~800–1,000 LOC removed; three packages become thin domain layers over `agentstore`; `grep -r "func configDir" internal/` returns one hit.

### F4 — Split `review/types.go` and name the verdict state machine

Split into: `finding.go` (domain types), `draft.go` (Draft + suppression/demotion key bookkeeping), `verdict.go` (**a single explicit reducer** replacing the five interacting functions `EffectiveMergeVerdict`/`ReconciledMergeVerdict`/`hasBlockingContent`/`verdictRank`/related — this logic's comments reference past bugs; make its states and transitions enumerable and table-tested), `render.go` (markdown body), `github_payload.go` (`ToReview*`, `EffectiveReviewEventAndBody`), `fallback_prompts.go`. Unify the three finding-identity key formats (`suppressionKey`, dedupe key, witness alignment key) into one `FindingKey` type. Give PR-wide findings a stable per-finding key (the `DemotedFindingKey` comment-hash already exists) so the arbiter can act on one PR-wide finding instead of the whole `(specialist, side)` group.

*Acceptance:* behavior-preserving (existing tests unmodified); verdict reducer has an exhaustive table test; no file in `internal/review` exceeds ~600 lines.

### F5 — TUI `Tab` interface + routing cleanup

Root `Update` (model.go, ~440 lines) hand-forwards to three concrete tab pointers in two separate phases, duplicates `NavBack` handling ×3, and must explicitly case-match seven overlay message types (documented deadlocks when one is forgotten). Introduce:

```go
type Tab interface {
    Init() tea.Cmd
    Update(tea.Msg) (Tab, tea.Cmd)
    View() string
    Resize(w, h int)
    SetContentOrigin(top int)
}
```

with `map[state.ViewMode]Tab` in root; make `state.ViewMode` live (root currently re-declares its own `mode` enum; `state/viewmode.go` and `state/appstate.go` are dead code — use them or delete them). Implement the remaining `NavigateKind`s or delete them. Move the detail view into `internal/tui/tabs/detail` for symmetry. Rename tab packages to kill the `langagentstui` alias plague (e.g. `tabs/langagents` → `tabs/langagentstab` or rename the engine packages). Add one `m.pushErrorOverlay(err)` helper (the push snippet is copy-pasted 6×) and a single `overlays.DismissMsg{Result any}` replacing four per-modal dismiss messages. Route overlay-bound messages via a `ForwardToOverlay` marker interface so new pipeline messages can't strand the overlay again.

*Acceptance:* ~400+ lines out of root; the two forwarding phases collapse to one loop; a regression test reproducing the documented vibe-coach deadlock class passes via root routing (not by calling the overlay directly).

### F6 — Shared TUI primitives (zones/mouse/dropdowns/messages)

- `zones.DispatchClick(msg, []ClickHandler{Zone, Do})` + `ForEachRowZone` helper — collapses each tab's `mouse.go` if-chain (~25 blocks in settings, ~20 in repoagents) to a table.
- `util/dropdown.Host`: owns the `bubble-dropdown` pointer, contentTop translation, focus sync, recreate-on-change, `OnSelect` — deletes the three parallel integrations (settings 260 lines, repoagents 88, root controls-panel fields/forwarders). Upstream candidates: `SetOptions` and absolute-coordinate hit-testing in `madicen/bubble-dropdown` remove two of the six duplicated concerns for every consumer.
- Generic `AsyncResult[K, T]{Key K; Val T; Err error}` message + one shared row-lifecycle (pending/running/done/error badge) state machine — replaces the 13 hand-rolled `*StartedMsg/*DoneMsg` structs across repoagents (×2 families in one file!) and langagents.
- Delete per-tab `style.go` hex-copies (settings' file is a verbatim subset of repoagents'; the "so this package does not import tui" comment is obsolete — `internal/tui/styles` is already a leaf) and route through `internal/tui/styles`.

*Acceptance:* tabs' `mouse.go` shrink ≥50%; one dropdown integration; zero hardcoded hex in `tabs/*` (all via styles/theme).

### F7 — `Backend` interface for data commands + demo

`internal/tui/data/commands.go` (563 LOC, **0 tests**) branches `if demoMode { demo.X() } else { gh.Y() }` in 9 commands and holds the riskiest untested code in the repo (posting orchestration). Define `Backend` (ListPRs, PRDetail, Diff, Checks, Discussion, ExistingComments, Post, …) with `ghBackend` and `demoBackend`. Move the posting orchestration — head-drift preflight, self-author verdict downgrade, file-level fallback, dry-run payload assembly — out of `tea.Cmd` closures into plain functions in `internal/review`/`internal/gh` so they're testable and reusable by the headless CLI (U1).

*Acceptance:* `data` package has tests against a fake `Backend`; posting logic unit-tested; demo mode passes through the same interface.

---

## Phase 2 — Robustness & scale

### R1 — Usage/cost telemetry (S)

Providers already return usage in every response body (`usage`, `usageMetadata`, Claude CLI's `total_cost_usd`/`duration_ms`) and all of it is discarded. With F1's `Result{Usage}`: aggregate per stage and per run in `runner.go`, emit in Progress events, display in the review overlay's done state and the run summary ("14 calls · 182k in / 21k out · ~$0.43 · 6m12s"). Log via 0.3.

### R2 — Inference concurrency semaphore (S)

No client-side rate limiting exists; parallel mode fires up to ~10 concurrent calls (plus hidden repair calls) with no cap. Add a weighted semaphore in the F1 provider layer (`max_concurrent_inference`, default 3). Then flip `ParallelSpecialists`/`ParallelPRAgents` defaults to true — the single biggest wall-clock win available, currently unsafe.

### R3 — Token/prompt budgeting + diff shaping (M — highest-leverage robustness item)

`buildReviewUserPrompt` inlines the entire diff uncapped into every call. Build a diff budgeter (the parsed-diff structures in `diff.go` already exist):

1. Drop lockfiles/generated/vendored/binary files (configurable globs; sensible defaults: `*.lock`, `package-lock.json`, `go.sum`, `vendor/`, `*_generated*`, minified assets) — replaced by a one-line manifest entry.
2. Per-file line caps with `…N lines omitted` markers; whole-prompt byte cap per provider (context-size table, conservative defaults; configurable).
3. On truncation: emit a Progress warning and a note in the rendered review body ("review ran on a truncated diff: files X, Y elided").
4. Per-specialist shaping later (formatting doesn't need test fixtures; testing needs test files most) — leave a hook, don't build yet.

*Acceptance:* a synthetic 5MB-diff fixture completes a run on an HTTP provider without a 400; truncation is disclosed in Progress and body.

### R4 — Retry sanity (S/M)

- Replace substring classification for Claude subprocess errors (`IsRetryableCompleteError` matching `"eof"`, `"429"` anywhere — `"eof"` matches "beforeEach") with a typed error from `runClaude` (exit code + parsed stderr class).
- Cap the multiplicative retry (stage retries × inner Complete retries can reach 25 attempts/stage): share an attempt budget between the two tiers.
- Add an aggregate run circuit breaker: total wall-clock cap and "abort after N consecutive stage failures", surfaced as a Progress event.
- Distinguish `skipped` vs `failed-after-retries` in `SpecialistResult`; final summary lists degraded stages.

### R5 — Native JSON mode (S)

Wire `response_format: {"type":"json_object"}` (OpenAI-compat/Ollama) and `responseMimeType: "application/json"` + `responseSchema` (Gemini) via F1's `Request.JSONSchema`/`Capabilities().NativeJSON`. Keep the F2 salvage ladder as fallback. Cuts parse-failure stage retries (each of which re-pays the full prompt).

### R6 — gh layer consolidation (M, staged)

1. **(S)** GraphQL helper `graphQLQuery[T](ctx, query, vars)` folding the triplicated response/error boilerplate (checks/threads/discussion); merge those three per-PR queries into **one** GraphQL document for the PR-agents prefetch (3 execs → 1).
2. **(M)** Adopt `github.com/cli/go-gh/v2` behind existing function signatures: same auth resolution as the `gh` CLI (keeps the "no separate auth surface" design), in-process REST/GraphQL, real status codes feeding the (kept — it's good) `APIError` taxonomy in `errors.go`, native Retry-After. Migrate transport first (`run`/`runGraphQL`/the three hand-built POST paths), sugar commands (`gh pr view/diff`) later. Keep `git` for checkout.
3. **(M)** Pagination: `reviewThreads(first:100)`, per-thread `comments(first:50)`, discussion `comments/reviews(first:100)`, `search(first:50)` all silently truncate — add cursor loops or explicit overflow warnings. Fix `ListPullReviews` under-fill.
4. **(M)** PR-data cache keyed by `(headSHA, updatedAt)` with `GetPRHeadSHA` revalidation → cheap incremental refresh; batch `RecentPRsTouchingPaths` (currently up to ~60 sequential execs) into GraphQL.
5. **(S)** `gh` minimum-version check in `CheckAuth()` while any shell-out remains.

### R7 — Worktree strategy (M)

Beyond the 0.4 purge: shared bare-repo cache per `owner/repo` with `git worktree add` per run — repeat reviews fetch deltas instead of full clones. Reuse the worktree when headSHA is unchanged.

### R8 — Config hardening (S)

- `ValidateForProvider()` at startup/profile-save (base URL required for openai_compatible, key for gemini, `claude` binary on PATH for claude) surfaced in the TUI instead of first-inference failure; `mergeEnv` warns on invalid values instead of silently dropping.
- Secrets: support `api_key_env` (and optionally `api_key_cmd`) indirection in profiles; redact keys from every marshal path except `Save`; keep 0600.
- Decide and test the "one-shot `--ai-api-key`/env override gets persisted into the named profile on Save" behavior (`syncActiveProfileFromFlat`) — track provenance and exclude overrides from Save, or document it loudly.

---

## Phase 3 — Review quality: the flagship phase

This is the product's heart. Order: Q1 (registry) → Q2 (contracts) → Q3–Q6 in parallel → Q7/Q8.

### Q1 — Specialist registry (the keystone refactor)

Specialists are hard-coded arrays (`AllSpecialists`, `AllPRAgents`) whose names are wired into ~8 files: lane priority, actionability gate applicability, evidence injection (`if name == SpecTesting || name == SpecDocs`), witness filter, arbiter security guards, and both output contracts. Replace with a declarative registry:

```go
type SpecialistSpec struct {
    Name           string
    Kind           Kind        // code | pr-wide
    PromptSource   PromptRef   // embedded default + user-override path
    Inputs         InputSet    // diff, evidence-pack, checks, threads, discussion, briefs…
    Gates          []Gate      // which deterministic gates apply
    LanePriority   int
    ArbiterPolicy  ArbiterPolicy // suppressible? demotable? (security: never)
    Witnessable    bool
    SeverityLadder string      // injected into the prompt
}
```

Adding a specialist becomes: register a spec + write a prompt. Then: **user-defined specialists** as `.md` + `.json` pairs under `~/.config/appr-ai-sal/specialists/` (the prompt-override loader already proves demand). Ship an example (e.g. `performance` or `i18n`).

*Acceptance:* zero behavior change for the built-in panel (golden run against demo fixtures identical); a test registers a custom specialist end-to-end; the ~8 hard-coded dispatch sites are gone.

### Q2 — Generated output contracts + schema

Generate the JSON contracts from the registry instead of hand-maintained consts — this *mechanically* fixes the enum-drift class (0.4 items 1–2 become impossible to reintroduce). Emit a JSON schema per agent for R5's native JSON mode and F2's validation. Slim the PR-agent contract: they currently receive the full ~3.5k-token `reviewOutputContract` although suggestion mechanics are irrelevant to them (their suggestions are force-stripped by `constrainPRAgentScope`) — a dedicated slim contract saves real tokens on 4 of ~10 calls per run.

### Q3 — Prompt overhaul (paired with an evals harness, Q4 — do not land blind)

Concrete fixes from the audit, beyond the 0.4 quick wins:

1. **Value-correctness ownership seam**: `tech.md` claims config/IaC value-correctness, `design.md` explicitly invites one-line literal fixes, `testing.md` forbids them — and when no tech briefs exist the tech specialist doesn't run, leaving "memory: 717M → 717Mi" unowned. Define the ownership rule in the registry/contract (e.g. tech owns it when active; design owns it otherwise) and state it in both prompts.
2. **Disclose all gates to the models**: testing/docs prompts describe their auto-demotion post-processor (excellent pattern — the model can pre-comply); formatting's naming-convention gate and tech's IaC schema gate are undisclosed. Add matching disclosure sections.
3. **Few-shot the failure modes the gates catch**: add one worked *multi-line* suggestion example with indentation (Python/YAML), one adversarial "this suggestion is WRONG and will be stripped, here's why" example, and a correct `anchor_excerpt` example (the current double-negative instruction about diff prefixes is the exact spot models err).
4. **`verified`/`confidence` fields**: the contract's "file unverified findings at reduced severity with a comment saying so" instruction has no machinery. Add `"confidence": 0.0–1.0` and `"verified": bool` to the schema; deterministic gates enrich it (relocated anchor, synthesized suggestion, witness verdict); strictness floors gain a confidence axis (drop low-confidence warnings under balanced); TUI sorts/badges by it.
5. **Strictness for arbiter + witness**: both currently receive no strictness signal; the arbiter can't calibrate demotion aggressiveness to the user's chosen intensity. Thread the strictness block through.
6. **De-Go-ify formatting.md** (four of five "what to report" bullets are Go examples; lang briefs exist precisely for this); give every specialist an explicit severity ladder like security.md's (the only one that has one).
7. **Consolidate verdict definitions** (currently three slightly different phrasings across vibe-coach.md, the contract, and `vibeCoachSystemAddendum` — drift here shifts verdict distribution).
8. **Witness terminology**: rename `congruent/divergent` → `supports_finding/contradicts_finding` (the prompt currently needs a paragraph apologizing for the naming); keep a compat alias while parsing.
9. **checks.md**: the model is told a failing *required* check is `error` but never receives required-ness — plumb it from the checks GraphQL (it's available on `statusCheckRollup` contexts) into `formatChecksSection`.
10. **Repo-agent prompt templating**: the five `repo-agent-*.md` files are ~80% shared boilerplate; generate from a template + per-topic delta.
11. **Full worked example for the arbiter**: it has the most complex contract and no complete realistic JSON example — add one.

### Q4 — Evals harness for prompts (the quality flywheel)

There is zero regression testing of prompt behavior; every prompt tweak is flown blind despite excellent gate unit tests. Build `internal/evals` + `make evals`:

- **Corpus**: fixture PRs (diff + repo-context + briefs) with golden expectations: findings that *must* appear (seeded bugs), findings that must *not* (the documented false-positive scar tissue: memory-Mi units, `aws_s3_bucket_policy` tags, snake_case-on-Go), anchor accuracy, JSON validity, verdict.
- **Scoring**: precision/recall per specialist, anchor-hit rate, suggestion-survival rate (how many pass the gates), JSON-parse-first-try rate, token cost.
- **Runner**: pluggable provider (Ollama for free local CI-nightly; Claude/API for release gates); A/B two prompt versions via the existing override mechanism; markdown report artifact.
- Every Q3 prompt change lands with before/after eval numbers in the PR description.

*Acceptance:* `make evals PROVIDER=ollama` produces a scored report; CI job (nightly or manual) runs it; ≥10 corpus PRs covering all specialists.

### Q5 — Static-analysis pre-pass (ground the findings)

Run cheap deterministic tools in the worktree before specialists and feed results into the evidence pack (`BuildPRReviewEvidence` is the injection point): `gofmt -l`, `go vet`, `golangci-lint` when configs exist (repocontext already harvests lint configs!), `ruff`, `eslint`, `terraform validate` — behind timeouts, fail-open. Effects: (a) specialists are told "the linter already flags X — don't re-report; do report what linters can't see"; (b) the checks agent gets real annotations; (c) replace the hard-coded 13-entry IaC `tags` table (`iac_schema_gate.go`) with `terraform providers schema -json` when terraform is present (keep the table as fallback); (d) "linter is silent" becomes a false-positive signal for formatting findings.

*Acceptance:* on a Go fixture with a gofmt violation, formatting cites the tool instead of hand-flagging; the IaC gate consults live schema when available.

### Q6 — Finding-machinery upgrades

1. **Multi-line suggestions**: the whole engine assumes single-line anchors, but GitHub suggestions support `start_line`..`line` ranges. Extend `Finding` with an optional `StartLine`, the excerpt gate to verify the full range, and the posting payload accordingly. This is the top capability gap for real fixes.
2. **Cross-hunk anchor relocation**: `FindUniqueExcerptInFile` already does unique whole-file matching but is only used for TUI stale-diff re-anchoring; use it as a second chance in `validateAnchorExcerpt` before stripping.
3. **Wrong-line prose comments**: excerpt-mismatched findings *without* suggestions currently keep their line silently; annotate or demote them (a prose comment on the wrong line is a false positive to the reader).
4. **Adjacent-line dedupe**: extend `dedupeInlineFindingsAcrossSpecialists` with a ±2-line window (same Jaccard test); dedupe PR-wide findings across description/scope (currently never touched).
5. **Witness expansion**: feed PR-wide testing/docs findings to the witness (path-history evidence speaks to exactly those; currently inline-only), and add a formatting evidence harvester (identifier-style sampling generalizes `tech_evidence.go`'s token counting) so the witness can cover formatting.
6. **Repair-pass parity**: `applyRepairs` skips `validateAnchorExcerpt` semantics (`tmp.AnchorExcerpt = ""`); give repaired suggestions the same excerpt verification as first-pass ones.

### Q7 — Multi-model routing & ensemble (after F1)

Per-stage model selection in config: haiku-class for formatting/docs/witness, opus-class for security/design/arbiter — cost drops with no quality loss on the cheap lanes. Optional ensemble mode: run security on two models, union with dedupe (machinery exists); run the witness on a different model family than the specialist it audits (decorrelated hallucinations). Config shape: `stage_models: {security: "...", default: "..."}` in profiles.

### Q8 — PR-author intent extraction (S/M)

A cheap pre-pass over description + linked issues (fetch them — currently not fetched at all) extracting `{intent, acceptance_criteria, non_goals, linked_issues}` as a structured section injected into scope (stops guessing intent from the title), testing (acceptance criteria → expected cases), and vibe-coach (grounds its "done-when" output).

---

## Phase 4 — Big new capabilities

### B1 — Reviewer memory: learn from accept/skip (the moat feature)

The TUI already captures the perfect training signal — `UserSkipPostKeys`, `UserPostDemotedKeys`, arbiter suppressions the user reversed — and discards it after each run. Persist per repo under `RepoProfilesDir()`:

```json
{"fingerprint": {"specialist": "...", "path_glob": "...", "comment_hash": "...", "severity": "..."},
 "decision": "posted|skipped|demote_reversed", "count": 3, "last": "..."}
```

Uses, in escalating order: (1) a "previously rejected patterns" section injected into the arbiter prompt; (2) a deterministic pre-arbiter suppressor for high-confidence repeats (N≥3 skips of near-identical findings), disclosed in the TUI ("suppressed: you've skipped this 3×; press x to resurface"); (3) feed the evals corpus (Q4) with real-world negatives. Always user-inspectable and clearable (`appr-ai-sal memory list/clear`).

### B2 — Incremental re-review

Cache the `Draft` keyed by `(owner/repo#N, headSHA)`. On re-review of a PR with new commits: compute the interdiff, re-run specialists only over changed files/hunks, carry forward prior findings whose anchors survive (the excerpt-relocation machinery already solves re-anchoring), and have the discussion agent verify prior findings were addressed. Cost drops from O(PR) to O(delta) and matches how reviewers actually work.

### B3 — Thread-aware posting & replies

`unresolvedThreadAnchors` and `DetectPriorAprrAISalActivity` already exist. When a new finding matches an existing unresolved thread's anchor, post as an in-thread **reply** instead of a duplicate top-level comment; on re-runs, reply to the tool's own prior comments with status ("resolved by commit abc123" / "still present"). Requires the GraphQL `addPullRequestReviewThreadReply` mutation (or REST equivalent) in the gh layer.

### B4 — Chat-with-specialist ("challenge this finding")

From a finding card, `c` opens a scoped exchange: the specialist gets its original finding + the hunk + the user's question, and must either withdraw (with the card auto-skipped) or strengthen its justification. Cheap (one scoped call, like the repair pass), converts marginal findings into signal, and the transcript feeds B1.

### B5 — Context expansion for non-Claude backends

Only the Claude subprocess gets repo tools (`Read,Glob,Grep`); HTTP providers review the diff blind — the biggest quality gap for design/security lanes off-Claude. Deterministic context expander: for each changed symbol, include enclosing full function bodies, type definitions, and callers/callees (via `gopls` when available, ctags fallback), under the R3 token budget. Gate on `Capabilities().RepoTools == false`.

### U1 — Headless CLI mode (CI-ready)

Highly feasible today: `review.Run(ctx, ref, cfg) (<-chan Progress, error)` imports no bubbletea, and a headless subcommand precedent exists (`repo-context`). Add real subcommands (default `tui`):

```
appr-ai-sal review owner/repo#123 --json [--post] [--dry-run] [--fail-on request_changes]
```

Drain Progress as NDJSON on stderr, marshal the final `Draft` to stdout, exit non-zero per `--fail-on` for CI gating. `--post` uses the posting functions extracted in F7. Then a documented GitHub Actions recipe. This turns a TUI into a platform.

### U2 — Draft persistence & resume

Serialize `review.Draft` + card decisions to the cache dir keyed by `owner/repo#N@headSHA`; on reopening a PR with a stored draft, offer resume. Quitting mid-approval currently discards a multi-minute, multi-dollar run.

---

## Phase 5 — TUI/UX quality of life (depends on F5/F6 only)

1. **Central keymap + `?` help overlay** (highest value/effort): adopt `bubbles/key.Binding` for all ~40 bindings (list ~18, detail 23 raw string cases); derive both the status bar and a full-screen `?` overlay from the keymap so hints can't drift. Status bar currently carries 17 hand-maintained segments in list mode.
2. **Edit findings before posting**: textarea (bubbles) editing of comment body + `e` to round-trip through `$EDITOR` via `tea.ExecProcess`. For a tool that posts under the reviewer's name, owning the words is table stakes — arguably the most important missing feature in the product.
3. **Pipeline cancel + completion notification**: thread a cancellable context into `StartReviewCmd` (currently `context.Background()` — closing the overlay leaks the runner); terminal bell/OSC-9 notification when a run finishes while minimized.
4. **Diff upgrades**: chroma syntax highlighting (chroma is already an indirect dep via glamour), intra-line word-level diff, `n`/`p` jump between inline finding tags, in-diff search, jump from approval card to its diff position; side-by-side view later.
5. **Finding triage**: filter/sort the card list by severity/specialist/confidence (Q3.4); severity counts in the tab bar.
6. **Command palette** (`ctrl+k`): fuzzy palette over a real command registry (subsumes status-bar overflow; `sahilm/fuzzy` already indirect).
7. **Theming completion**: route all chrome through the theme (currently 19 themed slots; everything else fixed dark hexes, exactly one `AdaptiveColor` in the tree); semantic palette (bg/fg/surface/accent) + light preset + `NO_COLOR` support.
8. **Thread browsing**: render existing inline PR comments in the diff (already fetched for dedupe, never shown); browsable review-history pane (plumbing exists via `ListPullReviews`); reply via B3.
9. **Clipboard everywhere**: copy PR URL / finding / hunk; add OSC52 fallback (atotto/clipboard fails over SSH).
10. **Queue workflows**: `A` run-panel-on-all-listed-PRs (sequential, respecting R2's semaphore); optional list auto-refresh/watch mode.
11. **teatest + goldens**: adopt `charmbracelet/x/exp/teatest` for 3–5 end-to-end flows against demo mode (list → detail → run → dry-run post); golden-file tests for big render functions (`tabs/review/view.go` is 1,236 untested-render lines). The demo package makes this nearly free.

---

## Phase 6 — Provider expansion (depends on F1)

1. **Anthropic API direct** (`/v1/messages`, `x-api-key`, tool-use JSON forcing) — the most glaring gap: Anthropic users currently *must* install the `claude` CLI; blocks headless/CI use. 529 handling already exists in `APIHTTPError`.
2. **Provider presets**: OpenRouter, GitHub Models, Groq, Together (base-URL presets in the profile editor); Azure OpenAI (needs `api-key` header + deployment URL scheme — a real transport variant).
3. **Model listing** (`ListModels`): Ollama `/api/tags`, OpenAI-compat `/models`, Gemini `/v1beta/models` → picker in the profiles panel (model IDs are currently hand-typed).
4. **Streaming** (SSE; claude CLI `--output-format stream-json`): token-liveness in the running overlay (a 5-minute call currently looks hung) and replace the whole-response HTTP timeout (which kills slow-but-alive generations at exactly `TimeoutSec`) with idle/first-byte timeouts.

---

## Library adoptions (summary table)

| Replace | With | Where | Wins |
|---|---|---|---|
| Hand-rolled diff parser (281 LOC) | `bluekeyes/go-gitdiff` | `internal/review/diff.go` | rename/binary/mode/no-newline edge cases; keep anchor helpers |
| `gh` CLI shell-outs (9 `exec.Command*` sites; `run` in `gh.go`, `runGraphQL` in `review_state.go`) | `cli/go-gh/v2` | `internal/gh` | same auth, no process spawn, real status codes, pagination |
| Hand-rolled backoff (382 LOC) | `cenkalti/backoff/v5` | `internal/review/retry.go` → `internal/ai` | keep only error classification |
| Nothing (no logging) | stdlib `log/slog` → file | everywhere | diagnosability |
| Ad-hoc JSON field checks | `santhosh-tekuri/jsonschema` (optional) | `internal/llmjson` | pre-repair validation, powers R5 |
| 5× cloned XDG resolution | `internal/appdirs` (or `adrg/xdg`) | config/cache paths | one implementation |
| stdlib `flag` + ad-hoc subcommand sniffing | keep flag; add real subcommand dispatch (cobra only if it grows) | `cmd/appr-ai-sal` | U1 needs subcommands |

Extraction candidates for the author's public module family: `internal/tui/overlays` → `bubble-modals`; `internal/tui/util` (glamour cache, viewport, clipboard) → `bubble-utils`; multi-dropdown `Host` → upstream into `bubble-dropdown`.

---

## Suggested milestone packaging

| Milestone | Contents | Theme |
|---|---|---|
| **M1 – Guarded** | 0.1–0.4 | CI + correctness punch list |
| **M2 – Consolidated** | F1–F7 | ~2–3k LOC removed, seams created |
| **M3 – Robust** | R1–R8 | cost visibility, big-PR safety, faster runs |
| **M4 – Sharper** | Q1–Q6 | registry, contracts, prompt overhaul with evals proof |
| **M5 – Smarter** | Q7, Q8, B1, B2, B3 | memory, incremental, threads, routing |
| **M6 – Everywhere** | U1, U2, B4, B5 | headless/CI, resume, chat, context expansion |
| **M7 – Delightful** | Phase 5 | keymap/help, edit-before-post, diff & theme polish |
| **M8 – Open** | Phase 6 | providers, streaming, model listing |

Every milestone ends with: README + docs updated, demo tapes re-recorded if UI changed, eval report attached (M4+), and a tagged release.

---

## Appendix A — Duplication inventory (delete targets)

| Duplicate | Locations | Fix |
|---|---|---|
| `configDir()` ×5 | `review/prompts.go`, `conventionwitness/agent.go`, `{repo,tech,lang}agents/generate.go` | F3 `internal/appdirs` |
| `CacheDir()` ×3 + inline | `{repo,tech,lang}agents/store.go`, `runner.go` | F3 |
| `extractJSONObject` ×2 byte-identical | `review/agents.go`, `conventionwitness/agent.go` | F2 |
| JSON parse cascades ×5 divergent | `agents.go` ×2, `repo_experts.go`, `conventionwitness`, `suggestion_repair.go` | F2 `llmjson.Parse[T]` |
| `CompleteFunc` typedef ×5 | 4 subpackages + `suggestion_repair.go` | F1 |
| store/freshness/prompt-override ×3 | `{repo,tech,lang}agents` | F3 |
| Tab forwarding ×3 ×2 phases; NavBack ×3; error-overlay push ×6 | `tui/model/model.go` | F5 |
| mouse.go if-chains; dropdown integrations ×3; async msg structs ×13; style.go hex copies | `tui/tabs/*`, root | F6 |
| demo-mode branch ×9 | `tui/data/commands.go` | F7 |
| GraphQL response boilerplate ×3; RFC3339 parse ×8; checks-rollup logic ×2 | `internal/gh` | R6.1 |
| Finding-identity keys ×3 formats | `types.go`, `finding_dedupe.go`, witness | F4 `FindingKey` |

## Appendix B — Known contract/prompt inconsistencies (fix in 0.4/Q2/Q3)

1. Vibe `finding_refs` enum omits tech + PR agents (functional bug).
2. Arbiter roster omits tech; stale "Specialist + vibe digest" heading.
3. Value-correctness ownership undefined when tech inactive (tech.md vs design.md vs testing.md).
4. `critical` severity undefined in security.md though `critical_only` strictness depends on it.
5. One-rank vs multi-rank demote contradiction inside repo-arbiter.md.
6. Gate disclosure asymmetry (testing/docs disclosed; naming/IaC gates not).
7. Arbiter + witness receive no strictness signal.
8. Verdict definitions triplicated with drift (vibe-coach.md / contract / strictness addendum).

## Appendix C — Metrics to watch (define before M4)

- Eval scores per specialist: precision / recall / anchor-hit rate / JSON-first-try rate (Q4).
- Suggestion survival rate through gates; repair-pass fire rate (0.4.11).
- Cost + wall-clock per run (R1); truncation rate (R3).
- User signal: post rate vs skip rate per specialist (feeds B1); % findings edited before post (Phase 5.2).
