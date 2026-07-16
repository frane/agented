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
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/mcp"
	"github.com/frane/agented/internal/store"
)

// newTestClient builds an in-process mcp client wrapping a real Engine and
// the same registered tools that ae serve exposes. No subprocess needed.
func newTestClient(t *testing.T) (*mcpclient.Client, *cmd.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	wsDir := filepath.Join(dir, ".agented")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(wsDir, "state.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	engine := &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "tester",
		DBPath: dbPath,
	}
	pool := cmd.NewPool(cmd.PoolOptions{Actor: "tester"})
	if err := pool.Register(wsDir, engine); err != nil {
		t.Fatal(err)
	}
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
	return cli, engine, dir
}

// TestMCPListsExpectedTools asserts exact two-way sync between the exported
// ToolNames list and what the server actually registers: a tool added to
// RegisterTools without a ToolNames entry (or vice versa) fails here.
func TestMCPListsExpectedTools(t *testing.T) {
	cli, _, _ := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := cli.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tool := range tools.Tools {
		have[tool.Name] = true
	}
	want := map[string]bool{}
	for _, n := range mcp.ToolNames {
		want[n] = true
		if !have[n] {
			t.Errorf("ToolNames lists %s but the server does not register it", n)
		}
	}
	for n := range have {
		if !want[n] {
			t.Errorf("server registers %s but ToolNames does not list it", n)
		}
	}
}

func TestMCPOpenReturnsStateToken(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, ok := res.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("unexpected content type: %T", res.Content[0])
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, tc.Text)
	}
	if _, ok := parsed["StateToken"]; !ok {
		t.Errorf("ae_open response missing StateToken: %v", parsed)
	}
}

func TestMCPReplaceAdvancesToken(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("hello\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	openRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := mcpToken(t, openRes)
	repRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name: "ae_replace",
			Arguments: map[string]any{
				"path": path, "start": float64(1), "end": float64(1),
				"with": "world\n", "expect": tok,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	newTok := mcpToken(t, repRes)
	if newTok == "" || newTok == tok {
		t.Errorf("token did not advance: %q -> %q", tok, newTok)
	}
}

func TestMCPViewMatchesFileContent(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}},
	})
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      "ae_view",
			Arguments: map[string]any{"path": path, "start": float64(1), "end": float64(3)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, _ := res.Content[0].(mcpgo.TextContent)
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(tc.Text, want) {
			t.Errorf("view missing %q: %s", want, tc.Text)
		}
	}
}

func TestMCPSearchReportsMatches(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("x\nfoo\ny\nfoo\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}},
	})
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      "ae_search",
			Arguments: map[string]any{"path": path, "pattern": "foo"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, _ := res.Content[0].(mcpgo.TextContent)
	if !strings.Contains(tc.Text, "foo") {
		t.Errorf("search response missing matches: %s", tc.Text)
	}
}

func mcpToken(t *testing.T, res *mcpgo.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(mcpgo.TextContent)
	if !ok {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
		return ""
	}
	if s, ok := parsed["StateToken"].(string); ok {
		return s
	}
	return ""
}

func TestMCPInsertDeleteUndoRedo(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("a\nb\nc\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	openRes, _ := cli.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}}})
	tok := mcpToken(t, openRes)

	insRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_insert", Arguments: map[string]any{"path": path, "after": float64(1), "text": "x\n", "expect": tok}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok2 := mcpToken(t, insRes)
	if tok2 == tok || tok2 == "" {
		t.Fatalf("insert did not advance token: %q -> %q", tok, tok2)
	}

	delRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_delete", Arguments: map[string]any{"path": path, "start": float64(2), "end": float64(2), "expect": tok2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mcpToken(t, delRes) == tok2 {
		t.Error("delete did not advance token")
	}

	undoRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_undo", Arguments: map[string]any{"path": path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = mcpToken(t, undoRes)

	redoRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_redo", Arguments: map[string]any{"path": path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mcpToken(t, redoRes) == "" {
		t.Error("redo returned empty token")
	}
}

func TestMCPMarksAndAnnotations(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}}})

	if _, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_mark_add", Arguments: map[string]any{"path": path, "name": "spot", "line": float64(2)}},
	}); err != nil {
		t.Fatal(err)
	}
	listRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_mark_list", Arguments: map[string]any{"path": path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := listRes.Content[0].(mcpgo.TextContent); !ok || !strings.Contains(tc.Text, "spot") {
		t.Errorf("mark_list missing spot: %v", listRes.Content)
	}

	if _, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_annotate_add", Arguments: map[string]any{"path": path, "content": "explore later"}},
	}); err != nil {
		t.Fatal(err)
	}
	annRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_annotate_list", Arguments: map[string]any{"path": path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := annRes.Content[0].(mcpgo.TextContent); !ok || !strings.Contains(tc.Text, "explore later") {
		t.Errorf("annotate_list missing entry: %v", annRes.Content)
	}
}

func TestMCPSaveAndLoad(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("init\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	openRes, _ := cli.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}}})
	tok := mcpToken(t, openRes)
	cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_replace", Arguments: map[string]any{"path": path, "start": float64(1), "end": float64(1), "with": "after\n", "expect": tok}},
	})
	if _, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_save", Arguments: map[string]any{"path": path}},
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "after\n" {
		t.Errorf("save did not persist content: %q", body)
	}
	os.WriteFile(path, []byte("disk-edited\n"), 0o644)
	if _, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_load", Arguments: map[string]any{"path": path}},
	}); err != nil {
		t.Fatal(err)
	}
	viewRes, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_view", Arguments: map[string]any{"path": path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, _ := viewRes.Content[0].(mcpgo.TextContent)
	if !strings.Contains(tc.Text, "disk-edited") {
		t.Errorf("load did not pull new content: %s", tc.Text)
	}
}

func TestMCPFindAcrossFiles(t *testing.T) {
	cli, _, dir := newTestClient(t)
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	os.WriteFile(a, []byte("alpha bravo\n"), 0o644)
	os.WriteFile(b, []byte("alphacentauri\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": a}}})
	cli.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": b}}})
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_find", Arguments: map[string]any{"pattern": "alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, _ := res.Content[0].(mcpgo.TextContent)
	if !strings.Contains(tc.Text, "a.txt") || !strings.Contains(tc.Text, "b.txt") {
		t.Errorf("find did not span both files: %s", tc.Text)
	}
}

func TestMCPTransactionBeginCommit(t *testing.T) {
	cli, _, dir := newTestClient(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("x\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: "ae_open", Arguments: map[string]any{"path": path}}})
	if _, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_begin", Arguments: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_commit", Arguments: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMCPWho(t *testing.T) {
	cli, _, _ := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "ae_who", Arguments: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, _ := res.Content[0].(mcpgo.TextContent)
	if !strings.Contains(tc.Text, "tester") {
		t.Errorf("who response missing actor: %s", tc.Text)
	}
}
