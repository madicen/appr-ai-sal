# detail-diff.tape — diff upgrades: in-diff search (/), finding-tag
# jump (n/p), and syntax-highlighted hunks on the PR detail view.
#
# Regen with: vhs vhs/detail-diff.tape
# Output:     screenshots/detail-diff.gif

Output screenshots/detail-diff.gif

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
Enter
Sleep 2.5s

# Walk into the file tree so the centre pane shows a hunk.
Type "j"
Sleep 800ms
Type "j"
Sleep 800ms
Type "j"
Sleep 1.5s

# In-diff search: / opens the prompt; type a token from the demo diff.
Type "/"
Sleep 500ms
Type "demo"
Enter
Sleep 2s

# Step through matches with n, then back with p.
Type "n"
Sleep 1s
Type "p"
Sleep 1s
Escape
Sleep 1s

Hide
Type "q"
Sleep 500ms
Type "q"
Sleep 500ms
