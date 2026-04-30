package config

import "fmt"

// Validate checks enums and durations. Returns first error encountered.
func Validate(c *Config) error {
	switch c.Concurrency.RequireExpect {
	case "writes", "warn", "off":
	default:
		return fmt.Errorf("concurrency.require_expect: invalid %q", c.Concurrency.RequireExpect)
	}
	switch c.Concurrency.AutoSave {
	case "", "clean", "off", "force":
	default:
		return fmt.Errorf("concurrency.auto_save: invalid %q (want clean|off|force)", c.Concurrency.AutoSave)
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
	switch c.IDE.Diagnostics.Default {
	case "", "errors", "warnings", "all", "none":
	default:
		return fmt.Errorf("ide.diagnostics.default: invalid %q (want errors|warnings|all|none)", c.IDE.Diagnostics.Default)
	}
	if c.IDE.Diagnostics.CacheTTL != "" {
		if _, err := ParseDuration(c.IDE.Diagnostics.CacheTTL); err != nil {
			return fmt.Errorf("ide.diagnostics.cache_ttl: %w", err)
		}
	}
	switch c.Workspace.AutoCreate {
	case "", "root-only", "true", "false":
	default:
		return fmt.Errorf("workspace.auto_create: invalid %q (want root-only|true|false)", c.Workspace.AutoCreate)
	}
	for name, s := range map[string]string{
		"transactions.auto_rollback_idle_for":         c.Transactions.AutoRollbackIdleFor,
		"stale.buffer_idle_for":                       c.Stale.BufferIdleFor,
		"stale.branch_idle_for":                       c.Stale.BranchIdleFor,
		"audit.retention":                             c.Audit.Retention,
		"auto_prune.policies.closed_files_older_than": c.AutoPrune.Policies.ClosedFilesOlderThan,
		"auto_prune.policies.dead_branches_idle_for":  c.AutoPrune.Policies.DeadBranchesIdleFor,
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
