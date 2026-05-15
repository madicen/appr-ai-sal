# appr-ai-sal

A terminal app that pulls the GitHub PRs you've been asked to review, runs a
panel of specialist AI reviewers over them, and lets you edit and post the
review with a keypress. You stay in the loop — nothing goes to GitHub without
your confirmation.

## Demo

The hero recording walks through opening a PR, firing the specialist
panel, watching the synthetic pipeline progress through every stage, and
landing on the rendered summary:

![review run](screenshots/review-run.gif)

<details>
<summary>PR detail navigation (tree expand / collapse, pane focus, controls toggle)</summary>

![detail navigation](screenshots/detail-nav.gif)

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

The specialists are independent: each one focuses on a single concern
(formatting, design, testing, docs, security) so their feedback is targeted
and not muddled. A "vibe coach" specialist reads the combined output and
produces a small set of high-leverage prompts the PR author can paste back
into their own AI assistant.

The same specialist prompts and JSON contracts are used for every backend;
only the inference transport changes (subprocess `claude` vs HTTP). A bounded
**repository context** block (convention files from the PR worktree plus
optional merged-PR digest) is injected into every specialist and the vibe coach
so Claude and HTTP backends see the same baseline “repo culture” text.

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
make install     # installs to $GOBIN (typically ~/go/bin)
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

## How it works

1. You select a PR (or paste a URL).
2. `appr-ai-sal` fetches the PR's branch into a worktree under
   `~/.cache/appr-ai-sal/`.
3. It builds repository context from the PR worktree (and optional mapped local
   clone for missing convention files), optionally appends a capped list of
   recent merged PR titles from `gh`, then runs one inference call per
   specialist in parallel. With **Claude**, that is `claude -p` with read-only
   repo tools under the worktree. With **HTTP** backends there are no repo tools;
   models rely on the unified diff, PR metadata, and the same injected
   repository context block as Claude.
4. Each specialist returns structured findings (path, line, severity,
   comment, suggested edit).
5. Once all specialists return, the vibe coach runs over their collected
   output to produce author prompts.
6. The TUI renders the draft review. You review, edit, and post.

Nothing is posted to GitHub until you press `p` and confirm.

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
```

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

## Status

Early MVP. Built for one user (the author) to validate the loop, then
shareable with a team. Known gaps: no in-app editing of the draft body
yet, no draft persistence between runs, no resume of a review for a PR
already reviewed, and worktrees are not cleaned up automatically. Issues
and PRs welcome once it's stable.
