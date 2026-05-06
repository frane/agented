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
	IDE         IDECfg         `json:"ide"`
}

// WorkspaceCfg controls workspace discovery (used by Locate).
type WorkspaceCfg struct {
	// AutoCreate is one of: "root-only" (default), "true", "false".
	// "root-only": auto-create at the project root when one is detected.
	// "true": also auto-create at cwd when no project root is detected.
	// "false": disable tier-2 entirely (require explicit `ae init`).
	AutoCreate string `json:"auto_create"`
}

// IDECfg controls v0.3 IDE/LSP mode. Off by default.
//
// When Enabled is true, ae spawns a long-running daemon (ae lsp) that
// hosts language servers, caches diagnostics in SQLite, and answers
// symbol/reference/definition queries via a Unix socket. Mutating verbs
// pick up the cached diagnostics and emit them as `diag` lines.
type IDECfg struct {
	Enabled          bool                       `json:"enabled"`
	AutoStartDaemon  bool                       `json:"auto_start_daemon"`
	Languages        map[string]IDELanguageCfg  `json:"languages"`
	Extensions       map[string]string          `json:"extensions"`
	Diagnostics      IDEDiagnosticsCfg          `json:"diagnostics"`
}

// IDELanguageCfg configures one language: which servers to spawn, plus
// auto-start. Multiple servers per language are supported (e.g.
// typescript-language-server + vscode-eslint-language-server). The first
// entry in Servers answers symbol/reference/definition queries; all
// entries contribute diagnostics.
//
// Backward compat: the legacy single-server form (top-level Server/Args)
// from v0.3.0 / v0.3.1 still works. When Servers is non-empty it wins.
type IDELanguageCfg struct {
	Servers   []IDEServerCfg `json:"servers,omitempty"`
	AutoStart bool           `json:"auto_start"`

	// Legacy single-server fields. Used only when Servers is empty.
	Server string   `json:"server,omitempty"`
	Args   []string `json:"args,omitempty"`
}

// IDEServerCfg pins one LSP server within a language.
//
// Name is the display label used in lsp_status and the diagnostics
// source_server column (e.g. "tsserver", "eslint"). Command is the
// executable to spawn. If Name is empty it falls back to Command.
type IDEServerCfg struct {
	Name    string   `json:"name,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`

	// InitOptions is sent to the LSP server in the `initializationOptions`
	// field of the `initialize` request. Server-specific schema (gopls,
	// rust-analyzer, pyright, etc. each accept different keys); ae does no
	// validation. Empty/nil sends nothing.
	InitOptions map[string]any `json:"init_options,omitempty"`
}

// ResolvedServers returns the canonical server list, synthesizing one
// from the legacy Server/Args fields when Servers is empty.
func (c IDELanguageCfg) ResolvedServers() []IDEServerCfg {
	if len(c.Servers) > 0 {
		return c.Servers
	}
	if c.Server == "" {
		return nil
	}
	return []IDEServerCfg{{Name: c.Server, Command: c.Server, Args: c.Args}}
}

// IDEDiagnosticsCfg controls how diagnostics surface on mutating verbs.
type IDEDiagnosticsCfg struct {
	// Default is one of: errors | warnings | all | none. Per-call override
	// via --diagnostics / -G.
	Default        string `json:"default"`
	MaxPerResponse int    `json:"max_per_response"`
	CacheTTL       string `json:"cache_ttl"`
}

type Concurrency struct {
	RequireExpect string `json:"require_expect"` // writes | warn | off
	AutoSave         string `json:"auto_save"`           // clean | off | force (default clean)
	AutoLoadOnDrift  bool   `json:"auto_load_on_drift"` // when true, sniff disk before each write and load divergent content into a new edit
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
	DefaultFormat     string `json:"default_format"`     // tab | json
	IncludeStateToken bool   `json:"include_state_token"`
	SyntaxHighlight   bool   `json:"syntax_highlight"`   // when true, color tokens by language (chroma)
	NudgeOnPipe       bool   `json:"nudge_on_pipe"`     // when true, read verbs print a stderr nudge when stdout is piped without a --limit/-L or --range bound
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

