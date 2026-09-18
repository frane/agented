package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// envApply walks the known schema and overlays AE_*=... env vars on merged.
func envApply(merged map[string]any, sources Sources) {
	envMap := map[string]string{
		"AE_ACTOR":                      "actor",
		"AE_AUTO_LOAD_ON_DRIFT":         "concurrency.auto_load_on_drift",
		"AE_READ_DRIFT":                 "concurrency.read_drift",
		"AE_AUTO_SAVE":                  "concurrency.auto_save",
		"AE_REQUIRE_EXPECT":             "concurrency.require_expect",
		"AE_AUTO_ROLLBACK_IDLE_FOR":     "transactions.auto_rollback_idle_for",
		"AE_STALE_BUFFER_IDLE_FOR":      "stale.buffer_idle_for",
		"AE_STALE_BRANCH_IDLE_FOR":      "stale.branch_idle_for",
		"AE_AUTO_PRUNE_ENABLED":         "auto_prune.enabled",
		"AE_AUTO_PRUNE_ON_CLOSE":        "auto_prune.on_close",
		"AE_AUTO_PRUNE_ON_OPEN":         "auto_prune.on_open",
		"AE_AUTO_PRUNE_SCHEDULE":        "auto_prune.schedule",
		"AE_AUDIT_RETENTION":            "audit.retention",
		"AE_AUDIT_AUTO_PRUNE":           "audit.auto_prune",
		"AE_OUTPUT_DEFAULT_FORMAT":      "output.default_format",
		"AE_OUTPUT_INCLUDE_STATE_TOKEN": "output.include_state_token",
		"AE_OUTPUT_SYNTAX_HIGHLIGHT":    "output.syntax_highlight",
		"AE_OUTPUT_EDIT_DIFF":           "output.edit_diff",
		"AE_SKILL_ENFORCE_VERSION":      "skill.enforce_version",
		"AE_MCP_DEFAULT_TRANSPORT":      "mcp.default_transport",
		"AE_MCP_TCP_PORT":               "mcp.tcp_port",
		"AE_MCP_UNIX_SOCKET_PATH":       "mcp.unix_socket_path",
		"AE_LOGGING_LEVEL":              "logging.level",
		"AE_LOGGING_DESTINATION":        "logging.destination",
		"AE_WORKSPACE_AUTO_CREATE":      "workspace.auto_create",
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
			if !hadExisting {
				// The key isn't in this file yet — the overwhelmingly common
				// case for `ae config set`, since the defaults live in the
				// embedded defaults.json, not in the user's config. Without a
				// type to coerce against, a bool key was written as the string
				// "false", and every subsequent ae command then failed to
				// decode the config: a wedged workspace from one config set.
				existing, hadExisting = defaultLeaf(dotted)
			}
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

// coerceLeavesToSchema walks a merged config tree and rewrites any string
// leaf whose schema default is a bool or a number into that type. Values the
// schema does not know, and strings that do not parse, are left alone for
// validation to report.
func coerceLeavesToSchema(m map[string]any, prefix string) {
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok {
			coerceLeavesToSchema(sub, path)
			continue
		}
		str, ok := v.(string)
		if !ok {
			continue
		}
		def, known := defaultLeaf(path)
		if !known {
			continue
		}
		if coerced, err := coerce(str, def, true); err == nil {
			m[k] = coerced
		}
	}
}

// defaultLeafs is the built-in defaults as a generic JSON tree, so a dotted
// key can be typed without the user's file mentioning it.
var defaultLeafs = sync.OnceValue(func() map[string]any {
	b, err := json.Marshal(Defaults())
	if err != nil {
		return nil
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
})

// defaultLeaf returns the default value at a dotted key, and whether one
// exists. Unknown keys return (nil, false), which leaves coerce's existing
// string behavior for config the schema doesn't know about.
func defaultLeaf(dotted string) (any, bool) {
	cur := any(defaultLeafs())
	for _, p := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
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
	return val, nil
}
