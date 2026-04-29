package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

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
// any other type replaces. Records each modified leaf path with sourceName.
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

	envApply(merged, sources)

	for k, v := range flagOverrides {
		if err := setDottedString(merged, k, v); err != nil {
			return nil, nil, fmt.Errorf("flag override %s: %w", k, err)
		}
		sources[k] = SourceFlag
	}

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
