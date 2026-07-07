# appr-ai-sal

A terminal app that pulls the GitHub PRs you've been asked to review, runs a
panel of specialist AI reviewers over them, and lets you edit and post the
review with a keypress. You stay in the loop — nothing goes to GitHub without
your confirmation.

## Quick start

New here? This walks you from zero to your first AI-assisted review.
Skim the [Demo](#demo) below first if you want to see what the TUI looks
like before installing.

### 1. Install the prerequisites

- **Go 1.22+** — check with `go version`.
- **GitHub CLI** — install [`gh`](https://cli.github.com/) and run `gh auth login` once. `appr-ai-sal` uses `gh` for all GitHub auth and PR fetches.
- **Pick one AI backend:**
  - **Claude (default, simplest)** — install the [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) so `claude` is on your `PATH`. No extra config needed.
  - **Ollama (local, no API key)** — install [Ollama](https://ollama.com) and pull a model, e.g. `ollama pull llama3.2`.
  - **Gemini** — grab an API key from [Google AI Studio](https://aistudio.google.com/apikey).
  - **OpenAI-compatible** — note your server's base URL (e.g. `https://api.example.com/v1`) and API key.

### 2. Install appr-ai-sal

**Homebrew (macOS / Linux, recommended):**

```bash
brew install madicen/tap/appr-ai-sal
```

This also pulls in `gh` if you don't already have it. Verify with `appr-ai-sal -h`.

**From source (any platform with a Go toolchain):**

```bash
go install github.com/madicen/appr-ai-sal/cmd/appr-ai-sal@latest
```

This drops the binary in `$GOBIN` (typically `~/go/bin`). Make sure that
directory is on your `PATH`, then verify with `appr-ai-sal -h`.

### 3. Point it at your AI backend

If you installed the `claude` CLI you can skip this step — Claude is the
default. Otherwise export the variables for your chosen provider:

```bash
# Ollama (local, no key needed)
export APPR_AI_SAL_AI_PROVIDER=ollama
export APPR_AI_SAL_AI_MODEL=llama3.2

# Gemini
export APPR_AI_SAL_AI_PROVIDER=gemini
export APPR_AI_SAL_AI_MODEL=gemini-2.0-flash
export APPR_AI_SAL_AI_API_KEY=...

# OpenAI-compatible
export APPR_AI_SAL_AI_PROVIDER=openai_compatible
export APPR_AI_SAL_AI_BASE_URL=https://api.example.com/v1
export APPR_AI_SAL_AI_MODEL=...
export APPR_AI_SAL_AI_API_KEY=...
```

Prefer a GUI? Launch the app and press `o` to open **Settings** with the AI
fields focused — `ctrl+s` saves to `~/.config/appr-ai-sal/ai.json` so you
don't have to keep these env vars around.

### 4. Run your first review

```bash
appr-ai-sal
```

In the TUI:

1. The left pane lists PRs where you've been requested as a reviewer. Use `↑`/`↓` (or `j`/`k`) to highlight one and press `enter` to open it.
   - Empty list? Press `u` to paste a PR URL (e.g. `https://github.com/owner/repo/pull/123`) or `owner/repo#123` shorthand.
2. Press `r` to run the specialist panel. You'll see per-specialist progress while they run in parallel.
3. Read through the rendered draft. Press `,` if you want to dial the review's intensity up or down (lenient / balanced / strict).
4. Press `p` to post the draft as a GitHub review — you'll get a confirm prompt first. Press `q` or `esc` to walk away without posting; **nothing is sent until you confirm**.

Want to kick the tires without touching GitHub or your AI provider? Try the
self-contained demo mode, which runs end-to-end against canned data:

```bash
appr-ai-sal --demo
```

For full details on configuration, providers, repository context, and every
keybinding, keep reading.

## Demo

The hero recording walks through opening a PR, firing the specialist
panel, watching the synthetic pipeline progress through every stage, and
landing on the rendered summary:

![review run](screenshots/review-run.gif)

<details>
<summary>PR detail navigation (overview selector for Description / Checks / Discussion, file tree, pane focus)</summary>

![detail navigation](screenshots/detail-nav.gif)

A small selector above the file tree lets you swap the centre pane between
the rendered PR description, the status-check rollup (with failing-run
output and annotation excerpts inline), and the Conversation timeline
(issue comments + review-summary bodies, time-sorted). Press `g` to jump
to the description, `j`/`k` to walk the unified left-column cursor across
the overview rows and into the file tree, or click any row.

Drag the vertical seam between any two panes to resize them — the border
columns where the panes meet are a 2-cell-wide hit zone. Min widths
keep each pane (and the diff) usable; the resized layout resets to the
defaults on the next app restart.

</details>

<details>
<summary>Repo agents tab — fresh / missing mix, regen flow</summary>

![repo agents](screenshots/repo-agents.gif)

</details>

<details>
<summary>Language agents tab — scoped to the diff's stack</summary>

![language agents](screenshots/lang-agents.gif)

</details>

<details>
<summary>Settings — AI provider, strictness chips, repo-context tab</summary>

![settings](screenshots/settings.gif)

</details>

The recordings come from a self-contained `--demo` mode that swaps the
`gh` CLI, AI providers, and on-disk cache for canned data, so they're
reproducible offline. To regenerate:

```sh
brew install vhs   # one-time, https://github.com/charmbracelet/vhs
make screenshots   # runs every tape under vhs/ and writes screenshots/*.gif
make gif-review-run  # regen just one tape during iteration
make demo          # boot the demo binary interactively (no gh / no AI calls)
```

The tape scripts live in [`vhs/`](vhs/) and the canned data in
[`internal/demo/`](internal/demo/). Tape timings (sleeps, typing speed)
are tuned for a 1300×700 capture; tweak the `Set` directives at the top
of each tape if you need a different size.

The same specialist prompts and JSON contracts are used for every backend;
only the inference transport changes (subprocess `claude` vs HTTP). A bounded
**repository context** block (convention files from the PR worktree plus
optional merged-PR digest) is injected into every specialist and the vibe coach
so Claude and HTTP backends see the same baseline “repo culture” text. The
[Review pipeline](#review-pipeline) section below walks through every agent
that participates in a run.

### Review strictness

How hard specialists (and the vibe coach) should push—**lenient** (rubber stamp:
only egregious issues), **balanced** (default), or **strict** (thorough pass).
The vibe coach is told that each JSON `prompt` must be copy-pasteable text for
the author’s AI to **fix the concrete problems** in the specialist findings,
not generic advice.

Set via `review_strictness` in `ai.json`, **`APPR_AI_SAL_REVIEW_STRICTNESS`**, the
in-app **Settings** pane (**`,`** opens with review strictness focused; **`o`** opens with AI fields focused), or **`-review-strictness`**. Accepted aliases include
`light` / `rubber_stamp` → lenient and `thorough` / `heavy` → strict.

## Requirements

- Go 1.22+
- `gh` (GitHub CLI) — used for auth; run `gh auth login` once
- One AI backend (pick one):
  - **Claude** — `claude` (Claude Code CLI) on your `PATH` (default)
  - **Gemini** — Google AI API key; model id such as `gemini-2.0-flash`
  - **Ollama** — local OpenAI-compatible server (default base `http://127.0.0.1:11434/v1`)
  - **OpenAI-compatible** — any server implementing `POST …/v1/chat/completions` (set base URL)

Auth for GitHub is delegated to `gh`. HTTP backends use your configured API key
where required; keys are not printed in progress logs. The TUI masks the API
key field.

## Install

```bash
brew install madicen/tap/appr-ai-sal     # macOS / Linux, brings in gh
# or
make install                             # installs to $GOBIN (typically ~/go/bin)
# or
go install github.com/madicen/appr-ai-sal/cmd/appr-ai-sal@latest
```

## Usage

```bash
appr-ai-sal
```

Opens a two-pane TUI:

- **Left**: PRs where you've been requested as a reviewer.
- **Right**: PR detail, diff, and the draft review.

### Keybinds

| Key       | Action                                                  |
|-----------|---------------------------------------------------------|
| `↑` / `↓` | Move in the PR list (or `j` / `k`)                      |
| `enter`   | Open the highlighted PR                                 |
| `r`       | Run the specialist panel on the open PR                 |
| `p`       | Post the draft as a review (with a confirm step)        |
| `o`       | Open **Settings** with AI fields focused (provider, model, URL, key, timeout); **ctrl+s** saves to `ai.json` |
| `,`       | Open **Settings** with review strictness focused (**ctrl+,** is often sent as **ctrl+@** and works the same) |
| `u`       | Paste a PR URL or `owner/repo#N` shorthand              |
| `R`       | Refresh the PR list                                     |
| `/`       | Filter the PR list                                      |
| `esc`     | Back / cancel                                           |
| `q`       | Quit (or back to list from a sub-view)                  |

### Specialists

Each comment in the draft is tagged with the specialist that produced it.
Posted GitHub bodies state clearly that **appr-ai-sal** (AI) generated the text
and name the **agent** (specialist). GitHub **suggestion** blocks are only used
when the model supplies literal replacement code; otherwise feedback stays
comment-only. PR-wide notes use `path` / `line` cleared so they appear in the
review summary instead of on a line.

Specialist labels in the UI:

- `[formatting]` — style, naming, layout, lint-style nits
- `[design]` — separation of concerns, abstractions, API shape
- `[testing]` — coverage gaps, missing edge cases, brittle tests
- `[docs]` — missing or stale comments and docstrings
- `[security]` — secrets, injection, unsafe defaults
- `[vibe-coach]` — prompts the author can paste back into their AI to fix
  the most important issues in one pass

Specialist prompts ship embedded in the binary (sourced from
`internal/review/prompts/<name>.md` in this repo). To customize a specialist
without rebuilding from source, drop an override at
`~/.config/appr-ai-sal/prompts/<name>.md` (or `$XDG_CONFIG_HOME/appr-ai-sal/prompts/<name>.md`).
The override takes precedence over the embedded prompt.

## Review pipeline

A run is a chain of small agents, each with a narrow job. The
[Specialists](#specialists) subsection above lists the five code reviewers
whose tags you see on each finding; this section explains every agent that
participates — including the briefs they consume — and the order things
happen in.

### The agents

- **Code-reviewing specialists** (5 agents: `formatting`, `design`,
  `testing`, `docs`, `security`). Each reads the PR (diff + injected
  context briefs, repository tools when running on Claude) and returns
  structured findings inside its lane. They never reach outside their
  specialty in the rendered output, and every comment in the draft is
  tagged with the specialist that produced it. Sequential by default;
  toggle **Parallel specialists** in the Review controls pane (or set
  `parallel_specialists: true` in `repo-context.json`) to run them
  concurrently.

- **Repo agents** (one brief per `(repo, specialist)`). Short markdown
  documents describing how *this* repo handles each topic — e.g. the
  testing brief might say "small pure helpers ship without tests in this
  codebase." Each specialist's brief is threaded into its own user
  prompt before it runs, so the specialist's findings reflect the local
  convention rather than a generic best-practice. Open the **Repo
  agents** tab with `ctrl+r` (or click the row in the controls pane);
  build / refresh from the PR you're on with `ctrl+b`.

- **Tech experts** (one brief per `(repo, technology)`). Per-stack
  conventions — e.g. "Kestra flows in this repo register triggers via
  X." A tech brief lives on a repo and is shared across all five
  specialists for that repo. Tech experts are opt-in: a fresh repo has
  none until you add one. Manage them with `ctrl+t`.

- **Language briefs** (one brief per language). Convention notes that
  apply *across* repos — Python uses `snake_case`, Go's exported
  identifiers carry godoc comments, etc. The runner picks the brief(s)
  matching the diff's dominant language(s) and injects them into every
  specialist. The top-5 languages ship as bundled defaults; you can
  generate, edit, and refresh them in the **Lang agents** tab (`ctrl+l`).

- **Repository context block**. A bounded text blob built from the PR
  worktree's convention files (`AGENTS.md`, `README`, lint configs)
  plus an optional digest of recent merged-PR titles. Surfaced to the
  human reviewer in the TUI; the per-specialist repo agent briefs above
  are what specialists actually consume on the prompt side.

- **Per-PR evidence pack**. Static + history evidence harvested from
  the PR worktree (sibling test files, doc.go presence,
  exported-symbol coverage, recent merged PRs touching the changed
  paths). Currently injected only into the `testing` and `docs`
  specialists and reused by the convention witness below.

- **Convention witness**. A per-finding sanity check that fires
  *between* the specialists and the arbiter for `testing` / `docs`
  findings. For each finding it answers a single question — "does the
  rest of this repo actually do what this finding asks for?" — and
  tags it `congruent`, `divergent`, or `unknown` with a short
  citation. The arbiter consumes the verdicts; reviewers see the
  divergent/congruent/unknown tallies in the rendered review body.
  Disabled by setting `convention_witness: false` in
  `repo-context.json`.

- **Repo arbiter**. The last gate before the human sees the findings.
  Reads every specialist's output plus the briefs and convention
  witnesses, and may **suppress** an inline comment (drop it from the
  GitHub post; still shown in the TUI so the reviewer can override),
  **demote** its severity by one rank, or **override** the merge
  verdict. Defaults to "trust the specialists" — most calibration
  already happened upstream via the briefs — and only intervenes when
  a finding contradicts an explicit repo norm. Disabled by setting
  `repo_expert_panel: false` in `repo-context.json`.

- **Vibe coach**. A synthesis pass after the arbiter that reads the
  surviving findings and emits (a) a merge **verdict** (`approve` /
  `request_changes` / `comment`) and (b) a small set of paste-ready
  **author prompts** the PR author can drop into *their* AI assistant
  to fix the most important issues in one or two iterations. Re-runs
  lazily if you skip findings during the approval flow so the verdict
  and summary stay in sync with what you're actually posting.

### Run order

1. **Worktree + diff** — clone the PR head into
   `~/.cache/appr-ai-sal/worktrees/`, fetch the unified diff. No LLM
   calls yet.
2. **Context injection** — load the per-specialist repo agent briefs,
   tech expert briefs, language briefs, the repository context block,
   and (for `testing` / `docs`) the per-PR evidence pack. Progress for
   each appears in the **Context injection** group at the top of the
   running overlay.
3. **Specialists** run with their injected briefs (sequential by
   default; parallel when configured). Each one is independently
   retried inside its own per-stage budget.
4. **Convention witness** (optional) classifies every testing/docs
   finding against the PR evidence pack.
5. **Repo arbiter** (optional) reconciles the specialist findings with
   the briefs and witnesses; may suppress, demote, or override the
   verdict.
6. **Vibe coach** consumes the post-arbiter findings and produces the
   verdict + paste-ready author prompts.
7. **Review overlay** opens. You walk findings one card at a time
   (`y` to post, `n` / `s` to skip, `←` / `→` to navigate, `f` to skip
   the rest), then confirm the summary. **Nothing hits GitHub until
   you press `y` on the final confirmation.**

The chrome `[-]` button collapses the modal to its tab strip so you
can keep browsing the diff while the pipeline runs; flip **Start
review minimized** in the Run options pane to open the modal that way
by default.

## AI configuration

Resolution order: **CLI flags** (`-ai-*`) **>** environment variables **>**
optional JSON file **`~/.config/appr-ai-sal/ai.json`** (or under
`$APPR_AI_SAL_CONFIG_DIR`) **>** defaults.

### Providers

| Provider | Behavior |
|----------|----------|
| `claude` (default) | Subprocess `claude -p --output-format json` with repo tools scoped to the PR worktree. |
| `gemini` | `generateContent` on the Google Generative Language API. Requires `APPR_AI_SAL_AI_API_KEY` and a model id (e.g. `gemini-2.0-flash`). Optional `APPR_AI_SAL_AI_BASE_URL` overrides the API origin (default `https://generativelanguage.googleapis.com`). |
| `ollama` | OpenAI-style `POST {base}/v1/chat/completions`. Default base `http://127.0.0.1:11434/v1` if `base_url` is empty. Bearer uses the string `ollama` when no API key is set (local servers). |
| `openai_compatible` | Same chat schema as Ollama; you must set `APPR_AI_SAL_AI_BASE_URL` to your server’s OpenAI root (e.g. `https://api.example.com/v1`). |

### Environment variables

| Variable | Meaning |
|----------|---------|
| `APPR_AI_SAL_AI_PROVIDER` | `claude` \| `gemini` \| `ollama` \| `openai_compatible` |
| `APPR_AI_SAL_AI_BASE_URL` | Optional HTTP base (see provider table). |
| `APPR_AI_SAL_AI_MODEL` | Model id (`--model` for Claude; HTTP model field for others). |
| `APPR_AI_SAL_MODEL` | Legacy alias for Claude only when `APPR_AI_SAL_AI_MODEL` is unset. |
| `APPR_AI_SAL_AI_API_KEY` | API key for Gemini / OpenAI-compatible; often unused for local Ollama. |
| `APPR_AI_SAL_AI_TIMEOUT_SEC` | HTTP client timeout and overall review context floor (default `300`). |
| `APPR_AI_SAL_REVIEW_STRICTNESS` | `lenient` \| `balanced` \| `strict` (plus aliases above). |

### CLI flags

```text
-ai-provider string       claude | gemini | ollama | openai_compatible
-ai-base-url string      HTTP base URL when applicable
-ai-model string         model id for the active provider
-ai-api-key string       prefer env for secrets
-ai-timeout-sec int      default 300; use -1 to leave unchanged from file/env
-review-strictness string lenient | balanced | strict
-version                 print the version and exit
```

`appr-ai-sal -version` (or the bare `appr-ai-sal version` subcommand) prints the
build's version string and exits. Local `go run` / `go build` builds report
`dev`; release binaries carry the tagged version stamped in by GoReleaser.

### Ollama quick start

1. Install [Ollama](https://ollama.com) and pull a model (e.g. `ollama pull llama3.2`).
2. Set `APPR_AI_SAL_AI_PROVIDER=ollama` and `APPR_AI_SAL_AI_MODEL` to that model name.
3. Leave base URL empty to use `http://127.0.0.1:11434/v1`, or set `APPR_AI_SAL_AI_BASE_URL` explicitly.

### Gemini quick start

1. Create an API key in [Google AI Studio](https://aistudio.google.com/apikey) (or your Google Cloud project).
2. `export APPR_AI_SAL_AI_PROVIDER=gemini`
3. `export APPR_AI_SAL_AI_API_KEY=...`
4. `export APPR_AI_SAL_AI_MODEL=gemini-2.0-flash` (or another supported model id).

Large PRs may exceed smaller models’ context windows; prefer a large-context model or a smaller diff.

### GitHub / cache paths

- `APPR_AI_SAL_CONFIG_DIR` — directory for prompt overrides and `ai.json`.
  Defaults to `$XDG_CONFIG_HOME/appr-ai-sal` or `~/.config/appr-ai-sal`.
- `APPR_AI_SAL_CACHE_DIR` — directory for PR worktrees.
  Defaults to `$XDG_CACHE_HOME/appr-ai-sal/worktrees` or
  `~/.cache/appr-ai-sal/worktrees`.
- Repository profiles (merged-PR digest cache) live next to that layout under
  `repo-profiles` (e.g. `~/.cache/appr-ai-sal/repo-profiles` when using the
  default cache root).

### Logging

The TUI can't write to stderr without corrupting the screen, so structured
diagnostics (`log/slog`) go to a file instead. Every LLM call
(provider / model / stage / duration / retry count), every `gh` invocation, and
each pipeline stage transition is logged; **API keys are never written** (any
key material is redacted before it reaches the log).

- `APPR_AI_SAL_LOG_LEVEL` — `debug` \| `info` \| `warn` \| `error`
  (default `info`). Raise to `debug` when diagnosing a failed review run.
- `APPR_AI_SAL_LOG_DIR` — explicit override for the log directory.
  Otherwise logs land under `$APPR_AI_SAL_CONFIG_DIR/log`,
  `$XDG_STATE_HOME/appr-ai-sal/log`, or `~/.local/state/appr-ai-sal/log`.
- The log file is `appr-ai-sal.log` inside that directory; a failed run is
  meant to be diagnosable from that file alone.

### Repository context (`repo-context.json`)

Optional JSON at **`~/.config/appr-ai-sal/repo-context.json`** (same directory
rules as `ai.json` / `$APPR_AI_SAL_CONFIG_DIR`):

| Field | Meaning |
|-------|---------|
| `repo_roots` | Map of `owner/repo` → absolute path to a **local clone** used only to read convention files that are missing from the PR worktree (style reference, not source of truth for changed lines). |
| `max_bytes` | Hard cap on the injected repository context block (default ~24 KiB). |
| `ttl_seconds` | How long merged-PR digest cache entries are reused before refresh (default 86400). |
| `include_pr_history` | When `true` (default), append recent merged PR titles from GitHub via `gh`. Omit this key in JSON to keep the default `true`; set explicitly to `false` to disable. |
| `pr_history_limit` | Max merged PR rows to fetch (default 30). |
| `repo_culture_summarize` | When `true`, one extra AI call turns the title list into short bullets (same provider as reviews). |

**Security / caps:** only a fixed allowlist of small convention paths is read;
paths under `.git`, `vendor`, `.env*`, key-like names, and similar are skipped;
each file read is capped. The digest never embeds full bodies—only titles,
URLs, and the first meaningful line of each PR body.

**Stale local clone vs PR head:** the diff and worktree always describe what
changed on the branch under review. A mapped local clone only fills in missing
convention snippets (for example when the PR worktree is sparse); it is not a
substitute for the head checkout.

**CLI refresh** (requires `gh` auth for PR history):

```bash
appr-ai-sal repo-context refresh owner/repo
appr-ai-sal repo-context refresh --all-mapped   # every entry in repo_roots
```

Before each review run, if the merged-PR cache entry is missing or past TTL, it
is rebuilt automatically (progress line `repo context:` in the TUI).

## Development

CI (GitHub Actions) builds, vets, and tests on both Ubuntu and macOS, and runs
`golangci-lint` + `govulncheck`. The suite is hermetic — nothing in it requires
the `gh` or `claude` CLIs or network access. The same checks are available
locally via the `Makefile`:

```bash
make test        # go test ./...
make test-race   # go test -race ./...   (mirrors CI)
make cover       # go test -cover ./...
make lint        # golangci-lint run ./...
```

`make lint` needs `golangci-lint` on your `PATH`; install it with
`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` or
`brew install golangci-lint`. The enabled linters live in `.golangci.yml`.

## Status

Early MVP. Built for one user (the author) to validate the loop, then
shareable with a team. Known gaps: no in-app editing of the draft body
yet, no draft persistence between runs, and no resume of a review for a PR
already reviewed. Worktrees under the cache dir are garbage-collected on
startup (older than 7 days, or beyond the newest 2 per PR). Issues and PRs
welcome once it's stable.
