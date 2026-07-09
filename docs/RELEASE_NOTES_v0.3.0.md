# Release notes — v0.3.0

This release completes the master improvement plan (Phases 0–6). Highlights:

## Review engine

- Specialist registry with generated JSON contracts and prompt overhaul
- Evals harness (`make evals`, `--replay` for CI) with report at `docs/evals-report.md`
- Q7 routing/ensemble, Q8 intent classification, reviewer memory, incremental reviews
- Thread-aware posting, challenge flow (`c`), context expansion
- Strictness blocks wired into arbiter and convention witness; witness verdict rename with compat parsing
- Optional JSON Schema validation in `internal/llmjson` (`ValidateJSON`, `ParseValidating`)

## TUI

- Tabbed detail view, keymap help (`?`), command palette (`ctrl+k`)
- Edit-before-post, cancel/clipboard/queue, diff triage, review threads
- In-diff search (`/`), match navigation (`n`/`p`), syntax-highlighted hunks
- Theming and teatest goldens

## Headless / CI

- `appr-ai-sal review` subcommand for non-interactive runs
- Session resume, streaming SSE across providers (including Anthropic direct API)
- Azure preset, `ListModels`, provider presets

## GitHub layer

- Remaining `gh pr view/diff/list` shell-outs migrated to in-process go-gh REST/GraphQL
- Auth verified via viewer API instead of `gh auth status`

## Docs & demo

- README updated for new features and GIFs
- Seven VHS recordings under `screenshots/` (including `help-palette.gif`, `detail-diff.gif`)

## Upgrade

```bash
brew upgrade madicen/tap/appr-ai-sal
# or
go install github.com/madicen/appr-ai-sal/cmd/appr-ai-sal@v0.3.0
```

Requires `gh` 2.0.0+ with `gh auth login` completed.

## Tagging (maintainers)

```bash
git tag -a v0.3.0 -m "v0.3.0: improvement plan complete"
git push origin v0.3.0
# goreleaser release (with HOMEBREW_TAP_GITHUB_TOKEN set)
goreleaser release --clean
```
