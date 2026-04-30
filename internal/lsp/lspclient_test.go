package lsp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frane/agented/internal/lsp"
)

func findGopls(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("gopls"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, "go", "bin", "gopls")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if out, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
		gopath := strings.TrimSpace(string(out))
		if gopath != "" {
			p := filepath.Join(gopath, "bin", "gopls")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	for _, candidate := range []string{
		"/opt/homebrew/bin/gopls",
		"/usr/local/bin/gopls",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func TestSpawnGoplsAndDocumentSymbol(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	gopls := findGopls(t)
	if gopls == "" {
		t.Skip("gopls not installed")
	}

	dir := t.TempDir()
	src := `package main

import "fmt"

type Greeter struct {
	Name string
}

func (g Greeter) Greet() string {
	return "hello, " + g.Name
}

func main() {
	g := Greeter{Name: "world"}
	fmt.Println(g.Greet())
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var diags []lsp.LSPDiagnostic
	var diagURI string
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := lsp.SpawnClient(ctx, gopls, nil, dir,
		func(uri string, _ *int, ds []lsp.LSPDiagnostic) {
			mu.Lock()
			defer mu.Unlock()
			diagURI = uri
			diags = ds
		}, nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer c.Shutdown(ctx)

	uri := lsp.PathToURI(srcPath)
	if err := c.DidOpen(uri, "go", 1, src); err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	syms, err := c.DocumentSymbol(ctx, uri)
	if err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	if len(syms) == 0 {
		t.Fatalf("expected symbols, got none")
	}
	hasGreeter := false
	for _, s := range syms {
		if strings.Contains(s.Name, "Greeter") {
			hasGreeter = true
			break
		}
	}
	if !hasGreeter {
		t.Fatalf("Greeter not in symbols: %+v", syms)
	}

	// Give gopls a moment to publish diagnostics. Either zero diagnostics or
	// a non-error diagnostic on this clean file is fine — we only assert the
	// callback fires for the right URI.
	time.Sleep(2 * time.Second)
	mu.Lock()
	gotURI := diagURI
	gotDiags := diags
	mu.Unlock()
	if gotURI != "" && gotURI != uri {
		t.Fatalf("publishDiagnostics URI mismatch: got %s want %s (diags=%v)", gotURI, uri, gotDiags)
	}
}
