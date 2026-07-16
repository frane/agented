// Package agents is the single source of truth for every agent integration ae
// supports — Claude Code, Claude Desktop, Codex, Cursor, Gemini, OpenClaw, and
// the canonical `~/.agents/` location. Each Agent declares both its skill
// surface (where SKILL.md goes) and its MCP surface (where the agented MCP
// server entry goes), plus the shared detection logic.
//
// The skill and mcpinstall packages each build their target slices from this
// list. Adding a new client is a one-place change here, not two-place.
package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Agent is a unified target descriptor. Skill-only fields and MCP-only fields
// are nil-able; the consuming package skips agents that don't expose its
// surface. Detect is shared.
type Agent struct {
	// Name is the user-facing target name (--target=<name>).
	Name string

	// AlwaysWrite, when true, makes this agent considered under
	// `--target all` even when Detect returns false. Reserved for the
	// canonical "agents" pseudo-target.
	AlwaysWrite bool

	// Detect reports whether the agent appears installed on this system.
	// Returns (true, "") when found; (false, reason) otherwise.
	Detect func() (bool, string)

	// Skill-side surfaces (nil = no skill integration for this agent).
	SkillGlobal      func() (string, error)
	SkillProject     func(workspace string) string
	SkillPostInstall func(path, version string) error

	// MCP-side surfaces (nil = no MCP integration for this agent).
	MCPGlobal  func() (string, error)
	MCPProject func(workspace string) string
	MCPApply   func(path, serverName string, server map[string]any) (changed bool, err error)
	MCPRemove  func(path, serverName string) (changed bool, err error)
	MCPInspect func(path, serverName string) (map[string]any, error)

	// MCPServerExtras are agent-specific keys merged into the canonical
	// server entry on install (never overriding canonical keys). Used for
	// per-client auto-approval knobs, e.g. Gemini's `trust: true`, which
	// skips tool-call confirmations for this server. Nil for clients with
	// no such mechanism (Claude Code uses permission rules instead — see
	// internal/permissions; Codex approval is a global policy).
	MCPServerExtras map[string]any
}

// All is the source-of-truth ordered list. Order matters for summary output.
// Append (don't reorder) when adding a new agent.
var All = []Agent{
	agentsCanonical,
	claudeCode,
	claudeDesktop,
	codex,
	cursor,
	gemini,
	openclaw,
}

// Find returns the agent with the given name, or nil if unknown.
func Find(name string) *Agent {
	for i := range All {
		if All[i].Name == name {
			return &All[i]
		}
	}
	return nil
}

// HasSkill reports whether this agent exposes a skill install surface.
func (a Agent) HasSkill() bool {
	return a.SkillGlobal != nil || a.SkillProject != nil
}

// HasMCP reports whether this agent exposes an MCP-config install surface.
func (a Agent) HasMCP() bool {
	return a.MCPGlobal != nil || a.MCPProject != nil
}

// agentsCanonical — the spec-canonical ~/.agents/skills/ location, picked up
// by any agent that follows the cross-client convention. No CLI binary, no
// detection: AlwaysWrite forces it under --target all.
var agentsCanonical = Agent{
	Name:        "agents",
	AlwaysWrite: true,
	Detect: func() (bool, string) {
		return false, ""
	},
	SkillGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("agents: no home directory")
		}
		return filepath.Join(home, ".agents", "skills", "agented", "SKILL.md"), nil
	},
	SkillProject: func(workspace string) string {
		return filepath.Join(workspace, ".agents", "skills", "agented", "SKILL.md")
	},
}

// claudeCode — Claude Code (the CLI). Same home dir as Claude Desktop, so
// Detect overlaps; mcpinstall keeps them separate for distinct config paths.
var claudeCode = Agent{
	Name: "claude",
	Detect: func() (bool, string) {
		if pathIsFile(homeSubdir(".claude.json")) {
			return true, ""
		}
		if pathIsDir(homeSubdir(".claude")) {
			return true, ""
		}
		if _, err := exec.LookPath("claude"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	SkillGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude: no home directory")
		}
		return filepath.Join(home, ".claude", "skills", "agented", "SKILL.md"), nil
	},
	SkillProject: func(workspace string) string {
		return filepath.Join(workspace, ".claude", "skills", "agented", "SKILL.md")
	},
	MCPGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude: no home directory")
		}
		return filepath.Join(home, ".claude.json"), nil
	},
	MCPProject: func(workspace string) string {
		return filepath.Join(workspace, ".mcp.json")
	},
	MCPApply:   jsonMCPApply,
	MCPRemove:  jsonMCPRemove,
	MCPInspect: jsonMCPInspect,
}

