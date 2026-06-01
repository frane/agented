
// Package permissions installs / lists / uninstalls allow-rules for ae in
// each supported client's permissions config (Claude Code today; Codex when
// its config schema is documented). Mirrors the Target-driven design used
// by internal/skill so adding a new client is one append.
package permissions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultRules are the allow-patterns ae installs into a target's config.
// Covers `ae`, `./ae`, and absolute-path invocations.
// DefaultRules are the allow-patterns ae installs into a target's config.
// Covers `ae`, `./ae`, and absolute-path invocations.
var DefaultRules = []string{
	"Bash(ae *)",
	"Bash(ae)",
	"Bash(./ae *)",
	"Bash(./ae)",
}

// DefaultDenyRules are the built-in tools `ae permissions disable-internals`
// adds to permissions.deny. The point: when these are enabled, agents fall
// back to Read/Edit/Write out of training-data habit even with the agented
// skill installed, and the round-trip / token-economy story is lost.
//
// Only the file-mutation built-ins are in the default list. Read is in
// because the user's pain is exactly that — the agent skips `ae open`,
// goes to Read, and the file never registers in ae's workspace. Users
// who want Read available can edit the deny list manually after install.
var DefaultDenyRules = []string{
	"Read",
	"Edit",
	"Write",
	"NotebookEdit",
}

// DefaultStrictShellCmds is the shell-command deny list added by
// `--strict`. These are the obvious editor/reader fallbacks an agent
// reaches for when its built-in file tools are denied; without them in
// the deny list, the agent just routes around `disable-internals` via
// `Bash(cat *)` / `Bash(sed *)` / etc.
//
// Discovery + version-control commands stay allowed: grep, rg, find, ls,
// git. So do build tools (make, go, cargo, npm). The list is the editing
// and reading fallbacks only.
var DefaultStrictShellCmds = []string{
	"cat", "sed", "awk", "head", "tail",
	"vi", "vim", "nano", "less", "more",
	"ed", "emacs", "code",
}

// Scope discriminates global vs project install paths.
type Scope int

const (
	ScopeGlobal Scope = iota
	ScopeProject
)

// Status enumerates the per-target outcome of an install / uninstall run.
type Status string

const (
	StatusInstalled  Status = "installed"
	StatusUpdated    Status = "updated"
	StatusUnchanged  Status = "unchanged"
	StatusSkipped    Status = "skipped"
	StatusError      Status = "error"
	StatusWouldWrite Status = "would-write"
	StatusRemoved    Status = "removed"
	StatusNotFound   Status = "not-found"
)

// Result captures one target's outcome.
type Result struct {
	Target string
	Status Status
	Path   string
	Reason string
	Added  []string
}

// Target describes one client whose permission config ae knows how to write.
type Target struct {
	Name        string
	GlobalPath  func() (string, error)
	ProjectPath func(workspace string) string
	Detect      func() (bool, string)
	// Apply merges rules into the file at path; returns the rules that were
	// newly added (i.e. weren't already present). Targets implement their
	// own file format here so the caller doesn't care about JSON shape.
	Apply func(path string, rules []string) (added []string, err error)
	// Remove strips rules from the file at path; returns rules removed.
	// Idempotent: a missing file or absent rule is a no-op (zero removed).
	Remove func(path string, rules []string) (removed []string, err error)

	// DenyApply / DenyRemove are the deny-list pair, used by `ae permissions
	// disable-internals` to write Read/Edit/Write deny rules. Nil for clients
	// whose config schema has no deny mechanism; the CLI surfaces a clear
	// "not supported" skip message in that case rather than silently doing
	// nothing.
	DenyApply  func(path string, rules []string) (added []string, err error)
	DenyRemove func(path string, rules []string) (removed []string, err error)

	// StrictApply / StrictRemove are the --strict-mode pair: forbid the
	// shell-command fallbacks (cat, sed, awk, etc.) so the agent can't
	// route around the built-in tool denies via Bash. Per-target shape:
	// Claude → Bash(<cmd> *) entries in permissions.deny; Codex →
	// prefix_rule(... forbidden) in ~/.codex/rules/default.rules;
	// Gemini → policy rules with argsPattern matching the command name.
	// Nil for clients with no shell-deny mechanism.
	StrictApply  func(path string, cmds []string) (added []string, err error)
	StrictRemove func(path string, cmds []string) (removed []string, err error)
}

