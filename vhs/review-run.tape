# review-run.tape — hero recording: open the demo binary, drill into a
# PR, run the synthetic review pipeline end-to-end, and watch every
# stage transition through the persistent overlay.
#
# Regen with: vhs vhs/review-run.tape
# Output:     screenshots/review-run.gif

Output screenshots/review-run.gif

Set FontSize 14
Set Width 1300
Set Height 700
Set Theme "Builtin Dark"
Set TypingSpeed 65ms
Set PlaybackSpeed 1.0

# The Makefile target sets APPR_AI_SAL_DEMO_DIR before invoking vhs;
# we still set it inline in case someone runs the tape directly so the
# fixture-seeded "fresh" repo brief renders without manual setup.
Hide
Type "export APPR_AI_SAL_DEMO_DIR=$PWD/tmp/demo"
Enter
Type "./appr-ai-sal --demo"
Enter
Sleep 1.5s
Show

# Opening shot: the PR list with the canned set of review-requested PRs.
# Hold a beat so the viewer can read the list.
Sleep 2.5s

# Drill into the highlighted PR.
Enter
Sleep 3s

# The PR detail view paints: tree on the left, diff in the middle,
# Review controls on the right. Pause so the layout reads.
Sleep 3s

# Kick off the review pipeline. The synthetic progress generator
# spends ~1.5s on each pre-specialist stage, then runs the five
# specialists in parallel with staggered ~10s completion, then
# walks the witness / arbiter / vibe-coach post-stages. Total
# ~28s; we add a small buffer so the "done" event lands cleanly.
Type "r"
Sleep 30s

# The overlay lands on the summary / approve phase — give the
# viewer a beat to read the rendered findings before we close.
Sleep 5s

# Close the overlay and exit cleanly.
Hide
Type "q"
Sleep 500ms
Type "q"
Sleep 500ms
