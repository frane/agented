package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mclient "github.com/mark3labs/mcp-go/client"
	mctrans "github.com/mark3labs/mcp-go/client/transport"
)

// TestScenario12_MCPParity exercises a few representative tools over MCP and
// verifies they produce sensible JSON results. The point is parity: each
// tool's existence and basic behavior matches the CLI.
func TestScenario12_MCPParity(t *testing.T) {
	dir := t.TempDir()
	// Init workspace via CLI binary first.
	cmd := exec.Command(aeBin, "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "AE_ACTOR=mcptester")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, b)
	}
	// Write a file.
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Start ae serve as stdio MCP server.
	serverCmd := exec.Command(aeBin, "serve")
	serverCmd.Dir = dir
	serverCmd.Env = append(os.Environ(), "AE_ACTOR=mcptester")
	stdin, err := serverCmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := serverCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := serverCmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = stderr
	if err := serverCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = serverCmd.Process.Kill()
		serverCmd.Wait()
	}()
	transport := mctrans.NewIO(stdout, stdin, os.Stderr)
	if err := transport.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	cli := mclient.NewClient(transport)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = cli.Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "ae-test", Version: "1.0.0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// List tools.
	tools, err := cli.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	wantTools := []string{"ae_open", "ae_view", "ae_replace", "ae_branches", "ae_undo"}
	have := map[string]bool{}
	for _, tool := range tools.Tools {
		have[tool.Name] = true
	}
	for _, n := range wantTools {
		if !have[n] {
			t.Errorf("missing tool: %s", n)
		}
	}
	// Call ae_open and verify state_token returned.
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      "ae_open",
			Arguments: map[string]any{"path": path},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	// JSON content
	if tc, ok := res.Content[0].(mcpgo.TextContent); ok {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, tc.Text)
		}
		if _, ok := parsed["StateToken"]; !ok {
			t.Errorf("ae_open response missing StateToken: %v", parsed)
		}
	} else {
		t.Errorf("unexpected content type: %T", res.Content[0])
	}
}
