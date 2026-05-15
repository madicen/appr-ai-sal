#!/usr/bin/env bash
# setup-demo-fixtures.sh — seed the demo cache directory with a mix of
# "fresh" and "missing" repo / lang / tech agent briefs so the agents
# tabs in a VHS recording always render the same shape (one ready
# repo, one unconfigured repo, one cached language brief).
#
# The demo binary picks up these fixtures via APPR_AI_SAL_DEMO_DIR;
# the demo CLI flag in cmd/appr-ai-sal/main.go points
# APPR_AI_SAL_CONFIG_DIR/CACHE_DIR inside that root, and the cache
# packages resolve sibling profile directories from CACHE_DIR via
# filepath.Join(v, "..", "...").
#
# Re-running is idempotent — the script overwrites the same files each
# time so a re-record always starts from the same state.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

DEMO_DIR="${APPR_AI_SAL_DEMO_DIR:-${REPO_ROOT}/tmp/demo}"
CONFIG_DIR="${DEMO_DIR}/config"
CACHE_DIR="${DEMO_DIR}/cache"

# Cache layout (see internal/review/{repoagents,langagents,techagents}/store.go):
#   $CACHE_DIR/../repo-profiles/<owner>__<repo>/repo-agents.json
#   $CACHE_DIR/../repo-profiles/<owner>__<repo>/tech-agents.json
#   $CACHE_DIR/../lang-agents/lang-agents.json
# CACHE_DIR is set to $DEMO_DIR/cache by cmd/appr-ai-sal/main.go's
# configureDemoEnv, so the sibling dirs end up under $DEMO_DIR.
PROFILES_DIR="${DEMO_DIR}/repo-profiles"
LANG_DIR="${DEMO_DIR}/lang-agents"

mkdir -p "${CONFIG_DIR}" "${CACHE_DIR}" "${PROFILES_DIR}" "${LANG_DIR}"

GENERATED_AT="2026-05-13T22:00:00Z"

# Repo 1 — madicen/appr-ai-sal: seeded with a full set of specialist
# briefs so the repo-agents tab renders this row as "fresh" without
# the demo having to fake-complete on open.
APPR_DIR="${PROFILES_DIR}/madicen__appr-ai-sal"
mkdir -p "${APPR_DIR}"

cat > "${APPR_DIR}/repo-agents.json" <<JSON
{
  "owner": "madicen",
  "repo": "appr-ai-sal",
  "agents": {
    "formatting": {
      "specialist": "formatting",
      "context": "# Formatting brief\n\nGo source uses tabs for indentation, gofmt-canonical layout, and import groups (stdlib | third-party | this-module). Comments wrap at 80 cols where practical; inline comments above the line they describe.",
      "generated_at": "${GENERATED_AT}",
      "model": "demo",
      "provider": "demo"
    },
    "design": {
      "specialist": "design",
      "context": "# Design brief\n\nPrefer composition over inheritance; thin data structs with behaviour on the package boundary. Errors flow up via fmt.Errorf(\"...: %w\", err). Each TUI tab owns its own bubblezone hit boxes.",
      "generated_at": "${GENERATED_AT}",
      "model": "demo",
      "provider": "demo"
    },
    "testing": {
      "specialist": "testing",
      "context": "# Testing brief\n\nTable-driven tests live next to the code under review; fixtures stay inline as const strings unless they exceed ~30 lines. Subtests use t.Run with a descriptive name. No global state between tests — every test that touches env vars uses t.Setenv.",
      "generated_at": "${GENERATED_AT}",
      "model": "demo",
      "provider": "demo"
    },
    "docs": {
      "specialist": "docs",
      "context": "# Docs brief\n\nExported funcs and types carry a doc comment that explains intent (the why), not just shape. Package-level docs go in a doc.go file when there's enough to say. Public APIs include a short usage example wherever the call shape isn't self-evident.",
      "generated_at": "${GENERATED_AT}",
      "model": "demo",
      "provider": "demo"
    },
    "security": {
      "specialist": "security",
      "context": "# Security brief\n\nNever shell out with unvalidated user input; every gh CLI call goes through internal/gh and uses fixed argv. Secrets read from env vars only — never hardcoded, never echoed to logs. HTTP clients set timeouts on every request.",
      "generated_at": "${GENERATED_AT}",
      "model": "demo",
      "provider": "demo"
    }
  }
}
JSON

# Tech agents file lives next to the repo-agents file in the same
# repo-profile dir.
cat > "${APPR_DIR}/tech-agents.json" <<JSON
{
  "owner": "madicen",
  "repo": "appr-ai-sal",
  "agents": {
    "bubble-tea": {
      "tech": "bubble-tea",
      "label": "Bubble Tea",
      "seed": "Charm's TUI framework",
      "context": "# Bubble Tea brief (madicen/appr-ai-sal)\n\nElm-style update loop. Side effects flow through tea.Cmd. Widgets exchange tea.Msg — never reach into another model's state. viewport.Model content set via SetContent. Mouse zones registered inside the view function so hit boxes refresh per render.",
      "generated_at": "${GENERATED_AT}",
      "model": "demo",
      "provider": "demo"
    }
  }
}
JSON

# Repo 2 — madicen/plumbing-svc: deliberately NOT seeded. The
# repo-agents tab renders this row as "missing" until the user
# triggers regen, demonstrating the per-repo state mix.

# Lang briefs live in a single user-global file with all languages.
cat > "${LANG_DIR}/lang-agents.json" <<JSON
{
  "agents": {
    "go": {
      "language": "go",
      "context": "# Go language brief\n\nReturn (T, error) from anything that can fail. Don't panic for ordinary error paths. Goroutines have a clear cancellation path. time.Sleep in production code is a smell — use channel signalling instead. Mutex use carries a doc comment naming the invariant the lock protects.",
      "generated_at": "${GENERATED_AT}",
      "model": "demo",
      "provider": "demo"
    }
  }
}
JSON

cat <<MSG
demo fixtures written under ${DEMO_DIR}
  repo-profiles/madicen__appr-ai-sal/repo-agents.json   (fresh, all 5 specialists)
  repo-profiles/madicen__appr-ai-sal/tech-agents.json   (fresh, 1 tech: bubble-tea)
  repo-profiles/madicen__plumbing-svc/                  (missing — intentional)
  lang-agents/lang-agents.json                          (fresh, language: go)

To use these fixtures with the demo binary:
  export APPR_AI_SAL_DEMO_DIR=${DEMO_DIR}
  ./appr-ai-sal --demo
MSG
