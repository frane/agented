# Real-agent demo: drives Claude Code in non-interactive (`-p`) mode through
# a small refactor that exercises ae's read-edit-token-verify loop. Catches
# what the hand-typed CLI tape can't: a real agent calling ae verbs, the
# state_token actually threading forward, the audit log reflecting the
# agent's actor name.
#
# Wrapper exports:
#   - AE_DEMO_DIR   tempdir holding the seed hello.go
#   - ANTHROPIC_API_KEY (must be set; --bare requires it)
#
# Run via the wrapper, not directly:
#
#   docs/demo/render-claude.sh
#
# Output: docs/demo-claude.gif (referenced from the README).

Output docs/demo-claude.gif

Set Shell "bash"
Set FontSize 13
Set Width 1100
Set Height 720
Set Padding 16
Set TypingSpeed 50ms
Set Theme "Dracula"

Type `cd $AE_DEMO_DIR`
Sleep 200ms
Enter
Type `clear`
Enter
Sleep 400ms

# Show the starting file so the viewer knows what's about to change.
Type `cat hello.go`
Sleep 400ms
Enter
Sleep 1800ms
# Drive Claude Code. Non-interactive (-p), tools whitelisted to Bash so the
# agent has no choice but to drive ae from the shell. The agented skill
# (auto-loaded from ~/.agents/skills/agented/SKILL.md) teaches Claude how
# to use ae without us spelling it out in --append-system-prompt; the
# session loads it the same way any user-installed skill is loaded.
Type@30ms `claude -p --allow-dangerously-skip-permissions --allowed-tools "Bash" "Refactor hello.go: add a 'name string' parameter to hello() so it prints 'hello, <name>'. Update main() to call hello(\"agented\"). Use ae for all reads and edits."`
Sleep 600ms
Enter

# Claude does its thing. Output streams. The wall time depends on tool-call
# count (typically 4–6 calls: open, view, replace, replace, save, status).
# 90s buffers comfortably for the slow case.
Sleep 90s

# Show the result and the audit log so the viewer can see the agent's
# actor name, the state_token chain, and the per-edit timestamps.
Type `cat hello.go`
Sleep 400ms
Enter
Sleep 2500ms

Type `ae log hello.go --limit 10`
Sleep 400ms
Enter
Sleep 4000ms
