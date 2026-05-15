# repo-agents.tape — repo-agents tab: shows the per-repo specialist
# briefs view, the mix of fresh / missing repo states, and a single
# regen-all that walks through the fake-complete loop.
#
# Regen with: vhs vhs/repo-agents.tape
# Output:     screenshots/repo-agents.gif

Output screenshots/repo-agents.gif

Set FontSize 14
Set Width 1300
Set Height 700
Set Theme "Builtin Dark"
Set TypingSpeed 65ms

Hide
Type "export APPR_AI_SAL_DEMO_DIR=$PWD/tmp/demo"
Enter
Type "./appr-ai-sal --demo"
Enter
Sleep 1.5s
Show

# PR list lands.
Sleep 1.5s

# Drill into the hero PR so ctrl+r pre-focuses on madicen/appr-ai-sal.
Enter
Sleep 1.5s

# Open the repo-agents tab. ctrl+r from the detail view pre-focuses
# on the current PR's repo (madicen/appr-ai-sal in this fixture).
Ctrl+r
Sleep 3s

# Pause on the fully-seeded repo so the viewer can read the brief
# preview and the per-specialist freshness chips.
Sleep 2s

# Switch to the second repo (madicen/plumbing-svc), which has no
# briefs — the rows render as "missing" until a regen.
Right
Sleep 2.5s

# Kick off "Regenerate all" for the second repo. Each fake-complete
# waits ~900ms (internal/demo/agents.go), so this stays on screen
# long enough to read the in-progress chip flicker.
Type "A"
Sleep 5s

# Switch back to the first repo so the closing frame lands on the
# fully-fresh state instead of mid-regen.
Left
Sleep 1.5s

Hide
Escape
Sleep 400ms
Type "q"
Sleep 500ms
