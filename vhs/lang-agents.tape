# lang-agents.tape — language briefs view, scoped to the languages
# the focused PR's diff actually touches.
#
# Regen with: vhs vhs/lang-agents.tape
# Output:     screenshots/lang-agents.gif

Output screenshots/lang-agents.gif

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

Sleep 1.5s

# Drill into the hero PR so the lang-agents tab opens scoped to the
# Go-only set the diff exercises.
Enter
Sleep 1.5s

# Open the lang-agents tab.
Ctrl+l
Sleep 3s

# The lang-agents tab opens scoped to Go (per the demo diff). Pause
# so the viewer can read the row + freshness chip.
Sleep 2s

# Trigger a regen for Go; fake-complete waits ~900ms so the
# in-progress chip is visible before the row flips back to "fresh".
Type "g"
Sleep 3s

Hide
Escape
Sleep 400ms
Type "q"
Sleep 500ms
