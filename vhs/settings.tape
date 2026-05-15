# settings.tape — settings tab tour: AI provider chips, review
# strictness chips, repo-context tab, theme tab.
#
# Regen with: vhs vhs/settings.tape
# Output:     screenshots/settings.gif

Output screenshots/settings.gif

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

# PR list lands; "o" opens settings starting at Review & AI.
Sleep 1.5s

Type "o"
Sleep 2.5s

# Move down through the strictness rows so the viewer sees the
# bullet chip walk lenient → balanced → strict.
Type "j"
Sleep 700ms
Type "j"
Sleep 700ms
Type "j"
Sleep 700ms
Type "k"
Sleep 1s

# Switch to the next tab — "Repo context" (settings tabs cycle with [ / ]).
Type "]"
Sleep 2.5s

# Hold a beat so the viewer can read the repo-context panel.
Sleep 1.5s

# Switch to the "Theme" tab.
Type "]"
Sleep 2.5s

# Hold a beat for the theme picker.
Sleep 1.5s

# Exit settings, then quit the app.
Hide
Escape
Sleep 400ms
Type "q"
Sleep 500ms
