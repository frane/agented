package cmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

// TestAutoSaveVerifyHappyPath: regression test for v0.3.6 — the
// post-rename verify must NOT produce false positives on the
// happy path. After a normal replace, disk content matches head and
// saved=true is returned without spurious errors.
func TestAutoSaveVerifyHappyPath(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	engine := &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "race-test",
		DBPath: filepath.Join(dir, "state.db"),
	}
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o, _ := engine.Open(cmd.OpenInput{Path: path})
	res, err := engine.Replace(cmd.ReplaceInput{
		Path:   path,
		Start:  2,
		End:    2,
		With:   "TWO\n",
		Expect: o.StateToken,
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if res.Edit == nil || !res.Edit.Saved {
		t.Fatalf("expected saved=true, got %+v", res.Edit)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "one\nTWO\nthree\n" {
		t.Fatalf("disk content: got %q", string(got))
	}
}
