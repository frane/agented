# agented demo tape — drives the `ae` CLI through one read-edit-undo
# cycle in a temp workspace prepared by docs/demo/render.sh.
#
# Wrapper exports:
#   - AE_DEMO_DIR (tempdir holding the seed file `hello.go`)
#
# Run via the wrapper, not directly:
#
#   docs/demo/render.sh
#
# Output: docs/demo.gif (referenced from the README).

Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1000
Set Height 640
Set Padding 16
Set TypingSpeed 55ms
Set Theme "Dracula"

Type "cd $AE_DEMO_DIR"
Sleep 200ms
Enter
Type "clear"
Enter
Sleep 400ms

# 1. Open — first-touch registration. Returns id, line count,
#    head_edit_id, content_hash, state_token.
Type "ae open hello.go"
Sleep 700ms
Enter
Sleep 2500ms

# 2. View a slice with the state_token captured by the user mentally
#    (or by shell). Demo just shows the read.
Type "ae v hello.go -r 1:8"
Sleep 700ms
Enter
Sleep 2500ms

# 3. Replace a range. Token is from the previous read. Conflict-safe.
Type `ae s hello.go -r 5:5 -w '    fmt.Println("hi from agented")' -x <TOKEN>`
Sleep 700ms
Enter
Sleep 2500ms

# 4. Diff to confirm the edit landed.
Type "ae diff hello.go"
Sleep 700ms
Enter
Sleep 2500ms

# 5. Branching tree — undo walks back, branches lists alternatives.
Type "ae undo hello.go"
Sleep 600ms
Enter
Sleep 2000ms
Type "ae br hello.go"
Sleep 600ms
Enter
Sleep 2400ms

# 6. Annotation — durable cross-session memory.
Type "ae an hello.go a -t 'demo: edit reverted, branch preserved'"
Sleep 600ms
Enter
Sleep 1800ms
Type "ae status hello.go"
Sleep 600ms
Enter
Sleep 2400ms
