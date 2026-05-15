# detail-nav.tape — PR detail navigation: PR-overview selector
# (Description / Checks / Discussion above the file tree), tree
# expand/collapse, pane cycling, controls toggle. No review run —
# that's the hero tape's job. This one shows the static layout the
# user moves through before deciding to fire a review.
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

# PR list lands. The new chips on each row show +N/-M, file count,
# and the CI rollup state — give the viewer a beat to read them.
Sleep 2s

# Drill into PR #318 (top row — has a failing CI run wired up so
# the Checks tab below has something dramatic to render).
Enter
Sleep 2s

# The PR-overview selector sits above the file tree. Description is
# the top row; press `g` to land on it (kept as a muscle-memory key).
Type "g"
Sleep 2.5s

# Move down to Checks. Up-arrow walks the unified left-column cursor
# from the file tree back into the overview rows; pressing j from
# Description steps through Checks → Discussion → first tree row.
Type "j"
Sleep 2.5s

# Down again to Discussion.
Type "j"
Sleep 2.5s

# Step into the file tree — the cursor crosses the boundary into the
# first file row and the centre pane snaps back to that file's diff.
Type "j"
Sleep 1.5s

# Walk a couple more file rows so the gutter / hunk colors land.
Type "j"
Sleep 800ms
Type "j"
Sleep 1s

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