// Targets is the source-of-truth ordered list. Append (don't reorder) new
// clients here.
var Targets = []Target{
	claudeTarget,
	codexTarget,
	geminiTarget,
	openclawTarget,
}

// FindTarget returns the Target with the given name, or nil if unknown.
func FindTarget(name string) *Target {
	for i := range Targets {
		if Targets[i].Name == name {
			return &Targets[i]
		}
	}
	return nil
}

// claudeTarget — Claude Code uses a JSON schema with `permissions.allow` as
// a string array. Project scope writes to .claude/settings.local.json
// (machine-local, gitignored); global scope writes to ~/.claude/settings.json.
var claudeTarget = Target{
	Name: "claude",
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude: no home directory")
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	},
	ProjectPath: func(workspace string) string {
		return filepath.Join(workspace, ".claude", "settings.local.json")
	},
	Detect: func() (bool, string) {
		home, _ := os.UserHomeDir()
		if home != "" {
			if fi, err := os.Stat(filepath.Join(home, ".claude")); err == nil && fi.IsDir() {
				return true, ""
			}
		}
		if _, err := exec.LookPath("claude"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	Apply: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		existing := stringArray(perms["allow"])
		set := make(map[string]bool, len(existing))
		for _, r := range existing {
			set[r] = true
		}
		var added []string
		for _, r := range rules {
			if !set[r] {
				existing = append(existing, r)
				set[r] = true
				added = append(added, r)
			}
		}
		sort.Strings(existing)
		perms["allow"] = anyArray(existing)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return added, nil
	},
	Remove: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			return nil, nil
		}
		existing := stringArray(perms["allow"])
		toRemove := make(map[string]bool, len(rules))
		for _, r := range rules {
			toRemove[r] = true
		}
		var kept, removed []string
		for _, r := range existing {
			if toRemove[r] {
				removed = append(removed, r)
				continue
			}
			kept = append(kept, r)
		}
		perms["allow"] = anyArray(kept)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return removed, nil
	},
	// Claude reuses the same JSON shape for permissions.deny, just under a
	// different key. Same merge-and-sort logic; the deny entries (`Read`,
	// `Edit`, `Write`, etc.) live alongside the allow entries Claude already
	// has from `ae permissions install`.
	DenyApply: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		existing := stringArray(perms["deny"])
		set := make(map[string]bool, len(existing))
		for _, r := range existing {
			set[r] = true
		}
		var added []string
		for _, r := range rules {
			if !set[r] {
				existing = append(existing, r)
				set[r] = true
				added = append(added, r)
			}
		}
		sort.Strings(existing)
		perms["deny"] = anyArray(existing)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return added, nil
	},
	DenyRemove: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			return nil, nil
		}
		existing := stringArray(perms["deny"])
		toRemove := make(map[string]bool, len(rules))
		for _, r := range rules {
			toRemove[r] = true
		}
		var kept, removed []string
		for _, r := range existing {
			if toRemove[r] {
				removed = append(removed, r)
				continue
			}
			kept = append(kept, r)
		}
		perms["deny"] = anyArray(kept)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return removed, nil
	},
	// StrictApply for Claude: add Bash(<cmd> *) deny patterns to
	// permissions.deny. Same JSON shape as DenyApply, just a different rule
	// list. Idempotent.
	StrictApply: func(path string, cmds []string) ([]string, error) {
		rules := shellPatternRulesForClaude(cmds)
		root, err := readJSONObject(path)
		if err != nil {
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		existing := stringArray(perms["deny"])
		set := make(map[string]bool, len(existing))
		for _, r := range existing {
			set[r] = true
		}
		var added []string
		for _, r := range rules {
			if !set[r] {
				existing = append(existing, r)
				set[r] = true
				added = append(added, r)
			}
		}
		sort.Strings(existing)
		perms["deny"] = anyArray(existing)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return added, nil
	},
	StrictRemove: func(path string, cmds []string) ([]string, error) {
		rules := shellPatternRulesForClaude(cmds)
		root, err := readJSONObject(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			return nil, nil
		}
		existing := stringArray(perms["deny"])
		toRemove := make(map[string]bool, len(rules))
		for _, r := range rules {
			toRemove[r] = true
		}
		var kept, removed []string
		for _, r := range existing {
			if toRemove[r] {
				removed = append(removed, r)
				continue
			}
			kept = append(kept, r)
		}
		perms["deny"] = anyArray(kept)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return removed, nil
	},
}

