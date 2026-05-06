package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mserver "github.com/mark3labs/mcp-go/server"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/mcp"
)

// Verifies that one mcp.Server backed by a Pool routes calls to the correct
// workspace based on the absolute path argument. This is the desktop-host
// scenario: a single `ae serve` registration handling multiple projects.
func TestPoolMultiWorkspaceRouting(t *testing.T) {
	tmp := t.TempDir()
	projA := filepath.Join(tmp, "project-a")
	projB := filepath.Join(tmp, "project-b")
	for _, p := range []string{projA, projB} {
		if err := os.MkdirAll(filepath.Join(p, ".agented"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Two distinct files, one in each project, with distinct content so we
	// can prove the open call hits the right workspace.
	fileA := filepath.Join(projA, "a.txt")
	fileB := filepath.Join(projB, "b.txt")
	if err := os.WriteFile(fileA, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("bravo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No default workspace; the pool must resolve from path args alone.
	pool := cmd.NewPool(cmd.PoolOptions{Actor: "tester"})
	t.Cleanup(func() { _ = pool.Close() })

	srv := mserver.NewMCPServer("agented", "test", mserver.WithToolCapabilities(false))
	mcp.RegisterTools(srv, pool, os.Stderr)
	cli, err := mcpclient.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cli.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "ae-mcp-test", Version: "1.0.0"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Open both files via the same MCP server; each call must land in the
	// right workspace's SQLite, not in a single shared one.
	openA, err := callTool(ctx, cli, "ae_open", map[string]any{"path": fileA})
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	openB, err := callTool(ctx, cli, "ae_open", map[string]any{"path": fileB})
	if err != nil {
		t.Fatalf("open B: %v", err)
	}

	// File ids start at 1 in each fresh workspace; if both came from the
	// same SQLite, the second open would have id=2.
	if got := openA["FileID"]; got == nil {
		t.Fatalf("open A missing FileID: %v", openA)
	}
	if got := openB["FileID"]; got == nil {
		t.Fatalf("open B missing FileID: %v", openB)
	}
	if openA["FileID"] != openB["FileID"] {
		t.Fatalf("expected each project to start ids at 1; got A=%v B=%v",
			openA["FileID"], openB["FileID"])
	}

	// Sanity: a path outside any .agented/ workspace must be rejected with
	// a clear init-suggestion message rather than silently using a default.
	stray := filepath.Join(tmp, "not-a-project", "stray.txt")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      "ae_open",
			Arguments: map[string]any{"path": stray},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected stray-path open to error, got: %#v", res)
	}
	body := errorText(res)
	if !strings.Contains(body, "no .agented/") {
		t.Errorf("expected init-suggestion error, got %q", body)
	}
}

// callTool calls a tool and decodes its JSON result into a generic map.
func callTool(ctx context.Context, cli *mcpclient.Client, name string, args map[string]any) (map[string]any, error) {
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, jsonError(res)
	}
	for _, c := range res.Content {
		if t, ok := c.(mcpgo.TextContent); ok {
			var out map[string]any
			if err := json.Unmarshal([]byte(t.Text), &out); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	return nil, nil
}

func errorText(res *mcpgo.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(mcpgo.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

type jsonErr struct{ msg string }

func (e jsonErr) Error() string { return e.msg }

func jsonError(res *mcpgo.CallToolResult) error {
	return jsonErr{msg: errorText(res)}
}
