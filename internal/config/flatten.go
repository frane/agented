package config

import (
	"encoding/json"
	"strconv"
)

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