// claudeDesktop — Claude Desktop app, MCP-only (skills not supported by the
// desktop app today; for skills the user installs via Claude Code anyway).
var claudeDesktop = Agent{
	Name: "claude-desktop",
	Detect: func() (bool, string) {
		home, _ := os.UserHomeDir()
		if home == "" {
			return false, "no home directory"
		}
		dir := filepath.Dir(claudeDesktopConfigPath(home))
		if pathIsDir(dir) {
			return true, ""
		}
		return false, "no Claude Desktop config dir"
	},
	MCPGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude-desktop: no home directory")
		}
		return claudeDesktopConfigPath(home), nil
	},
	MCPApply:   jsonMCPApply,
	MCPRemove:  jsonMCPRemove,
	MCPInspect: jsonMCPInspect,
}

// codex — OpenAI Codex CLI. MCP config lives in TOML at ~/.codex/config.toml,
// distinct from the JSON-shape every other client uses, so this agent has its
// own apply/remove/inspect helpers.
var codex = Agent{
	Name: "codex",
	Detect: func() (bool, string) {
		if pathIsDir(homeSubdir(".codex")) {
			return true, ""
		}
		if _, err := exec.LookPath("codex"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	SkillGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("codex: no home directory")
		}
		return filepath.Join(home, ".codex", "skills", "agented", "SKILL.md"), nil
	},
	SkillProject: func(_ string) string {
		// Codex reads project skills only from .agents/skills/, which the
		// agents canonical target handles.
		return ""
	},
	MCPGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("codex: no home directory")
		}
		return filepath.Join(home, ".codex", "config.toml"), nil
	},
	MCPApply:   tomlMCPApply,
	MCPRemove:  tomlMCPRemove,
	MCPInspect: tomlMCPInspect,
}

// cursor — Cursor IDE. Project-scoped only (Cursor has no global skills dir).
// MCP config in Cursor lives in <workspace>/.cursor/mcp.json or the global
// Cursor settings; we target the project file.
var cursor = Agent{
	Name: "cursor",
	Detect: func() (bool, string) {
		cwd, _ := os.Getwd()
		if cwd != "" && pathIsDir(filepath.Join(cwd, ".cursor")) {
			return true, ""
		}
		if _, err := exec.LookPath("cursor"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	SkillProject: func(workspace string) string {
		return filepath.Join(workspace, ".cursor", "skills", "agented", "SKILL.md")
	},
	MCPProject: func(workspace string) string {
		return filepath.Join(workspace, ".cursor", "mcp.json")
	},
	MCPApply:   jsonMCPApply,
	MCPRemove:  jsonMCPRemove,
	MCPInspect: jsonMCPInspect,
}

// gemini — Google Gemini CLI. The skill side installs as a Gemini extension
// (GEMINI.md plus a sibling gemini-extension.json declaring the extension);
// the MCP side writes to ~/.gemini/settings.json.
var gemini = Agent{
	Name: "gemini",
	Detect: func() (bool, string) {
		if pathIsDir(homeSubdir(".gemini")) {
			return true, ""
		}
		if _, err := exec.LookPath("gemini"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	SkillGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("gemini: no home directory")
		}
		return filepath.Join(home, ".gemini", "extensions", "agented", "GEMINI.md"), nil
	},
	SkillPostInstall: func(path, version string) error {
		manifest := filepath.Join(filepath.Dir(path), "gemini-extension.json")
		body := fmt.Sprintf(`{
  "name": "agented",
  "version": %q,
  "contextFileName": %q,
  "mcpServers": {
    "agented": {
      "command": "ae",
      "args": ["serve"],
      "trust": true
    }
  }
}
`, version, filepath.Base(path))
		return os.WriteFile(manifest, []byte(body), 0o644)
	},
	MCPGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("gemini: no home directory")
		}
		return filepath.Join(home, ".gemini", "settings.json"), nil
	},
	MCPProject: func(workspace string) string {
		return filepath.Join(workspace, ".gemini", "settings.json")
	},
	MCPApply:   jsonMCPApply,
	MCPRemove:  jsonMCPRemove,
	MCPInspect: jsonMCPInspect,
	// trust:true is Gemini's documented per-server auto-approval: tool
	// calls from this server skip the confirmation prompt.
	MCPServerExtras: map[string]any{"trust": true},
}

