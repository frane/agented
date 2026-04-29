package mcpinstall

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// codexTarget — Codex CLI stores MCP servers in ~/.codex/config.toml under
// `[mcp_servers.<name>]` sections. We hand-roll a minimal section editor so
// the project stays at three runtime deps (no TOML library).
//
// User-scoped only; codex doesn't read project-local MCP configs.
var codexTarget = Target{
	Name:        "codex",
	ProjectPath: nil,
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("codex: no home directory")
		}
		return filepath.Join(home, ".codex", "config.toml"), nil
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
	Apply: func(path string, server map[string]any) (bool, error) {
		body, err := readOrEmpty(path)
		if err != nil {
			return false, err
		}
		section := codexSectionFor(server)
		updated, changed := upsertCodexSection(body, ServerName, section)
		if !changed {
			return false, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, err
		}
		return true, os.WriteFile(path, updated, 0o644)
	},
	Remove: func(path string) (bool, error) {
		body, err := readOrEmpty(path)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		updated, removed := removeCodexSection(body, ServerName)
		if !removed {
			return false, nil
		}
		return true, os.WriteFile(path, updated, 0o644)
	},
	Inspect: func(path string) (map[string]any, error) {
		body, err := readOrEmpty(path)
		if err != nil {
			return nil, err
		}
		entry, present := readCodexSection(body, ServerName)
		if !present {
			return nil, nil
		}
		return entry, nil
	},
}

// readOrEmpty reads path or returns an empty body if absent.
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

// codexSectionFor renders one [mcp_servers.<name>] block from the canonical
// server map. We only emit the keys we actually use.
func codexSectionFor(server map[string]any) string {
	cmd, _ := server["command"].(string)
	argsAny, _ := server["args"].([]any)
	var sb strings.Builder
	sb.WriteString("[mcp_servers.")
	sb.WriteString(ServerName)
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

// tomlString quotes s for TOML's basic-string syntax. Sufficient for the
// limited content we emit (paths, "serve").
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

// upsertCodexSection inserts or replaces the [mcp_servers.<name>] block in
// body. Returns the new body and whether anything changed.
func upsertCodexSection(body []byte, name, section string) ([]byte, bool) {
	header := "[mcp_servers." + name + "]"
	start, end := codexSectionRange(body, header)
	if start < 0 {
		// Append: ensure trailing newline before the new block.
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

// removeCodexSection strips the [mcp_servers.<name>] block including its
// trailing blank line. Returns the new body and whether anything was removed.
func removeCodexSection(body []byte, name string) ([]byte, bool) {
	header := "[mcp_servers." + name + "]"
	start, end := codexSectionRange(body, header)
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

// codexSectionRange finds the [start, end) byte range of the section whose
// first line is exactly `header`. Returns (-1, -1) if not found.
func codexSectionRange(body []byte, header string) (int, int) {
	lines := bytes.Split(body, []byte("\n"))
	offset := 0
	for i, line := range lines {
		if string(bytes.TrimRight(line, " \t")) == header {
			start := offset
			end := offset + len(line) + 1 // include header + its \n
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

// isTOMLSectionHeader reports whether line is a TOML section/table header.
func isTOMLSectionHeader(line []byte) bool {
	t := bytes.TrimSpace(line)
	return len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']'
}

// readCodexSection extracts `command` and `args` from the named section.
// Returns (nil, false) if the section isn't present.
func readCodexSection(body []byte, name string) (map[string]any, bool) {
	header := "[mcp_servers." + name + "]"
	start, end := codexSectionRange(body, header)
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

// splitTOMLArray splits "a, b, c" into ["a","b","c"], respecting quoted strings.
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
