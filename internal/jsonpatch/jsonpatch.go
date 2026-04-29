// Package jsonpatch provides additive merges into JSON files that the user
// owns. Used by ae permissions install to add allow rules without
// disturbing unrelated keys. Output is normalized 2-space indented JSON;
// top-level key order is alphabetized on write (acceptable cost for using
// stdlib only).
//
// The path is given as a leading-slash JSON pointer (RFC 6901 lite): /a/b/c
// addresses the array at root.a.b.c. Missing parents are created.
package jsonpatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// AddToArrayInObject inserts every value into the array at path,
// deduplicating against existing entries. Returns the modified JSON; if no
// values were new, returns content unchanged.
func AddToArrayInObject(content []byte, pointer string, values []string) ([]byte, error) {
	if len(content) == 0 {
		content = []byte("{}")
	}
	root, err := parseObject(content)
	if err != nil {
		return nil, err
	}
	parts, err := splitPointer(pointer)
	if err != nil {
		return nil, err
	}
	parent := descendCreating(root, parts[:len(parts)-1])
	leaf := parts[len(parts)-1]
	existing := stringArray(parent[leaf])
	set := make(map[string]bool, len(existing))
	for _, v := range existing {
		set[v] = true
	}
	added := false
	for _, v := range values {
		if set[v] {
			continue
		}
		existing = append(existing, v)
		set[v] = true
		added = true
	}
	if !added {
		return content, nil
	}
	parent[leaf] = anyArray(existing)
	return marshal(root)
}

// RemoveFromArrayInObject removes every occurrence of each value from the
// array at path. The array is left in place even if it becomes empty.
func RemoveFromArrayInObject(content []byte, pointer string, values []string) ([]byte, error) {
	if len(content) == 0 {
		return content, nil
	}
	root, err := parseObject(content)
	if err != nil {
		return nil, err
	}
	parts, err := splitPointer(pointer)
	if err != nil {
		return nil, err
	}
	parent, ok := descendExisting(root, parts[:len(parts)-1])
	if !ok {
		return content, nil
	}
	leaf := parts[len(parts)-1]
	existing := stringArray(parent[leaf])
	want := make(map[string]bool, len(values))
	for _, v := range values {
		want[v] = true
	}
	var kept []string
	removed := false
	for _, v := range existing {
		if want[v] {
			removed = true
			continue
		}
		kept = append(kept, v)
	}
	if !removed {
		return content, nil
	}
	parent[leaf] = anyArray(kept)
	return marshal(root)
}

// parseObject decodes content as a JSON object.
func parseObject(b []byte) (map[string]any, error) {
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("jsonpatch: parse: %w", err)
	}
	if v == nil {
		v = map[string]any{}
	}
	return v, nil
}

// splitPointer turns "/a/b/c" into ["a","b","c"]. Errors on malformed input.
func splitPointer(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("jsonpatch: pointer must start with '/'")
	}
	parts := strings.Split(p[1:], "/")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("jsonpatch: pointer is empty")
	}
	return parts, nil
}

// descendCreating walks parts in m, creating missing intermediate objects.
func descendCreating(m map[string]any, parts []string) map[string]any {
	cur := m
	for _, p := range parts {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	return cur
}

// descendExisting walks parts; returns false if any segment is missing.
func descendExisting(m map[string]any, parts []string) (map[string]any, bool) {
	cur := m
	for _, p := range parts {
		next, ok := cur[p].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// stringArray extracts a []string from a JSON-decoded array. Non-string
// elements are skipped.
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

// marshal encodes the map with 2-space indentation and trailing newline.
func marshal(v map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
