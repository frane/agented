package applyformat_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frane/agented/internal/applyformat"
)

// All three formats representing the same batch should produce identical Operations.
func TestThreeFormatsIdentity(t *testing.T) {
	short := strings.Join([]string{
		"@foo.go",
		"s 12:14 newName(",
		"i 80 // see ADR-0042",
		"d 67:69",
	}, "\n")
	long := strings.Join([]string{
		"@foo.go",
		"replace range=12:14 with=newName(",
		"insert after=80 text=// see ADR-0042",
		"delete range=67:69",
	}, "\n")
	jsonl := strings.Join([]string{
		`{"file":"foo.go","verb":"replace","range":"12:14","with":"newName("}`,
		`{"file":"foo.go","verb":"insert","after":80,"text":"// see ADR-0042"}`,
		`{"file":"foo.go","verb":"delete","range":"67:69"}`,
	}, "\n")
	sOps, err := applyformat.Parse([]byte(short), "")
	if err != nil {
		t.Fatalf("short: %v", err)
	}
	lOps, err := applyformat.Parse([]byte(long), "")
	if err != nil {
		t.Fatalf("long: %v", err)
	}
	jOps, err := applyformat.Parse([]byte(jsonl), "")
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	// Compare the semantic fields only (ignore LineNum since lines differ).
	for _, ops := range [][]applyformat.Operation{sOps, lOps, jOps} {
		for i := range ops {
			ops[i].LineNum = 0
		}
	}
	if !reflect.DeepEqual(sOps, lOps) {
		t.Errorf("short vs long mismatch:\nshort=%#v\nlong=%#v", sOps, lOps)
	}
	if !reflect.DeepEqual(sOps, jOps) {
		t.Errorf("short vs json mismatch:\nshort=%#v\njson=%#v", sOps, jOps)
	}
	if len(sOps) != 3 {
		t.Errorf("expected 3 ops, got %d", len(sOps))
	}
	if sOps[0].Verb != "replace" || sOps[0].With != "newName(" || sOps[0].Range != "12:14" {
		t.Errorf("op 0 wrong: %+v", sOps[0])
	}
	if sOps[1].Verb != "insert" || sOps[1].After != 80 {
		t.Errorf("op 1 wrong: %+v", sOps[1])
	}
}

func TestStateTokenSuffixShort(t *testing.T) {
	in := "@foo.go\ns 12:14 newName( ! ab12cd34"
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Expect != "ab12cd34" {
		t.Errorf("expect token not parsed: %+v", ops)
	}
	if ops[0].With != "newName(" {
		t.Errorf("with field corrupted: %q", ops[0].With)
	}
}

func TestStateTokenLongform(t *testing.T) {
	in := "@foo.go\nreplace range=12:14 with=newName( expect=ab12cd34"
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops", len(ops))
	}
	if ops[0].Expect != "ab12cd34" {
		t.Errorf("expect=%q want ab12cd34", ops[0].Expect)
	}
	if ops[0].With != "newName(" {
		t.Errorf("with=%q", ops[0].With)
	}
}

func TestHeredocShort(t *testing.T) {
	in := strings.Join([]string{
		"@foo.go",
		"s 12:14 <<<",
		"line one",
		"line two",
		"line three",
		"<<<",
		"i 80 // single-line follow-up",
	}, "\n")
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("got %d ops", len(ops))
	}
	want := "line one\nline two\nline three"
	if ops[0].With != want {
		t.Errorf("heredoc body wrong:\n got %q\nwant %q", ops[0].With, want)
	}
	if ops[1].After != 80 || ops[1].Text != "// single-line follow-up" {
		t.Errorf("op 1 wrong: %+v", ops[1])
	}
}

func TestHeredocLong(t *testing.T) {
	in := strings.Join([]string{
		"@foo.go",
		"replace range=12:14 with=<<<",
		"multi-line content",
		"here",
		"<<<",
	}, "\n")
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops", len(ops))
	}
	want := "multi-line content\nhere"
	if ops[0].With != want {
		t.Errorf("heredoc body wrong: got %q want %q", ops[0].With, want)
	}
}

func TestComments(t *testing.T) {
	in := strings.Join([]string{
		"# this is a comment",
		"@foo.go",
		"# another comment",
		"s 1:1 hello",
		"# trailing",
	}, "\n")
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}

func TestMixedFormatErrors(t *testing.T) {
	// Detection sees first line as shortform, then a JSON-style line later.
	in := strings.Join([]string{
		"@foo.go",
		"s 1:1 hello",
		`{"verb":"insert","after":2,"text":"oops"}`,
	}, "\n")
	_, err := applyformat.Parse([]byte(in), "")
	if err == nil {
		t.Fatal("expected error mixing shortform and JSON lines")
	}
}

func TestMalformedFirstLine(t *testing.T) {
	in := "@foo.go\nzzz blob blob"
	_, err := applyformat.Parse([]byte(in), "")
	if err == nil {
		t.Fatal("expected error on unrecognised verb")
	}
	msg := err.Error()
	if !strings.Contains(msg, "shortform") || !strings.Contains(msg, "longform") || !strings.Contains(msg, "JSON-lines") {
		t.Errorf("error should name all three formats, got %q", msg)
	}
}

func TestEmptyInput(t *testing.T) {
	ops, err := applyformat.Parse(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected 0 ops, got %d", len(ops))
	}
}

func TestDefaultFile(t *testing.T) {
	in := "s 1:1 hi"
	ops, err := applyformat.Parse([]byte(in), "default.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].File != "default.go" {
		t.Errorf("default file not propagated: %+v", ops)
	}
}

func TestCrossFileSeparator(t *testing.T) {
	in := strings.Join([]string{
		"@foo.go",
		"s 1:1 a",
		"@bar.go",
		"i 5 b",
	}, "\n")
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("got %d ops", len(ops))
	}
	if ops[0].File != "foo.go" || ops[1].File != "bar.go" {
		t.Errorf("file separators not honored: %+v", ops)
	}
}

func TestNewlineEscape(t *testing.T) {
	in := `@f.go
s 1:1 line one\nline two`
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	if ops[0].With != "line one\nline two" {
		t.Errorf("\\n not unescaped: %q", ops[0].With)
	}
}

func TestDoubleBackslashKeepsLiteral(t *testing.T) {
	in := `@f.go
s 1:1 the regex is \\n`
	ops, err := applyformat.Parse([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	want := `the regex is \n`
	if ops[0].With != want {
		t.Errorf("got %q want %q", ops[0].With, want)
	}
}

func TestRejectLeadingBackslashSpace(t *testing.T) {
	cases := []string{
		"@foo.go\ni 89 \\    StreamableHttpTransport,",
		"@foo.go\ns 12:14 \\\tindented body",
	}
	for _, in := range cases {
		_, err := applyformat.Parse([]byte(in), "")
		if err == nil {
			t.Errorf("expected error for %q; got nil", in)
			continue
		}
		if !strings.Contains(err.Error(), "looks like an attempt to escape") {
			t.Errorf("expected escape-foot-gun message, got %q", err.Error())
		}
	}
}
