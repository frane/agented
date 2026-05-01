package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

// TestMoveAutosaveSameFile is the regression for issue #3: ae move
// reported success but never flushed the new head to disk. After the
// fix, disk content matches the in-store head and Edit.Saved is true.
func TestMoveAutosaveSameFile(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	engine := &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "move-test",
		DBPath: filepath.Join(dir, "state.db"),
	}
	path := filepath.Join(dir, "foo.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Open(cmd.OpenInput{Path: path}); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Move(cmd.MoveInput{
		Path: path, FromStart: 1, FromEnd: 2, ToLine: 4,
	})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if res.Edit == nil || !res.Edit.Saved {
		t.Fatalf("expected Edit.Saved=true, got %+v", res.Edit)
	}
	got, _ := os.ReadFile(path)
	want := "c\nd\na\nb\ne\n"
	if string(got) != want {
		t.Fatalf("disk content: got %q want %q", string(got), want)
	}
}

// TestMoveAutosaveCrossFile mirrors the same fix for the cross-file
// branch. Both source and destination must be flushed.
func TestMoveAutosaveCrossFile(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	engine := &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "move-cross",
		DBPath: filepath.Join(dir, "state.db"),
	}
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("s1\ns2\ns3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("d1\nd2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Open(cmd.OpenInput{Path: src}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Open(cmd.OpenInput{Path: dst}); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Move(cmd.MoveInput{
		Path: src, FromStart: 1, FromEnd: 2,
		ToFile: dst, ToLine: 1,
	})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if res.Edit == nil || !res.Edit.Saved {
		t.Fatalf("expected Edit.Saved=true, got %+v", res.Edit)
	}
	gotSrc, _ := os.ReadFile(src)
	if string(gotSrc) != "s3\n" {
		t.Fatalf("src disk: got %q want %q", string(gotSrc), "s3\n")
	}
	gotDst, _ := os.ReadFile(dst)
	if string(gotDst) != "d1\ns1\ns2\nd2\n" {
		t.Fatalf("dst disk: got %q want %q", string(gotDst), "d1\ns1\ns2\nd2\n")
	}
}

// TestApplyMultiFileAtomicityOnFailure is the regression for issue #4:
// when one op in a multi-file ae apply batch fails, the successful
// earlier ops must not have been written to disk. Pre-fix, op 0
// succeeded and fsync'd before op 1 errored, leaving the rollback to
// unwind only the in-store head.
func TestApplyMultiFileAtomicityOnFailure(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	engine := &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "apply-test",
		DBPath: filepath.Join(dir, "state.db"),
	}
	x := filepath.Join(dir, "x.txt")
	y := filepath.Join(dir, "y.txt")
	if err := os.WriteFile(x, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(y, []byte("p\nq\nr\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Open(cmd.OpenInput{Path: x}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Open(cmd.OpenInput{Path: y}); err != nil {
		t.Fatal(err)
	}
	batch := "@" + x + "\ns 1:1 SHOULD_ROLL_BACK\n@" + y + "\ns 9999:9999 BAD_RANGE\n"
	in := cmd.ApplyInput{
		Stdin:     strings.NewReader(batch),
		MultiFile: true,
	}
	_, err = engine.Apply(in)
	if err == nil {
		t.Fatalf("expected error from out-of-bounds op, got nil")
	}
	gotX, _ := os.ReadFile(x)
	if string(gotX) != "a\nb\nc\n" {
		t.Fatalf("x.txt should be unchanged after rollback, got %q", string(gotX))
	}
	gotY, _ := os.ReadFile(y)
	if string(gotY) != "p\nq\nr\n" {
		t.Fatalf("y.txt should be unchanged after rollback, got %q", string(gotY))
	}
}

// TestApplyMultiFileSuccessFlushes confirms the success path still
// writes to disk for every touched file (i.e. the post-loop flush
// happens, not just the suppression).
func TestApplyMultiFileSuccessFlushes(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	engine := &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "apply-success",
		DBPath: filepath.Join(dir, "state.db"),
	}
	x := filepath.Join(dir, "x.txt")
	y := filepath.Join(dir, "y.txt")
	if err := os.WriteFile(x, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(y, []byte("p\nq\nr\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Open(cmd.OpenInput{Path: x}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Open(cmd.OpenInput{Path: y}); err != nil {
		t.Fatal(err)
	}
	batch := "@" + x + "\ns 1:1 X1\n@" + y + "\ns 1:1 Y1\n"
	_, err = engine.Apply(cmd.ApplyInput{
		Stdin:     strings.NewReader(batch),
		MultiFile: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	gotX, _ := os.ReadFile(x)
	if string(gotX) != "X1\nb\nc\n" {
		t.Fatalf("x.txt: got %q", string(gotX))
	}
	gotY, _ := os.ReadFile(y)
	if string(gotY) != "Y1\nq\nr\n" {
		t.Fatalf("y.txt: got %q", string(gotY))
	}
}
