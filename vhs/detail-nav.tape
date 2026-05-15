# detail-nav.tape — PR detail navigation: tree expand/collapse, pane
# cycling, description toggle, controls toggle. No review run — that's
# the hero tape's job. This one shows the static layout the user moves
# through before deciding to fire a review.
#
# Regen with: vhs vhs/detail-nav.tape
# Output:     screenshots/detail-nav.gif

Output screenshots/detail-nav.gif

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

# Drill into the hero PR (#742, top of the list).
Enter
Sleep 2s

# Cycle focus through the three panes (Files → Diff → Controls →
# back to Files) so the viewer sees how each one indicates focus.
Type "g"
Sleep 1.5s
Type "g"
Sleep 1s

# Cycle pane focus.
Tab
Sleep 1.2s
Tab
Sleep 1.2s
Tab
Sleep 1s

# Move down through the tree (j/k navigates rows; space folds folders).
Type "j"
Sleep 600ms
Type "j"
Sleep 600ms
Type "j"
Sleep 1s

# Toggle one folder collapsed.
Space
Sleep 1.2s
Space
Sleep 1.2s

# Toggle the Review controls pane off then on so the viewer sees the
# 2-pane vs 3-pane layout switch.
Type "c"
Sleep 1.5s
Type "c"
Sleep 1.5s

# Exit back to list, then quit.
Hide
Type "q"
Sleep 500ms
Type "q"
Sleep 500ms
