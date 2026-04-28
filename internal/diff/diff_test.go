package diff_test

import (
	"strings"
	"testing"

	"github.com/frane/agented/internal/diff"
)

func TestEqual(t *testing.T) {
	ops := diff.Compare("a\nb\nc\n", "a\nb\nc\n")
	for _, op := range ops {
		if op.Kind != diff.OpEqual {
			t.Errorf("non-equal op: %+v", op)
		}
	}
}

func TestSimpleReplace(t *testing.T) {
	ops := diff.Compare("a\nb\nc\n", "a\nB\nc\n")
	hasIns := false
	hasDel := false
	for _, op := range ops {
		if op.Kind == diff.OpInsert && op.Line == "B\n" {
			hasIns = true
		}
		if op.Kind == diff.OpDelete && op.Line == "b\n" {
			hasDel = true
		}
	}
	if !hasIns || !hasDel {
		t.Errorf("missing ops: ins=%v del=%v", hasIns, hasDel)
	}
}

func TestUnifiedHeader(t *testing.T) {
	out := diff.Unified("a\nb\n", "a\nB\n", "old", "new", 3)
	if !strings.Contains(out, "--- old") {
		t.Errorf("missing --- header: %q", out)
	}
	if !strings.Contains(out, "+++ new") {
		t.Errorf("missing +++ header: %q", out)
	}
	if !strings.Contains(out, "-b") || !strings.Contains(out, "+B") {
		t.Errorf("missing change lines: %q", out)
	}
}

func TestUnifiedNoNewlineAtEOF(t *testing.T) {
	out := diff.Unified("a\nb", "a\nc", "x", "y", 1)
	if !strings.Contains(out, "\\ No newline at end of file") {
		t.Errorf("expected no-newline marker: %q", out)
	}
}
