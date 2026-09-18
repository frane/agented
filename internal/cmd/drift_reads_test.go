package cmd_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/store"
)

// Reported by teal-goat-5721: `ae view` answered from the stored head with no
// drift detection, so a file edited outside ae read back as whatever ae last
// remembered — silently, with shifted line numbers. An audit run over a shared
// worktree nearly filed a critical false positive off one of these reads.
func TestViewReconcilesDiskDrift(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "one\ntwo\nthree\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	// Something outside ae rewrites the file: git checkout, another editor,
	// another agent.
	if err := os.WriteFile(p, []byte("one\nGUARD\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.View(cmd.ViewInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(res.View.Lines, "\n")
	if !strings.Contains(got, "GUARD") {
		t.Errorf("view served stale content, missing the on-disk line:\n%s", got)
	}
	if !strings.Contains(got, "2\tGUARD") {
		t.Errorf("view line numbers disagree with disk:\n%s", got)
	}
	if res.Warning == "" {
		t.Error("reconciled read must say so; silence is the bug")
	}
	if res.Stale {
		t.Error("content was reconciled, so it is not stale")
	}
}

func TestSearchReconcilesDiskDrift(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "alpha\nbeta\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.Search(cmd.SearchInput{Path: p, Pattern: "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Search.Matches) != 1 {
		t.Fatalf("search missed a line present on disk: %+v", res.Search.Matches)
	}
	if res.Search.Matches[0].Line != 3 {
		t.Errorf("match line %d, want 3", res.Search.Matches[0].Line)
	}
}

func TestFindReconcilesDiskDrift(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "alpha\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("alpha\nNEEDLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.Find(cmd.FindInput{Pattern: "NEEDLE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Find.Matches) != 1 {
		t.Fatalf("cross-file find missed a line present on disk: %+v", res.Find.Matches)
	}
}

// With reconciliation switched off the read still has to announce itself: a
// wrong answer that says so is recoverable, a silent one is not.
func TestViewReportsStaleWhenReconcileDisabled(t *testing.T) {
	e, dir := newEngine(t)
	e.Config.Concurrency.AutoLoadOnDrift = false
	p := writeFile(t, dir, "a.go", "one\ntwo\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.View(cmd.ViewInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stale {
		t.Error("drift served unreconciled must be flagged stale")
	}
	if !strings.Contains(res.Warning, "STALE") {
		t.Errorf("warning must name the problem: %q", res.Warning)
	}
	if strings.Contains(strings.Join(res.View.Lines, "\n"), "three") {
		t.Error("with reconcile off, view should still serve head, not disk")
	}
}

// Second bug: a file deleted from disk read back from ae's stored copy, and
// `ae open` wrote that copy's path back into existence. A deliberately removed
// component came back this way and broke the repo's orphan-component test.
func TestDeletedOnDiskIsReportedNotResurrected(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "gone.tsx", "export const A = 1\nexport const B = 2\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}

	if _, err := e.View(cmd.ViewInput{Path: p}); !errors.Is(err, store.ErrDeletedOnDisk) {
		t.Errorf("view of a deleted file: got %v, want ErrDeletedOnDisk", err)
	}
	if _, err := e.Open(cmd.OpenInput{Path: p}); !errors.Is(err, store.ErrDeletedOnDisk) {
		t.Errorf("open of a deleted file: got %v, want ErrDeletedOnDisk", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("open recreated a file that was deliberately deleted")
	}
	if _, err := e.Save(cmd.SaveInput{Path: p}); !errors.Is(err, store.ErrDeletedOnDisk) {
		t.Errorf("save of a deleted file: got %v, want ErrDeletedOnDisk", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("save restored a deliberately deleted file without --force")
	}

	// Restoring stays available, but it has to be asked for.
	if _, err := e.Save(cmd.SaveInput{Path: p, Force: true}); err != nil {
		t.Fatalf("save --force should restore: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "export const A = 1\nexport const B = 2\n" {
		t.Errorf("restored content: %q", b)
	}
}

// find must skip files that vanished rather than search a phantom copy of
// them — and say which ones it skipped.
func TestFindSkipsDeletedFiles(t *testing.T) {
	e, dir := newEngine(t)
	kept := writeFile(t, dir, "kept.go", "NEEDLE\n")
	gone := writeFile(t, dir, "gone.go", "NEEDLE\n")
	for _, p := range []string{kept, gone} {
		if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	res, err := e.Find(cmd.FindInput{Pattern: "NEEDLE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Find.Matches) != 1 {
		t.Fatalf("deleted file still searched: %+v", res.Find.Matches)
	}
	if !strings.Contains(res.Warning, "no longer exist on disk") {
		t.Errorf("skipped files must be reported: %q", res.Warning)
	}
}

// An unchanged file must not pick up a spurious edit on every read: the
// reconcile is a no-op when disk and head agree.
func TestReadWithoutDriftAddsNoEdit(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "one\ntwo\n")
	o, err := e.Open(cmd.OpenInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	before := o.Open.File.HeadEditID
	for i := 0; i < 3; i++ {
		if _, err := e.View(cmd.ViewInput{Path: p}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.View(cmd.ViewInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := e.Store.FileByID(*res.FileID)
	if err != nil {
		t.Fatal(err)
	}
	if fi.HeadEditID != before {
		t.Errorf("drift-free reads created edits: head %d -> %d", before, fi.HeadEditID)
	}
	if res.Warning != "" {
		t.Errorf("no drift, no warning; got %q", res.Warning)
	}
}

// read_drift=refuse: answer with nothing rather than reconcile or serve a
// known-stale copy. Exit code 3 at the CLI boundary.
func TestViewRefusesOnDriftWhenConfigured(t *testing.T) {
	e, dir := newEngine(t)
	e.Config.Concurrency.ReadDrift = "refuse"
	p := writeFile(t, dir, "a.go", "one\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.View(cmd.ViewInput{Path: p}); !errors.Is(err, store.ErrDriftRefused) {
		t.Errorf("got %v, want ErrDriftRefused", err)
	}
	// The tree was not touched.
	fi, err := e.Store.FileByPath(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if fi.LineCount != 1 {
		t.Errorf("refuse mode folded disk in anyway: %d lines", fi.LineCount)
	}
}

// The pre-existing auto_load_on_drift=false keeps meaning "don't touch the
// tree behind my back" without its owner having to learn read_drift.
func TestAutoLoadOnDriftFalseStillWarns(t *testing.T) {
	e, dir := newEngine(t)
	e.Config.Concurrency.AutoLoadOnDrift = false
	e.Config.Concurrency.ReadDrift = "" // unset, as an existing config would have it
	p := writeFile(t, dir, "a.go", "one\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.View(cmd.ViewInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stale {
		t.Error("expected the legacy bool to still force a stale-flagged read")
	}
}

// The persistent stamp is what makes reconcile-on-read affordable for the
// CLI, where every invocation is a new process with a cold in-process cache.
func TestDiskStampPersistsAcrossEngines(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "one\ntwo\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	res, err := e.View(cmd.ViewInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	st, err := e.Store.DiskStampGet(*res.FileID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Valid {
		t.Fatal("no stamp recorded after a confirmed-clean read")
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size != fi.Size() || st.MtimeNanos != fi.ModTime().UnixNano() {
		t.Errorf("stamp %+v does not match disk (size %d)", st, fi.Size())
	}
}
