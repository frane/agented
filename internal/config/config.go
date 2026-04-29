// Package config defines agented's runtime configuration: schema, parsing,
// and precedence resolution across defaults, global config, project config,
// environment variables, and CLI flags.
package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed defaults.json
var defaultsJSON []byte

// Config is the resolved configuration used by the binary at runtime.
type Config struct {
	Actor       string         `json:"actor"`
	Concurrency Concurrency    `json:"concurrency"`
	Transactions TransactionsCfg `json:"transactions"`
	Stale       StaleCfg       `json:"stale"`
	AutoPrune   AutoPruneCfg   `json:"auto_prune"`
	Audit       AuditCfg       `json:"audit"`
	Output      OutputCfg      `json:"output"`
	Skill       SkillCfg       `json:"skill"`
	MCP         MCPCfg         `json:"mcp"`
	Logging     LoggingCfg     `json:"logging"`
	Workspace   WorkspaceCfg   `json:"workspace"`
}

// WorkspaceCfg controls workspace discovery (used by Locate).
type WorkspaceCfg struct {
	// AutoCreate is one of: "root-only" (default), "true", "false".
	// "root-only": auto-create at the project root when one is detected.
	// "true": also auto-create at cwd when no project root is detected.
	// "false": disable tier-2 entirely (require explicit `ae init`).
	AutoCreate string `json:"auto_create"`
}

type Concurrency struct {
	RequireExpect string `json:"require_expect"` // writes | warn | off
}

type TransactionsCfg struct {
	AutoRollbackIdleFor string `json:"auto_rollback_idle_for"`
}

type StaleCfg struct {
	BufferIdleFor string `json:"buffer_idle_for"`
	BranchIdleFor string `json:"branch_idle_for"`
}

type AutoPruneCfg struct {
	Enabled  bool             `json:"enabled"`
	OnClose  bool             `json:"on_close"`
	OnOpen   bool             `json:"on_open"`
	Schedule string           `json:"schedule"` // daily | hourly | off
	Policies AutoPrunePolicies `json:"policies"`
}

type AutoPrunePolicies struct {
	ClosedFilesOlderThan string `json:"closed_files_older_than"`
	DeadBranchesIdleFor  string `json:"dead_branches_idle_for"`
	KeepRecentPerBranch  int    `json:"keep_recent_per_branch"`
}

type AuditCfg struct {
	Retention string `json:"retention"`
	AutoPrune bool   `json:"auto_prune"`
}

type OutputCfg struct {
	DefaultFormat     string `json:"default_format"` // tab | json
	IncludeStateToken bool   `json:"include_state_token"`
}

type SkillCfg struct {
	EnforceVersion string `json:"enforce_version"` // major | any | off
}

type MCPCfg struct {
	DefaultTransport string `json:"default_transport"` // stdio | tcp | unix
	TCPPort          int    `json:"tcp_port"`
	UnixSocketPath   string `json:"unix_socket_path"`
}

type LoggingCfg struct {
	Level       string `json:"level"`
	Destination string `json:"destination"`
}

// Sources records which file (if any) set each config field. Used by
// `ae config show --source`.
type Sources map[string]string

const (
	SourceBuiltin = "builtin"
	SourceGlobal  = "global"
	SourceProject = "project"
	SourceEnv     = "env"
	SourceFlag    = "flag"
)

// Defaults returns a fresh Config populated from the embedded defaults.
func Defaults() *Config {
	c := &Config{}
	if err := json.Unmarshal(defaultsJSON, c); err != nil {
		// Built-in defaults are tested; if this panics, the build is broken.
		panic(fmt.Sprintf("agented: built-in defaults are invalid: %v", err))
	}
	return c
}

// LoadFile parses a JSON config file. Returns nil and no error if the file
// does not exist (caller decides whether absence is an error).
func LoadFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	stripComments(raw)
	return raw, nil
}

// stripComments removes any "_comment" or "_comment_*" keys recursively.
func stripComments(v any) {
	switch m := v.(type) {
	case map[string]any:
		for k := range m {
			if k == "_comment" || strings.HasPrefix(k, "_comment_") {
				delete(m, k)
				continue
			}
			stripComments(m[k])
		}
	case []any:
		for _, x := range m {
			stripComments(x)
		}
	}
}

// merge overlays src on top of dst (in place). Maps are merged recursively;
// any other type replaces. records each modified leaf path with sourceName.
func merge(dst, src map[string]any, prefix string, sources Sources, sourceName string) {
	for k, v := range src {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if existingMap, ok := dst[k].(map[string]any); ok {
			if newMap, ok := v.(map[string]any); ok {
				merge(existingMap, newMap, path, sources, sourceName)
				continue
			}
		}
		dst[k] = v
		recordLeaves(path, v, sources, sourceName)
	}
}

func recordLeaves(prefix string, v any, sources Sources, name string) {
	if m, ok := v.(map[string]any); ok {
		for k, vv := range m {
			recordLeaves(prefix+"."+k, vv, sources, name)
		}
		return
	}
	sources[prefix] = name
}

