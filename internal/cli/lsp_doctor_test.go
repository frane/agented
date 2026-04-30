package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorRow(t *testing.T) {
	var b bytes.Buffer
	doctorRow(&b, "typescript", "binary", "tsserver", "ok", "/path (5.1.3)")
	got := b.String()
	want := "doctor\ttypescript\tbinary\ttsserver\tok\t/path (5.1.3)\n"
	if got != want {
		t.Fatalf("doctorRow:\nwant %q\ngot  %q", want, got)
	}
}

func TestDoctorRowEmptySubjectBecomesDash(t *testing.T) {
	var b bytes.Buffer
	doctorRow(&b, "ide", "enabled", "", "ok", "true")
	got := b.String()
	if !strings.Contains(got, "\t-\t") {
		t.Fatalf("expected dash for empty subject, got %q", got)
	}
}

// TestDoctorConfigFilesGo: in a temp dir with go.mod present, the go
// language check should report ok for go.mod and info for go.sum (absent).
func TestDoctorConfigFilesGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	doctorConfigFiles(&b, "go", root)
	got := b.String()
	if !strings.Contains(got, "doctor\tgo\tconfig\tgo.mod\tok\t") {
		t.Fatalf("go.mod ok line missing: %s", got)
	}
	if !strings.Contains(got, "doctor\tgo\tconfig\tgo.sum\tinfo\t") {
		t.Fatalf("go.sum info line missing: %s", got)
	}
}

// TestDoctorConfigFilesGoMissing: without go.mod, the report fails.
func TestDoctorConfigFilesGoMissing(t *testing.T) {
	root := t.TempDir()
	var b bytes.Buffer
	doctorConfigFiles(&b, "go", root)
	got := b.String()
	if !strings.Contains(got, "doctor\tgo\tconfig\tgo.mod\tfail\t") {
		t.Fatalf("missing go.mod should fail: %s", got)
	}
}

// TestDoctorConfigFilesTypescript: package.json present, tsconfig.json
// absent, no eslint config, no node_modules.
func TestDoctorConfigFilesTypescript(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	doctorConfigFiles(&b, "typescript", root)
	got := b.String()
	for _, want := range []string{
		"doctor\ttypescript\tconfig\tpackage.json\tok\t",
		"doctor\ttypescript\tconfig\ttsconfig.json\tinfo\t",
		"doctor\ttypescript\tconfig\teslint\twarn\t",
		"doctor\ttypescript\tdeps\tnode_modules\twarn\t",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line: %q\nfull output:\n%s", want, got)
		}
	}
}

// TestDoctorConfigFilesTypescriptWithEslint: any of the .eslintrc.*
// family ticks the eslint check from warn to ok.
func TestDoctorConfigFilesTypescriptWithEslint(t *testing.T) {
	root := t.TempDir()
	for _, fname := range []string{".eslintrc.json", "package.json"} {
		if err := os.WriteFile(filepath.Join(root, fname), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	doctorConfigFiles(&b, "typescript", root)
	got := b.String()
	if !strings.Contains(got, "doctor\ttypescript\tconfig\teslint\tok\t") {
		t.Fatalf("with .eslintrc.json present, eslint check should be ok: %s", got)
	}
}

// TestDoctorConfigFilesPython: pyproject.toml present, no venv.
func TestDoctorConfigFilesPython(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// Strip any inherited VIRTUAL_ENV so the "no venv" branch fires
	// deterministically.
	t.Setenv("VIRTUAL_ENV", "")
	var b bytes.Buffer
	doctorConfigFiles(&b, "python", root)
	got := b.String()
	for _, want := range []string{
		"doctor\tpython\tconfig\tpyproject.toml\tok\t",
		"doctor\tpython\tvenv\t-\tinfo\t",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line: %q\nfull output:\n%s", want, got)
		}
	}
}

// TestDoctorConfigFilesPythonVirtualEnv: with $VIRTUAL_ENV set, the venv
// check reports the env var path.
func TestDoctorConfigFilesPythonVirtualEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VIRTUAL_ENV", "/fake/venv/path")
	var b bytes.Buffer
	doctorConfigFiles(&b, "python", root)
	got := b.String()
	if !strings.Contains(got, "doctor\tpython\tvenv\tVIRTUAL_ENV\tok\t/fake/venv/path") {
		t.Fatalf("VIRTUAL_ENV detection: %s", got)
	}
}

// TestDoctorConfigFilesRust: Cargo.toml required.
func TestDoctorConfigFilesRust(t *testing.T) {
	root := t.TempDir()
	var b bytes.Buffer
	doctorConfigFiles(&b, "rust", root)
	got := b.String()
	if !strings.Contains(got, "doctor\trust\tconfig\tCargo.toml\tfail\t") {
		t.Fatalf("missing Cargo.toml should fail: %s", got)
	}
	// Now add it.
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	doctorConfigFiles(&b, "rust", root)
	got = b.String()
	if !strings.Contains(got, "doctor\trust\tconfig\tCargo.toml\tok\t") {
		t.Fatalf("with Cargo.toml present, rust check should be ok: %s", got)
	}
}

// TestDoctorConfigFilesUnknownLanguage: a language we don't have specific
// checks for falls into the generic info line.
func TestDoctorConfigFilesUnknownLanguage(t *testing.T) {
	root := t.TempDir()
	var b bytes.Buffer
	doctorConfigFiles(&b, "haskell", root)
	got := b.String()
	if !strings.Contains(got, "doctor\thaskell\tconfig\t-\tinfo\tno language-specific config checks defined") {
		t.Fatalf("unknown lang fallback: %s", got)
	}
}

// TestProbeVersionMissing: probing a binary that doesn't exist returns
// the empty string instead of crashing.
func TestProbeVersionMissing(t *testing.T) {
	got := probeVersion("definitely-not-a-real-bin-xyz-123")
	if got != "" {
		t.Fatalf("missing-bin probe: want empty, got %q", got)
	}
}

func TestIfaceInt(t *testing.T) {
	if got := ifaceInt(nil); got != "" {
		t.Errorf("nil pid: want empty, got %q", got)
	}
	v := 12345
	if got := ifaceInt(&v); got != "12345" {
		t.Errorf("12345: %q", got)
	}
}
