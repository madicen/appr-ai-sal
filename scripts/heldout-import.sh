#!/usr/bin/env bash
# Import a GitHub PR into docs/evals/held-out/cases/ as an unlabelled skeleton.
# Does not run any model. Label expectations.json before live evals.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 <pr-number> | $0 <owner/repo> <pr-number>" >&2
  exit 2
fi

if [[ $# -eq 1 ]]; then
  REPO="madicen/appr-ai-sal"
  NUM="$1"
else
  REPO="$1"
  NUM="$2"
fi

DEST=$(
  gh pr view "$NUM" --repo "$REPO" --json number,title,author,baseRefName,headRefName,body,additions,deletions,changedFiles,url \
    | REPO="$REPO" python3 -c '
import json, os, pathlib, re, sys
meta = json.load(sys.stdin)
repo = os.environ["REPO"]
num = meta["number"]
title = meta["title"] or f"pr-{num}"
slug = re.sub(r"[^a-z0-9]+", "-", title.lower()).strip("-")[:40] or "pr"
case_id = f"pr-{num}-{slug}"
dest = pathlib.Path("docs/evals/held-out/cases") / case_id
if dest.exists():
    sys.stderr.write(f"exists: {dest}\n")
    sys.exit(1)
dest.mkdir(parents=True)
case = {
    "id": case_id,
    "title": title,
    "target": "held-out",
    "repo": repo,
    "number": num,
    "author": (meta.get("author") or {}).get("login") or "",
    "base_ref": meta.get("baseRefName") or "main",
    "head_ref": meta.get("headRefName") or "",
    "body": meta.get("body") or "",
    "additions": meta.get("additions") or 0,
    "deletions": meta.get("deletions") or 0,
    "changed_files": meta.get("changedFiles") or 0,
    "strictness": "balanced",
    "run_pr_agents": False,
    "run_witness": False,
    "run_arbiter": False,
    "run_vibe_coach": False,
}
(dest / "case.json").write_text(json.dumps(case, indent=2) + "\n")
(dest / "expectations.json").write_text(json.dumps({
    "must_appear": [],
    "must_not_appear": [],
}, indent=2) + "\n")
(dest / "LABEL_STATUS.md").write_text(
    "pending\n\n"
    "Fill expectations.json (must_appear / must_not_appear) **before** any model run.\n"
    "Then change this file to a single line: labelled\n"
)
print(dest)
'
)

gh pr diff "$NUM" --repo "$REPO" > "$DEST/diff.patch"
echo "imported $REPO#$NUM -> $DEST"
echo "next: label expectations.json, set LABEL_STATUS.md to labelled, optionally add worktree/"
