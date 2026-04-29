package jsonpatch_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frane/agented/internal/jsonpatch"
)

func TestAddCreatesArrayWhenAbsent(t *testing.T) {
	out, err := jsonpatch.AddToArrayInObject(nil, "/permissions/allow", []string{"Bash(ae *)"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	allow := got["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Bash(ae *)" {
		t.Errorf("allow: %v", allow)
	}
}

func TestAddDedupes(t *testing.T) {
	in := []byte(`{"permissions":{"allow":["Bash(ae *)"]}}`)
	out, err := jsonpatch.AddToArrayInObject(in, "/permissions/allow", []string{"Bash(ae *)", "Bash(./ae *)"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	allow := got["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 2 {
		t.Errorf("expected 2, got %d: %v", len(allow), allow)
	}
}

func TestAddPreservesUnrelatedKeys(t *testing.T) {
	in := []byte(`{
  "permissions": {"allow": ["Bash(git *)"], "deny": ["Bash(rm -rf /)"]},
  "model": "sonnet"
}`)
	out, err := jsonpatch.AddToArrayInObject(in, "/permissions/allow", []string{"Bash(ae *)"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	if got["model"] != "sonnet" {
		t.Errorf("model lost: %v", got["model"])
	}
	deny := got["permissions"].(map[string]any)["deny"].([]any)
	if len(deny) != 1 || deny[0] != "Bash(rm -rf /)" {
		t.Errorf("deny lost: %v", deny)
	}
}

func TestAddNoChangeWhenAllPresent(t *testing.T) {
	in := []byte(`{"permissions":{"allow":["Bash(ae *)"]}}`)
	out, err := jsonpatch.AddToArrayInObject(in, "/permissions/allow", []string{"Bash(ae *)"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("expected unchanged input, got %s", out)
	}
}

func TestRemoveLeavesEmptyArray(t *testing.T) {
	in := []byte(`{"permissions":{"allow":["Bash(ae *)"]}}`)
	out, err := jsonpatch.RemoveFromArrayInObject(in, "/permissions/allow", []string{"Bash(ae *)"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	allow := got["permissions"].(map[string]any)["allow"]
	arr, ok := allow.([]any)
	if !ok {
		t.Fatalf("allow not an array: %T %v", allow, allow)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array, got %v", arr)
	}
}

func TestRemoveAbsentIsNoop(t *testing.T) {
	in := []byte(`{"permissions":{"allow":["Bash(git *)"]}}`)
	out, err := jsonpatch.RemoveFromArrayInObject(in, "/permissions/allow", []string{"Bash(ae *)"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Bash(git *)") {
		t.Errorf("git rule lost: %s", out)
	}
}

func TestPointerErrors(t *testing.T) {
	if _, err := jsonpatch.AddToArrayInObject([]byte("{}"), "no-leading-slash", []string{"x"}); err == nil {
		t.Error("expected pointer-format error")
	}
	if _, err := jsonpatch.AddToArrayInObject([]byte("{}"), "/", []string{"x"}); err == nil {
		t.Error("expected empty-pointer error")
	}
}