func shellPatternRulesForClaude(cmds []string) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, "Bash("+c+" *)")
	}
	return out
}

// openclawTarget — OpenClaw manages permissions at the agent level rather
// than via a settings.json schema, so explicit ae permissions install is a
// no-op. Present so `--target all` and `--target openclaw` produce a clear
// skip message instead of an error.
var openclawTarget = Target{
	Name: "openclaw",
	GlobalPath: func() (string, error) {
		return "", nil
	},
	ProjectPath: func(workspace string) string {
		return ""
	},
	Detect: func() (bool, string) {
		return false, "permissions are managed at the agent level by OpenClaw itself; nothing to install"
	},
	Apply: func(path string, rules []string) ([]string, error) {
		return nil, nil
	},
	Remove: func(path string, rules []string) ([]string, error) {
		return nil, nil
	},
}

// codexTarget — Codex CLI exposes tool-level toggles via a `[tools]` table
// in ~/.codex/config.toml (e.g. `tools.web_search = true`). The schema is
// underdocumented for built-in file tools — Codex's primary edit primitive
// is `apply_patch`, and `tools.apply_patch = false` is the inferred deny
// path. The TOML key is accepted by `codex -c` parsing without error, but
// the runtime behavior (whether it actually disables the tool) isn't
// guaranteed by published docs. We write it anyway with a stderr warning,
// so users can verify in their own session.
//
// Allow rules (Apply) are a no-op because Codex doesn't have a per-shell-
// command allow list; sandbox_mode + approval_policy handle that, which
// is out of scope for this command.
var codexTarget = Target{
	Name: "codex",
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("codex: no home directory")
		}
		return filepath.Join(home, ".codex", "config.toml"), nil
	},
	ProjectPath: func(workspace string) string {
		return ""
	},
	Detect: func() (bool, string) {
		home, _ := os.UserHomeDir()
		if home != "" {
			if fi, err := os.Stat(filepath.Join(home, ".codex")); err == nil && fi.IsDir() {
				return true, ""
			}
		}
		if _, err := exec.LookPath("codex"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	Apply: func(path string, rules []string) ([]string, error) {
		// Codex's allow story is sandbox_mode + approval_policy, not
		// per-tool allow rules. Skip silently here so `permissions install`
		// remains a no-op for codex.
		return nil, nil
	},
	Remove: func(path string, rules []string) ([]string, error) {
		return nil, nil
	},
	DenyApply: func(path string, rules []string) ([]string, error) {
		// Map canonical names -> Codex's apply_patch (the single edit
		// primitive). Read/Edit/Write/NotebookEdit all collapse to it.
		// Codex doesn't have separate read/write tools at the public
		// surface — the agent reads via shell + writes via apply_patch,
		// so denying apply_patch is the relevant lever.
		_ = rules // accepted but unused; keys are fixed
		return upsertCodexToolDenies(path, []string{"apply_patch"})
	},
	DenyRemove: func(path string, rules []string) ([]string, error) {
		_ = rules
		return removeCodexToolDenies(path, []string{"apply_patch"})
	},
	// StrictApply for Codex: write ~/.codex/rules/default.rules with
	// prefix_rule(... forbidden) entries for each shell command. This is
	// Codex's documented shell-command sandbox mechanism. The rules file
	// is independent of config.toml; the path argument here is ignored
	// (the .rules path is resolved relative to ~/.codex).
	StrictApply: func(_ string, cmds []string) ([]string, error) {
		rulesPath, err := codexRulesPath()
		if err != nil {
			return nil, err
		}
		body := buildCodexRulesFile(cmds)
		if existing, _ := os.ReadFile(rulesPath); string(existing) == body {
			return nil, nil
		}
		if err := os.MkdirAll(filepath.Dir(rulesPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(rulesPath, []byte(body), 0o644); err != nil {
			return nil, err
		}
		return cmds, nil
	},
	StrictRemove: func(_ string, cmds []string) ([]string, error) {
		rulesPath, err := codexRulesPath()
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(rulesPath); err != nil {
			return nil, nil
		}
		if err := os.Remove(rulesPath); err != nil {
			return nil, err
		}
		return cmds, nil
	},
}

func codexRulesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("codex: no home directory")
	}
	return filepath.Join(home, ".codex", "rules", "agented-strict.rules"), nil
}

// buildCodexRulesFile renders the Starlark .rules content. Each forbidden
// command becomes a prefix_rule() call; comments at the top point at
// docs/permissions.md for context.
func buildCodexRulesFile(cmds []string) string {
	var sb strings.Builder
	sb.WriteString("# Written by `ae permissions disable-internals --strict`.\n")
	sb.WriteString("# Forbids the shell-command fallbacks the agent might reach for\n")
	sb.WriteString("# instead of driving `ae`. Remove with the matching\n")
	sb.WriteString("# `ae permissions enable-internals --strict`.\n\n")
	for _, c := range cmds {
		sb.WriteString("prefix_rule(\n")
		fmt.Fprintf(&sb, "    pattern = [%q],\n", c)
		sb.WriteString("    decision = \"forbidden\",\n")
		fmt.Fprintf(&sb, "    justification = \"agented skill installed: use `ae` instead of `%s`\",\n", c)
		sb.WriteString(")\n\n")
	}
	return sb.String()
}

// upsertCodexToolDenies adds `<tool> = false` lines under a `[tools]` table
// in path, creating the table if absent. Returns the names that were newly
// added (skipping any already set to false). Idempotent.
func upsertCodexToolDenies(path string, tools []string) ([]string, error) {
	body, err := readOrEmpty(path)
	if err != nil {
		return nil, err
	}
	current := body
	var added []string
	for _, t := range tools {
		newBody, didAdd := insertCodexToolFalse(current, t)
		if didAdd {
			added = append(added, t)
			current = newBody
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return added, os.WriteFile(path, current, 0o644)
}

func removeCodexToolDenies(path string, tools []string) ([]string, error) {
	body, err := readOrEmpty(path)
	if err != nil || len(body) == 0 {
		return nil, err
	}
	current := body
	var removed []string
	for _, t := range tools {
		newBody, didRemove := stripCodexToolFalse(current, t)
		if didRemove {
			removed = append(removed, t)
			current = newBody
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	return removed, os.WriteFile(path, current, 0o644)
}

func insertCodexToolFalse(body []byte, tool string) ([]byte, bool) {
	line := tool + " = false"
	// Already present (anywhere in [tools] table)?
	if bytes.Contains(body, []byte(line)) {
		return body, false
	}
	if idx := bytes.Index(body, []byte("[tools]")); idx >= 0 {
		// Append the line after the [tools] header line.
		nl := bytes.IndexByte(body[idx:], '\n')
		if nl < 0 {
			return append(append([]byte{}, body...), []byte("\n"+line+"\n")...), true
		}
		ins := idx + nl + 1
		out := make([]byte, 0, len(body)+len(line)+1)
		out = append(out, body[:ins]...)
		out = append(out, []byte(line+"\n")...)
		out = append(out, body[ins:]...)
		return out, true
	}
	// No [tools] table yet; append a new one at the end.
	prefix := body
	if len(prefix) > 0 && prefix[len(prefix)-1] != '\n' {
		prefix = append(prefix, '\n')
	}
	if len(prefix) > 0 {
		prefix = append(prefix, '\n')
	}
	prefix = append(prefix, []byte("[tools]\n"+line+"\n")...)
	return prefix, true
}

func stripCodexToolFalse(body []byte, tool string) ([]byte, bool) {
	line := []byte(tool + " = false")
	idx := bytes.Index(body, line)
	if idx < 0 {
		return body, false
	}
	// Strip the line including its trailing newline (if any).
	end := idx + len(line)
	if end < len(body) && body[end] == '\n' {
		end++
	}
	out := make([]byte, 0, len(body)-(end-idx))
	out = append(out, body[:idx]...)
	out = append(out, body[end:]...)
	return out, true
}

// geminiTarget — Gemini CLI uses a Policy Engine with TOML rules at
// ~/.gemini/policies/*.toml. Each rule has toolName + decision ("deny" |
// "allow" | "ask_user") + priority. Allow-rules aren't needed for ae from
// Bash (Gemini's run_shell_command default policy lets it through), so
// Apply/Remove are no-ops. The deny pair is what does the work — writes
// (and removes) ~/.gemini/policies/agented-deny.toml with deny rules for
// the file-mutation built-ins.
var geminiTarget = Target{
	Name: "gemini",
	// Gemini's policy engine reads from ~/.gemini/policies/*.toml. There's
	// no project-scope equivalent — policies are user-level. We surface the
	// global policy file path here so resolvePath returns it under
	// scope=global; project-scope is intentionally empty so callers see a
	// clean "not supported in project scope" skip instead of a silent miss.
	GlobalPath: func() (string, error) {
		return geminiPolicyPath()
	},
	ProjectPath: func(workspace string) string {
		return ""
	},
	Detect: func() (bool, string) {
		home, _ := os.UserHomeDir()
		if home != "" {
			if fi, err := os.Stat(filepath.Join(home, ".gemini")); err == nil && fi.IsDir() {
				return true, ""
			}
		}
		return false, "no ~/.gemini directory; install with `gemini extensions install ...`"
	},
	Apply: func(path string, rules []string) ([]string, error) {
		// Allow rules aren't needed for the agented Bash flow.
		return nil, nil
	},
	Remove: func(path string, rules []string) ([]string, error) {
		return nil, nil
	},
	DenyApply: func(_ string, rules []string) ([]string, error) {
		policyPath, err := geminiPolicyPath()
		if err != nil {
			return nil, err
		}
		// Map ae-canonical names (Read/Edit/Write/NotebookEdit) to Gemini's
		// built-in tool names. Anything not in the map is passed through as
		// the user's own toolName, so callers can extend the list.
		mapped := mapDenyRulesForGemini(rules)
		body := buildGeminiDenyTOML(mapped)
		// Idempotent: if the file already has this body, no-op.
		if existing, _ := os.ReadFile(policyPath); string(existing) == body {
			return nil, nil
		}
		if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(policyPath, []byte(body), 0o644); err != nil {
			return nil, err
		}
		return mapped, nil
	},
	DenyRemove: func(_ string, rules []string) ([]string, error) {
		policyPath, err := geminiPolicyPath()
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(policyPath); err != nil {
			return nil, nil
		}
		// Single-file model: removing any rule means removing all of them.
		// Mirrors Apply, which writes the full set in one shot.
		mapped := mapDenyRulesForGemini(rules)
		if err := os.Remove(policyPath); err != nil {
			return nil, err
		}
		return mapped, nil
	},
	// StrictApply for Gemini: write a sibling policy file with
	// run_shell_command + argsPattern rules for each forbidden command.
	// Separate file so DenyApply/DenyRemove stay decoupled from the
	// strict additions.
	StrictApply: func(_ string, cmds []string) ([]string, error) {
		strictPath, err := geminiStrictPolicyPath()
		if err != nil {
			return nil, err
		}
		body := buildGeminiStrictTOML(cmds)
		if existing, _ := os.ReadFile(strictPath); string(existing) == body {
			return nil, nil
		}
		if err := os.MkdirAll(filepath.Dir(strictPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(strictPath, []byte(body), 0o644); err != nil {
			return nil, err
		}
		return cmds, nil
	},
	StrictRemove: func(_ string, cmds []string) ([]string, error) {
		strictPath, err := geminiStrictPolicyPath()
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(strictPath); err != nil {
			return nil, nil
		}
		if err := os.Remove(strictPath); err != nil {
			return nil, err
		}
		return cmds, nil
	},
}

func geminiStrictPolicyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("gemini: no home directory")
	}
	return filepath.Join(home, ".gemini", "policies", "agented-strict.toml"), nil
}

func buildGeminiStrictTOML(cmds []string) string {
	var sb strings.Builder
	sb.WriteString("# Written by `ae permissions disable-internals --strict`.\n")
	sb.WriteString("# Forbids the shell-command fallbacks for run_shell_command.\n")
	sb.WriteString("# Remove with `ae permissions enable-internals --strict`.\n\n")
	for _, c := range cmds {
		sb.WriteString("[[rule]]\n")
		sb.WriteString("toolName = \"run_shell_command\"\n")
		fmt.Fprintf(&sb, "argsPattern = \"^%s(\\\\s|$)\"\n", c)
		sb.WriteString("decision = \"deny\"\n")
		sb.WriteString("priority = 110\n")
		fmt.Fprintf(&sb, "denyMessage = \"agented skill installed: use `ae` instead of `%s`\"\n\n", c)
	}
	return sb.String()
}

func geminiPolicyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("gemini: no home directory")
	}
	return filepath.Join(home, ".gemini", "policies", "agented-deny.toml"), nil
}

// mapDenyRulesForGemini translates the ae-canonical deny rule names
// (Read/Edit/Write/NotebookEdit) to Gemini's built-in tool names. Any rule
// not in the map is passed through as-is so users can deny custom tools.
func mapDenyRulesForGemini(rules []string) []string {
	canon := map[string]string{
		"Read":         "read_file",
		"Edit":         "edit",
		"Write":        "write_file",
		"NotebookEdit": "edit", // Gemini has no notebook-specific tool; collapse to edit
	}
	seen := make(map[string]bool, len(rules))
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		mapped := r
		if v, ok := canon[r]; ok {
			mapped = v
		}
		if seen[mapped] {
			continue
		}
		seen[mapped] = true
		out = append(out, mapped)
	}
	return out
}

func buildGeminiDenyTOML(tools []string) string {
	var sb strings.Builder
	sb.WriteString("# Written by `ae permissions disable-internals`. Forces Gemini\n")
	sb.WriteString("# to use ae (via run_shell_command) for file edits instead of the\n")
	sb.WriteString("# built-in file tools. Remove with `ae permissions enable-internals`.\n\n")
	for _, t := range tools {
		sb.WriteString("[[rule]]\n")
		fmt.Fprintf(&sb, "toolName = %q\n", t)
		sb.WriteString("decision = \"deny\"\n")
		sb.WriteString("priority = 100\n")
		sb.WriteString("denyMessage = \"agented skill is installed; use `ae` from run_shell_command instead\"\n\n")
	}
	return sb.String()
}


// readJSONObject reads a JSON object from path. Returns an empty map (no
// error) if the file is absent. Errors only on IO/parse failure when the
// file does exist.
func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var out map[string]any
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func writeJSONObject(path string, v map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}

func stringArray(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func anyArray(s []string) []any {
	out := make([]any, 0, len(s))
	for _, x := range s {
		out = append(out, x)
	}
	return out
}

// InstallOptions parameterizes Install.
type InstallOptions struct {
	Selected  string // "all" or a specific target name
	Scope     Scope
	Workspace string   // required for ScopeProject
	Rules     []string // empty -> DefaultRules
	DryRun    bool

	// Strict, when true on a disable-internals call, also adds the
	// shell-command denies from DefaultStrictShellCmds (cat, sed, awk,
	// vi, ...) via each target's StrictApply. The CLI surfaces this as
	// `--strict` on `ae permissions disable-internals`.
	Strict bool
}

// Install adds rules to the resolved target file(s) and returns one Result
// per target that participated.
func Install(opts InstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultRules
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, applyOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		t := &Targets[i]
		// Detection determines whether to write to non-explicit targets under
		// --target all. agents-style always-write doesn't apply here.
		if det, reason := t.Detect(); !det {
			results = append(results, Result{Target: t.Name, Status: StatusSkipped, Reason: reason})
			continue
		}
		results = append(results, applyOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
	}
	return results, nil
}

func applyOne(t *Target, scope Scope, workspace string, rules []string, dryRun bool) Result {
	if t.Name == "openclaw" {
		return Result{Target: t.Name, Status: StatusSkipped,
			Reason: "permissions are managed at the agent level by OpenClaw itself; skipping"}
	}
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if dryRun {
		// Compute would-add by reading without writing.
		existing, _ := readJSONObject(path)
		perms, _ := existing["permissions"].(map[string]any)
		var allow []string
		if perms != nil {
			allow = stringArray(perms["allow"])
		}
		set := make(map[string]bool, len(allow))
		for _, r := range allow {
			set[r] = true
		}
		var would []string
		for _, r := range rules {
			if !set[r] {
				would = append(would, r)
			}
		}
		status := StatusUnchanged
		if len(would) > 0 {
			status = StatusWouldWrite
		}
		return Result{Target: t.Name, Status: status, Path: path, Added: would}
	}
	_, statErr := os.Stat(path)
	preExisted := statErr == nil
	added, err := t.Apply(path, rules)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	switch {
	case len(added) == 0:
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	case preExisted:
		return Result{Target: t.Name, Status: StatusUpdated, Path: path, Added: added}
	default:
		return Result{Target: t.Name, Status: StatusInstalled, Path: path, Added: added}
	}
}

func resolvePath(t *Target, scope Scope, workspace string) (string, error) {
	switch scope {
	case ScopeGlobal:
		if t.GlobalPath == nil {
			return "", nil
		}
		return t.GlobalPath()
	case ScopeProject:
		if t.ProjectPath == nil {
			return "", nil
		}
		return t.ProjectPath(workspace), nil
	}
	return "", fmt.Errorf("unknown scope %d", scope)
}

// ListEntry describes the install state of one target for `ae permissions list`.
type ListEntry struct {
	Target    string
	Detected  string
	Installed bool
	Path      string
	Rules     []string
}

// List inspects every Target's resolved file and reports installed allow rules.
func List(scope Scope, workspace string) []ListEntry {
	out := make([]ListEntry, 0, len(Targets))
	for i := range Targets {
		t := &Targets[i]
		entry := ListEntry{Target: t.Name}
		if det, _ := t.Detect(); det {
			entry.Detected = "yes"
		} else {
			entry.Detected = "no"
		}
		path, err := resolvePath(t, scope, workspace)
		if err != nil || path == "" {
			entry.Path = "(project only)"
			out = append(out, entry)
			continue
		}
		entry.Path = path
		root, err := readJSONObject(path)
		if err == nil {
			if perms, ok := root["permissions"].(map[string]any); ok {
				rules := stringArray(perms["allow"])
				ours := filterAERules(rules)
				if len(ours) > 0 {
					entry.Installed = true
					entry.Rules = ours
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// filterAERules returns just the rules from `existing` that match our
// DefaultRules — used by List so we don't report unrelated user rules.
func filterAERules(existing []string) []string {
	want := make(map[string]bool, len(DefaultRules))
	for _, r := range DefaultRules {
		want[r] = true
	}
	var out []string
	for _, r := range existing {
		if want[r] {
			out = append(out, r)
		}
	}
	return out
}

// UninstallOptions parameterizes Uninstall.
type UninstallOptions struct {
	Selected  string
	Scope     Scope
	Workspace string
	Rules     []string // empty -> DefaultRules
	DryRun    bool

	// Strict mirrors InstallOptions.Strict: when true, also removes the
	// shell-command deny additions made by `--strict`. Surfaced as
	// `--strict` on `ae permissions enable-internals`.
	Strict bool
}

// Uninstall removes ae's allow rules from each target's config.
func Uninstall(opts UninstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultRules
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, removeOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		results = append(results, removeOne(&Targets[i], opts.Scope, opts.Workspace, rules, opts.DryRun))
	}
	return results, nil
}

func removeOne(t *Target, scope Scope, workspace string, rules []string, dryRun bool) Result {
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if _, err := os.Stat(path); err != nil {
		return Result{Target: t.Name, Status: StatusNotFound, Path: path}
	}
	if dryRun {
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path}
	}
	removed, err := t.Remove(path, rules)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	if len(removed) == 0 {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	}
	return Result{Target: t.Name, Status: StatusRemoved, Path: path, Added: removed}
}

// InstallDenies adds the deny-list rules (DefaultDenyRules unless opts.Rules
// overrides) to each target's permissions.deny array. Targets without a
// DenyApply are skipped with a clear "not supported" reason — Gemini and
// Codex don't have public deny-list schemas today; this leaves room for
// them to fill in DenyApply when the schema is documented without changing
// the call surface here.
func InstallDenies(opts InstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultDenyRules
	}
	shellCmds := []string{}
	if opts.Strict {
		shellCmds = DefaultStrictShellCmds
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, denyApplyOne(t, opts.Scope, opts.Workspace, rules, shellCmds, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		t := &Targets[i]
		if det, reason := t.Detect(); !det {
			results = append(results, Result{Target: t.Name, Status: StatusSkipped, Reason: reason})
			continue
		}
		results = append(results, denyApplyOne(t, opts.Scope, opts.Workspace, rules, shellCmds, opts.DryRun))
	}
	return results, nil
}

// UninstallDenies removes the deny-list rules from each target.
func UninstallDenies(opts UninstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultDenyRules
	}
	shellCmds := []string{}
	if opts.Strict {
		shellCmds = DefaultStrictShellCmds
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, denyRemoveOne(t, opts.Scope, opts.Workspace, rules, shellCmds, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		results = append(results, denyRemoveOne(&Targets[i], opts.Scope, opts.Workspace, rules, shellCmds, opts.DryRun))
	}
	return results, nil
}

func denyApplyOne(t *Target, scope Scope, workspace string, rules []string, shellCmds []string, dryRun bool) Result {
	if t.DenyApply == nil {
		return Result{Target: t.Name, Status: StatusSkipped,
			Reason: t.Name + ": deny-list schema not yet known; track in github.com/frane/agented for support"}
	}
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if dryRun {
		preview := make([]string, 0, len(rules)+len(shellCmds))
		preview = append(preview, rules...)
		for _, c := range shellCmds {
			preview = append(preview, "shell:"+c)
		}
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path, Added: preview}
	}
	added, err := t.DenyApply(path, rules)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	// Strict: also push shell-command denies through the per-target StrictApply.
	if len(shellCmds) > 0 && t.StrictApply != nil {
		strictAdded, serr := t.StrictApply(path, shellCmds)
		if serr != nil {
			return Result{Target: t.Name, Status: StatusError, Path: path, Reason: serr.Error()}
		}
		for _, c := range strictAdded {
			added = append(added, "shell:"+c)
		}
	}
	if len(added) == 0 {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	}
	return Result{Target: t.Name, Status: StatusInstalled, Path: path, Added: added}
}

func denyRemoveOne(t *Target, scope Scope, workspace string, rules []string, shellCmds []string, dryRun bool) Result {
	if t.DenyRemove == nil {
		return Result{Target: t.Name, Status: StatusSkipped,
			Reason: t.Name + ": deny-list schema not yet known; track in github.com/frane/agented for support"}
	}
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if _, err := os.Stat(path); err != nil && t.StrictRemove == nil {
		return Result{Target: t.Name, Status: StatusNotFound, Path: path}
	}
	if dryRun {
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path}
	}
	var removed []string
	if _, statErr := os.Stat(path); statErr == nil {
		r, derr := t.DenyRemove(path, rules)
		if derr != nil {
			return Result{Target: t.Name, Status: StatusError, Path: path, Reason: derr.Error()}
		}
		removed = append(removed, r...)
	}
	if len(shellCmds) > 0 && t.StrictRemove != nil {
		strictRemoved, serr := t.StrictRemove(path, shellCmds)
		if serr != nil {
			return Result{Target: t.Name, Status: StatusError, Path: path, Reason: serr.Error()}
		}
		for _, c := range strictRemoved {
			removed = append(removed, "shell:"+c)
		}
	}
	if len(removed) == 0 {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	}
	return Result{Target: t.Name, Status: StatusRemoved, Path: path, Added: removed}
}

func readOrEmpty(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}