// Resolve combines defaults + global file + project file + env + flag overrides.
// flagOverrides is a map of dotted keys to string values; env vars are read
// with prefix AE_ and key transform "."→"_" then upper-case.
// If projectPath is empty, the project layer is skipped.
func Resolve(globalPath, projectPath string, flagOverrides map[string]string) (*Config, Sources, error) {
	sources := make(Sources)
	merged := map[string]any{}
	var defaults map[string]any
	if err := json.Unmarshal(defaultsJSON, &defaults); err != nil {
		return nil, nil, fmt.Errorf("decode defaults: %w", err)
	}
	stripComments(defaults)
	merge(merged, defaults, "", sources, SourceBuiltin)

	if globalPath != "" {
		raw, err := LoadFile(globalPath)
		if err != nil {
			return nil, nil, err
		}
		if raw != nil {
			merge(merged, raw, "", sources, SourceGlobal)
		}
	}
	if projectPath != "" {
		raw, err := LoadFile(projectPath)
		if err != nil {
			return nil, nil, err
		}
		if raw != nil {
			merge(merged, raw, "", sources, SourceProject)
		}
	}

	// Env vars: AE_<UPPER_DOT_TO_UNDERSCORE>.
	envApply(merged, sources)

	// Flag overrides.
	for k, v := range flagOverrides {
		if err := setDottedString(merged, k, v); err != nil {
			return nil, nil, fmt.Errorf("flag override %s: %w", k, err)
		}
		sources[k] = SourceFlag
	}

	// Re-encode then decode into struct.
	b, err := json.Marshal(merged)
	if err != nil {
		return nil, nil, err
	}
	cfg := &Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, nil, fmt.Errorf("decode resolved config: %w", err)
	}
	if err := Validate(cfg); err != nil {
		return nil, nil, err
	}
	return cfg, sources, nil
}

// envApply walks the known schema and overlays AE_*=... env vars on merged.
func envApply(merged map[string]any, sources Sources) {
	envMap := map[string]string{
		"AE_ACTOR":                     "actor",
		"AE_REQUIRE_EXPECT":            "concurrency.require_expect",
		"AE_AUTO_ROLLBACK_IDLE_FOR":    "transactions.auto_rollback_idle_for",
		"AE_STALE_BUFFER_IDLE_FOR":     "stale.buffer_idle_for",
		"AE_STALE_BRANCH_IDLE_FOR":     "stale.branch_idle_for",
		"AE_AUTO_PRUNE_ENABLED":        "auto_prune.enabled",
		"AE_AUTO_PRUNE_ON_CLOSE":       "auto_prune.on_close",
		"AE_AUTO_PRUNE_ON_OPEN":        "auto_prune.on_open",
		"AE_AUTO_PRUNE_SCHEDULE":       "auto_prune.schedule",
		"AE_AUDIT_RETENTION":           "audit.retention",
		"AE_AUDIT_AUTO_PRUNE":          "audit.auto_prune",
		"AE_OUTPUT_DEFAULT_FORMAT":     "output.default_format",
		"AE_OUTPUT_INCLUDE_STATE_TOKEN":"output.include_state_token",
		"AE_SKILL_ENFORCE_VERSION":     "skill.enforce_version",
		"AE_MCP_DEFAULT_TRANSPORT":     "mcp.default_transport",
		"AE_MCP_TCP_PORT":              "mcp.tcp_port",
		"AE_MCP_UNIX_SOCKET_PATH":      "mcp.unix_socket_path",
		"AE_LOGGING_LEVEL":             "logging.level",
		"AE_LOGGING_DESTINATION":       "logging.destination",
		"AE_WORKSPACE_AUTO_CREATE":     "workspace.auto_create",
	}
	for envName, dotted := range envMap {
		if v, ok := os.LookupEnv(envName); ok {
			_ = setDottedString(merged, dotted, v)
			sources[dotted] = SourceEnv
		}
	}
}

// setDottedString writes a string value into a nested map at dotted key,
// coercing into bool/int when the existing value at that key suggests it.
func setDottedString(m map[string]any, dotted, val string) error {
	parts := strings.Split(dotted, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			existing, hadExisting := cur[p]
			coerced, err := coerce(val, existing, hadExisting)
			if err != nil {
				return err
			}
			cur[p] = coerced
			return nil
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	return nil
}

func coerce(val string, existing any, hadExisting bool) (any, error) {
	if hadExisting {
		switch existing.(type) {
		case bool:
			b, err := strconv.ParseBool(val)
			if err != nil {
				return nil, fmt.Errorf("expected bool, got %q", val)
			}
			return b, nil
		case float64:
			n, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return nil, fmt.Errorf("expected number, got %q", val)
			}
			return n, nil
		}
	}
	// Default: string
	return val, nil
}

