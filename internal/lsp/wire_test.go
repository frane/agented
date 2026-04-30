package lsp

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestEncodeDecodeRequestRoundTrip(t *testing.T) {
	cases := []Request{
		{Verb: "sym", Args: []string{"foo.go"}},
		{Verb: "ref", Args: []string{"HandleAuth"}},
		{Verb: "def", Args: []string{"HandleAuth", "foo.go:47:12"}},
		{Verb: "notify", Args: []string{"changed", "foo.go", "47", "50"},
			Content: []string{"line one", "line two"}},
	}
	for _, want := range cases {
		var buf bytes.Buffer
		if err := want.Encode(&buf); err != nil {
			t.Fatalf("encode: %v", err)
		}
		got, err := DecodeRequest(bufio.NewReader(&buf))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Verb != want.Verb || !equalStr(got.Args, want.Args) || !equalStr(got.Content, want.Content) {
			t.Fatalf("round-trip mismatch: got %#v want %#v", got, want)
		}
	}
}

func TestDecodeResponses(t *testing.T) {
	raw := strings.Join([]string{
		"sym\tfunc\tfoo.go:47:1\tHandleAuth",
		"sym\tfunc\tfoo.go:89:1\tparseToken",
		"end",
		"",
	}, "\n")
	rs, err := DecodeResponses(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("want 2 records, got %d", len(rs))
	}
	if rs[0].Kind != "sym" || rs[0].Fields[2] != "HandleAuth" {
		t.Fatalf("unexpected first record: %#v", rs[0])
	}
}

func TestEncodeResponsesRoundTrip(t *testing.T) {
	in := []Response{
		SymbolResponse("func", "foo.go:47:1", "HandleAuth"),
		RefResponse("foo.go:50:5", "call", "HandleAuth(ctx, req)"),
	}
	var buf bytes.Buffer
	if err := EncodeResponses(&buf, in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeResponses(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len mismatch: %d vs %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Kind != in[i].Kind {
			t.Fatalf("[%d] kind mismatch", i)
		}
		if !equalStr(out[i].Fields, in[i].Fields) {
			t.Fatalf("[%d] fields mismatch: %v vs %v", i, out[i].Fields, in[i].Fields)
		}
	}
}

func TestParseLocation(t *testing.T) {
	path, line, col, err := ParseLocation("/abs/path/foo.go:47:12")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if path != "/abs/path/foo.go" || line != 47 || col != 12 {
		t.Fatalf("unexpected parse: %s %d %d", path, line, col)
	}
}

func TestFormatLocation(t *testing.T) {
	got := FormatLocation("foo.go", 47, 12)
	if got != "foo.go:47:12" {
		t.Fatalf("unexpected: %s", got)
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