// openclaw — OpenClaw assistant. Skills under ~/.openclaw/workspace/skills/.
// No MCP integration today.
var openclaw = Agent{
	Name: "openclaw",
	Detect: func() (bool, string) {
		if pathIsDir(homeSubdir(".openclaw")) {
			return true, ""
		}
		if _, err := exec.LookPath("openclaw"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	SkillGlobal: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("openclaw: no home directory")
		}
		return filepath.Join(home, ".openclaw", "workspace", "skills", "agented", "SKILL.md"), nil
	},
}

// claudeDesktopConfigPath returns the per-OS config location for Claude Desktop.
func claudeDesktopConfigPath(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata != "" {
			return filepath.Join(appdata, "Claude", "claude_desktop_config.json")
		}
		return filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

func homeSubdir(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, name)
}

func pathIsDir(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return fi.IsDir()
}

func pathIsFile(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

// ---- JSON-shape MCP helpers (Claude, Cursor, Gemini, Claude Desktop) ----
//
// Every JSON-config client stores MCP servers under root.mcpServers.<name>.
// Apply/Remove/Inspect share the same shape; the agent only varies the path.

func jsonMCPApply(path, serverName string, server map[string]any) (bool, error) {
	root, err := readJSONObject(path)
	if err != nil {
		return false, err
	}
	mcp, _ := root["mcpServers"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	existing, _ := mcp[serverName].(map[string]any)
	if jsonEqual(existing, server) {
		return false, nil
	}
	mcp[serverName] = server
	root["mcpServers"] = mcp
	return true, writeJSONObject(path, root)
}

func jsonMCPRemove(path, serverName string) (bool, error) {
	root, err := readJSONObject(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	mcp, _ := root["mcpServers"].(map[string]any)
	if mcp == nil {
		return false, nil
	}
	if _, present := mcp[serverName]; !present {
		return false, nil
	}
	delete(mcp, serverName)
	root["mcpServers"] = mcp
	return true, writeJSONObject(path, root)
}

func jsonMCPInspect(path, serverName string) (map[string]any, error) {
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	mcp, _ := root["mcpServers"].(map[string]any)
	if mcp == nil {
		return nil, nil
	}
	entry, _ := mcp[serverName].(map[string]any)
	if entry == nil {
		return nil, nil
	}
	return entry, nil
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
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

func jsonEqual(a, b map[string]any) bool {
	if a == nil || b == nil {
		return false
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// ---- TOML-shape MCP helpers (Codex CLI) ----
//
// Codex stores MCP servers as `[mcp_servers.<name>]` sections. We hand-roll a
// minimal section editor so the project stays at three runtime deps.

func tomlMCPApply(path, serverName string, server map[string]any) (bool, error) {
	body, err := readOrEmpty(path)
	if err != nil {
		return false, err
	}
	section := tomlSectionFor(serverName, server)
	updated, changed := upsertTOMLSection(body, serverName, section)
	if !changed {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, updated, 0o644)
}

func tomlMCPRemove(path, serverName string) (bool, error) {
	body, err := readOrEmpty(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	updated, removed := removeTOMLSection(body, serverName)
	if !removed {
		return false, nil
	}
	return true, os.WriteFile(path, updated, 0o644)
}

func tomlMCPInspect(path, serverName string) (map[string]any, error) {
	body, err := readOrEmpty(path)
	if err != nil {
		return nil, err
	}
	entry, present := readTOMLSection(body, serverName)
	if !present {
		return nil, nil
	}
	return entry, nil
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

func tomlSectionFor(name string, server map[string]any) string {
	cmd, _ := server["command"].(string)
	argsAny, _ := server["args"].([]any)
	var sb strings.Builder
	sb.WriteString("[mcp_servers.")
	sb.WriteString(name)
	sb.WriteString("]\n")
	sb.WriteString("command = ")
	sb.WriteString(tomlString(cmd))
	sb.WriteByte('\n')
	if len(argsAny) > 0 {
		sb.WriteString("args = [")
		for i, a := range argsAny {
			if i > 0 {
				sb.WriteString(", ")
			}
			s, _ := a.(string)
			sb.WriteString(tomlString(s))
		}
		sb.WriteString("]\n")
	}
	return sb.String()
}

func tomlString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func upsertTOMLSection(body []byte, name, section string) ([]byte, bool) {
	header := "[mcp_servers." + name + "]"
	start, end := tomlSectionRange(body, header)
	if start < 0 {
		out := bytes.Clone(body)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, []byte(section)...)
		return out, true
	}
	existing := string(body[start:end])
	if existing == section {
		return body, false
	}
	out := make([]byte, 0, len(body)+len(section))
	out = append(out, body[:start]...)
	out = append(out, []byte(section)...)
	out = append(out, body[end:]...)
	return out, true
}

func removeTOMLSection(body []byte, name string) ([]byte, bool) {
	header := "[mcp_servers." + name + "]"
	start, end := tomlSectionRange(body, header)
	if start < 0 {
		return body, false
	}
	trim := end
	if trim < len(body) && body[trim] == '\n' {
		trim++
	}
	out := make([]byte, 0, len(body))
	out = append(out, body[:start]...)
	out = append(out, body[trim:]...)
	return out, true
}

func tomlSectionRange(body []byte, header string) (int, int) {
	lines := bytes.Split(body, []byte("\n"))
	offset := 0
	for i, line := range lines {
		if string(bytes.TrimRight(line, " \t")) == header {
			start := offset
			end := offset + len(line) + 1
			for j := i + 1; j < len(lines); j++ {
				next := lines[j]
				if isTOMLSectionHeader(next) {
					return start, end
				}
				end += len(next) + 1
			}
			if end > len(body) {
				end = len(body)
			}
			return start, end
		}
		offset += len(line) + 1
	}
	return -1, -1
}

func isTOMLSectionHeader(line []byte) bool {
	t := bytes.TrimSpace(line)
	return len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']'
}

func readTOMLSection(body []byte, name string) (map[string]any, bool) {
	header := "[mcp_servers." + name + "]"
	start, end := tomlSectionRange(body, header)
	if start < 0 {
		return nil, false
	}
	out := map[string]any{}
	chunk := body[start:end]
	for _, raw := range bytes.Split(chunk, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "command":
			s, err := unquoteTOMLString(val)
			if err == nil {
				out["command"] = s
			}
		case "args":
			arr, err := parseTOMLStringArray(val)
			if err == nil {
				out["args"] = anyArrayFromStrings(arr)
			}
		}
	}
	return out, true
}

func unquoteTOMLString(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", errors.New("not a quoted string")
	}
	inner := s[1 : len(s)-1]
	var sb strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			switch inner[i+1] {
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteByte(inner[i+1])
			}
			i++
			continue
		}
		sb.WriteByte(inner[i])
	}
	return sb.String(), nil
}

func parseTOMLStringArray(s string) ([]string, error) {
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, errors.New("not an array")
	}
	body := strings.TrimSpace(s[1 : len(s)-1])
	if body == "" {
		return nil, nil
	}
	parts := splitTOMLArray(body)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		v, err := unquoteTOMLString(p)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func splitTOMLArray(s string) []string {
	var out []string
	var cur strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && (i == 0 || s[i-1] != '\\'):
			inStr = !inStr
			cur.WriteByte(c)
		case c == ',' && !inStr:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func anyArrayFromStrings(s []string) []any {
	out := make([]any, 0, len(s))
	for _, x := range s {
		out = append(out, x)
	}
	return out
}