// Validate checks enums and durations. Returns first error encountered.
func Validate(c *Config) error {
	switch c.Concurrency.RequireExpect {
	case "writes", "warn", "off":
	default:
		return fmt.Errorf("concurrency.require_expect: invalid %q", c.Concurrency.RequireExpect)
	}
	switch c.AutoPrune.Schedule {
	case "daily", "hourly", "off":
	default:
		return fmt.Errorf("auto_prune.schedule: invalid %q", c.AutoPrune.Schedule)
	}
	switch c.Output.DefaultFormat {
	case "tab", "json":
	default:
		return fmt.Errorf("output.default_format: invalid %q", c.Output.DefaultFormat)
	}
	switch c.Skill.EnforceVersion {
	case "major", "any", "off":
	default:
		return fmt.Errorf("skill.enforce_version: invalid %q", c.Skill.EnforceVersion)
	}
	switch c.MCP.DefaultTransport {
	case "stdio", "tcp", "unix":
	default:
		return fmt.Errorf("mcp.default_transport: invalid %q", c.MCP.DefaultTransport)
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level: invalid %q", c.Logging.Level)
	}
	switch c.Workspace.AutoCreate {
	case "", "root-only", "true", "false":
	default:
		return fmt.Errorf("workspace.auto_create: invalid %q (want root-only|true|false)", c.Workspace.AutoCreate)
	}
	for name, s := range map[string]string{
		"transactions.auto_rollback_idle_for":      c.Transactions.AutoRollbackIdleFor,
		"stale.buffer_idle_for":                    c.Stale.BufferIdleFor,
		"stale.branch_idle_for":                    c.Stale.BranchIdleFor,
		"audit.retention":                          c.Audit.Retention,
		"auto_prune.policies.closed_files_older_than": c.AutoPrune.Policies.ClosedFilesOlderThan,
		"auto_prune.policies.dead_branches_idle_for": c.AutoPrune.Policies.DeadBranchesIdleFor,
	} {
		if _, err := ParseDuration(s); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if c.AutoPrune.Policies.KeepRecentPerBranch < 0 {
		return fmt.Errorf("auto_prune.policies.keep_recent_per_branch must be >= 0")
	}
	return nil
}

// AutoRollbackIdle returns the parsed duration for transaction idle rollback.
func (c *Config) AutoRollbackIdle() time.Duration {
	d, _ := ParseDuration(c.Transactions.AutoRollbackIdleFor)
	return d
}

// BufferIdle returns parsed buffer-idle duration.
func (c *Config) BufferIdle() time.Duration {
	d, _ := ParseDuration(c.Stale.BufferIdleFor)
	return d
}

// BranchIdle returns parsed branch-idle duration.
func (c *Config) BranchIdle() time.Duration {
	d, _ := ParseDuration(c.Stale.BranchIdleFor)
	return d
}

// ClosedFilesOlderThan returns parsed duration.
func (c *Config) ClosedFilesOlderThan() time.Duration {
	d, _ := ParseDuration(c.AutoPrune.Policies.ClosedFilesOlderThan)
	return d
}

// DeadBranchesIdleFor returns parsed duration.
func (c *Config) DeadBranchesIdleFor() time.Duration {
	d, _ := ParseDuration(c.AutoPrune.Policies.DeadBranchesIdleFor)
	return d
}

// AuditRetention returns parsed duration.
func (c *Config) AuditRetention() time.Duration {
	d, _ := ParseDuration(c.Audit.Retention)
	return d
}

// GlobalPath returns the global config file path: ~/.agented/config.json.
func GlobalPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".agented", "config.json")
}

// SetDotted writes a key in a config file, creating the file if necessary.
// Used by `ae config set`.
func SetDotted(path, key, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		stripComments(raw)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := setDottedString(raw, key, value); err != nil {
		return err
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// UnsetDotted removes a dotted key from a config file (no-op if missing).
func UnsetDotted(path, key string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	raw := map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	stripComments(raw)
	parts := strings.Split(key, ".")
	deleteDotted(raw, parts)
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func deleteDotted(m map[string]any, parts []string) {
	if len(parts) == 0 {
		return
	}
	if len(parts) == 1 {
		delete(m, parts[0])
		return
	}
	if next, ok := m[parts[0]].(map[string]any); ok {
		deleteDotted(next, parts[1:])
	}
}

// FlattenLeaves returns a flat dotted-key map of all leaf values, sorted
// for stable output. Used by `ae config show`.
func FlattenLeaves(c *Config) map[string]string {
	b, _ := json.Marshal(c)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	out := map[string]string{}
	flatten(raw, "", out)
	return out
}

func flatten(v any, prefix string, out map[string]string) {
	switch m := v.(type) {
	case map[string]any:
		for k, vv := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			flatten(vv, path, out)
		}
	case bool:
		out[prefix] = strconv.FormatBool(m)
	case float64:
		// Integers in JSON come back as float64; format compactly.
		if m == float64(int64(m)) {
			out[prefix] = strconv.FormatInt(int64(m), 10)
		} else {
			out[prefix] = strconv.FormatFloat(m, 'f', -1, 64)
		}
	case string:
		out[prefix] = m
	case nil:
		out[prefix] = ""
	}
}
