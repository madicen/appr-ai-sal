# help-palette.tape — global help overlay (?) and fuzzy command
# palette (ctrl+k). Shows the Phase 5 keymap-derived surfaces.
#
# Regen with: vhs vhs/help-palette.tape
# Output:     screenshots/help-palette.gif

Output screenshots/help-palette.gif

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

# PR list — open the full-screen help overlay.
Sleep 1.5s
Type "?"
Sleep 3s
Escape
Sleep 1s

# Command palette from the list view.
Ctrl+k
Sleep 2.5s
Escape
Sleep 1s

# Drill into a PR and open help again (detail bindings differ).
Enter
Sleep 2s
Type "?"
Sleep 3s
Escape
Sleep 1s

Hide
Type "q"
Sleep 500ms
Type "q"
Sleep 500ms
