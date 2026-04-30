package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Client is a minimal JSON-RPC client to one language server, communicating
// over the server's stdio. Only the LSP messages we use are implemented:
// initialize, initialized, didOpen, didChange, didClose, documentSymbol,
// workspaceSymbol, references, definition, and the publishDiagnostics
// notification.
type Client struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan rpcResponse

	onPublish func(uri string, version *int, diags []LSPDiagnostic)
	onLog     func(line string)
	closed    atomic.Bool
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// LSPDiagnostic is the subset of LSP Diagnostic we care about.
type LSPDiagnostic struct {
	Range struct {
		Start LSPPos `json:"start"`
		End   LSPPos `json:"end"`
	} `json:"range"`
	Severity int    `json:"severity"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// LSPPos is 0-indexed line/character per LSP spec.
type LSPPos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// LSPLocation is the result type for definition/references.
type LSPLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start LSPPos `json:"start"`
		End   LSPPos `json:"end"`
	} `json:"range"`
}

// LSPSymbolInfo is the workspace-level symbol record.
type LSPSymbolInfo struct {
	Name     string      `json:"name"`
	Kind     int         `json:"kind"`
	Location LSPLocation `json:"location"`
}

// LSPDocumentSymbol is the per-file hierarchical symbol record. We flatten it.
type LSPDocumentSymbol struct {
	Name     string              `json:"name"`
	Kind     int                 `json:"kind"`
	Range    LSPLocation         `json:"-"`
	Selection LSPLocation        `json:"-"`
	Children []LSPDocumentSymbol `json:"children,omitempty"`
	// Raw fields we unmarshal manually because the field name overlaps with
	// LSPLocation.
	RawRange struct {
		Start LSPPos `json:"start"`
		End   LSPPos `json:"end"`
	} `json:"range"`
	RawSelection struct {
		Start LSPPos `json:"start"`
		End   LSPPos `json:"end"`
	} `json:"selectionRange"`
}

// SpawnClient launches the named command, wires stdio, reads the server's
// initialize response, and returns a ready Client. The caller invokes
// onPublish/onLog to be notified about diagnostics and stderr lines.
func SpawnClient(ctx context.Context, command string, args []string, workspaceRoot string,
	onPublish func(uri string, version *int, diags []LSPDiagnostic),
	onLog func(line string),
) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(os.Environ(), "GOFLAGS=") // keep server's view clean
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", command, err)
	}
	c := &Client{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		pending:   map[int64]chan rpcResponse{},
		onPublish: onPublish,
		onLog:     onLog,
	}
	go c.readLoop()
	go c.stderrPump()

	// LSP initialize.
	rootURI := PathToURI(workspaceRoot)
	initParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"rootPath":  workspaceRoot,
		"workspaceFolders": []map[string]string{
			{"uri": rootURI, "name": filepath.Base(workspaceRoot)},
		},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{},
				"definition":         map[string]any{"linkSupport": false},
				"references":         map[string]any{},
				"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
			},
			"workspace": map[string]any{
				"symbol":           map[string]any{},
				"workspaceFolders": true,
			},
		},
	}
	var initResult json.RawMessage
	if err := c.call(ctx, "initialize", initParams, &initResult); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialized: %w", err)
	}
	return c, nil
}

// DidOpen tells the LSP a file is open. Required before per-file queries.
func (c *Client) DidOpen(uri, languageID string, version int, text string) error {
	return c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": languageID,
			"version":    version,
			"text":       text,
		},
	})
}

// DidChange forwards the new full text of a file at a new version.
func (c *Client) DidChange(uri string, version int, text string) error {
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": version,
		},
		"contentChanges": []map[string]any{{"text": text}},
	})
}

// DidClose tells the LSP we no longer track a file.
func (c *Client) DidClose(uri string) error {
	return c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
}

// WorkspaceSymbol queries the LSP for symbols matching name across the workspace.
func (c *Client) WorkspaceSymbol(ctx context.Context, name string) ([]LSPSymbolInfo, error) {
	var out []LSPSymbolInfo
	err := c.call(ctx, "workspace/symbol", map[string]any{"query": name}, &out)
	return out, err
}

// DocumentSymbol queries a single file. LSPs return either []SymbolInformation
// (legacy) or []DocumentSymbol (hierarchical). We normalize to flat SymbolInfo.
func (c *Client) DocumentSymbol(ctx context.Context, uri string) ([]LSPSymbolInfo, error) {
	var raw json.RawMessage
	err := c.call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	}, &raw)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	// Try the hierarchical form first.
	var docSyms []LSPDocumentSymbol
	if err := json.Unmarshal(raw, &docSyms); err == nil && len(docSyms) > 0 && docSyms[0].Name != "" {
		return flattenDocumentSymbols(uri, docSyms, ""), nil
	}
	var info []LSPSymbolInfo
	if err := json.Unmarshal(raw, &info); err == nil {
		return info, nil
	}
	return nil, fmt.Errorf("unrecognized documentSymbol result")
}

// References returns all reference locations for the symbol at uri:line:char.
func (c *Client) References(ctx context.Context, uri string, line, character int, includeDeclaration bool) ([]LSPLocation, error) {
	var out []LSPLocation
	err := c.call(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     LSPPos{Line: line, Character: character},
		"context":      map[string]any{"includeDeclaration": includeDeclaration},
	}, &out)
	return out, err
}

// Definition resolves the definition for the symbol at uri:line:char.
func (c *Client) Definition(ctx context.Context, uri string, line, character int) ([]LSPLocation, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     LSPPos{Line: line, Character: character},
	}, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	// LSP spec: result is Location | Location[] | LocationLink[]. We try array first.
	var arr []LSPLocation
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return arr, nil
	}
	var single LSPLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []LSPLocation{single}, nil
	}
	return nil, nil
}

// Shutdown sends shutdown + exit per LSP spec, then waits for the process to
// exit (best-effort). Idempotent.
func (c *Client) Shutdown(ctx context.Context) error {
	if c.closed.Load() {
		return nil
	}
	_ = c.call(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", nil)
	return c.Close()
}

// Close terminates the connection without negotiating shutdown.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = c.stdin.Close()
	_ = c.cmd.Process.Kill()
	_, _ = c.cmd.Process.Wait()
	return nil
}

// PID returns the spawned LSP process pid.
func (c *Client) PID() int { return c.cmd.Process.Pid }

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: marshalParams(params)}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("lsp %s: %s", method, resp.Error.Message)
		}
		if result != nil && len(resp.Result) > 0 {
			if rm, ok := result.(*json.RawMessage); ok {
				*rm = append((*rm)[:0], resp.Result...)
				return nil
			}
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

func (c *Client) notify(method string, params any) error {
	return c.send(rpcRequest{JSONRPC: "2.0", Method: method, Params: marshalParams(params)})
}

func marshalParams(params any) json.RawMessage {
	if params == nil {
		return nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return b
}

func (c *Client) send(req rpcRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func (c *Client) readLoop() {
	r := bufio.NewReader(c.stdout)
	for {
		body, err := readMessage(r)
		if err != nil {
			return
		}
		var resp rpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			continue
		}
		if resp.Method != "" && resp.ID == nil {
			c.handleNotification(resp.Method, resp.Params)
			continue
		}
		if resp.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*resp.ID]
			c.mu.Unlock()
			if ok {
				ch <- resp
			}
		}
	}
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p struct {
			URI         string          `json:"uri"`
			Version     *int            `json:"version,omitempty"`
			Diagnostics []LSPDiagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(params, &p); err == nil && c.onPublish != nil {
			c.onPublish(p.URI, p.Version, p.Diagnostics)
		}
	case "window/logMessage", "window/showMessage":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(params, &p); err == nil && c.onLog != nil {
			c.onLog(p.Message)
		}
	}
}

func (c *Client) stderrPump() {
	r := bufio.NewReader(c.stderr)
	for {
		line, err := r.ReadString('\n')
		if line != "" && c.onLog != nil {
			c.onLog(strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			return
		}
	}
}

// readMessage reads one LSP message: a Content-Length header followed by JSON.
func readMessage(r *bufio.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if cl, ok := parseContentLength(line); ok {
			contentLength = cl
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("lsp: bad content-length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func parseContentLength(line string) (int, bool) {
	const prefix = "Content-Length:"
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[len(prefix):]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// PathToURI converts an absolute file path to a file:// URI per LSP spec.
func PathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Use url.URL so character escaping is correct on weird paths.
	u := url.URL{Scheme: "file", Path: abs}
	return u.String()
}

// URIToPath converts a file:// URI back to a filesystem path.
func URIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return strings.TrimPrefix(uri, "file://")
	}
	if u.Scheme != "file" {
		return uri
	}
	return u.Path
}

// flattenDocumentSymbols walks the hierarchical symbol tree into a flat list.
func flattenDocumentSymbols(uri string, syms []LSPDocumentSymbol, parent string) []LSPSymbolInfo {
	var out []LSPSymbolInfo
	for _, s := range syms {
		name := s.Name
		if parent != "" {
			name = parent + "." + s.Name
		}
		var loc LSPLocation
		loc.URI = uri
		loc.Range.Start = s.RawSelection.Start
		loc.Range.End = s.RawSelection.End
		out = append(out, LSPSymbolInfo{Name: name, Kind: s.Kind, Location: loc})
		if len(s.Children) > 0 {
			out = append(out, flattenDocumentSymbols(uri, s.Children, name)...)
		}
	}
	return out
}

// LSPKindName converts an LSP SymbolKind integer to the agent-facing string.
// Reference: SymbolKind in LSP 3.17.
func LSPKindName(kind int) string {
	switch kind {
	case 1:
		return "module"
	case 2:
		return "module"
	case 3:
		return "module"
	case 4:
		return "module"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "field"
	case 8:
		return "field"
	case 9:
		return "method"
	case 10:
		return "type"
	case 11:
		return "interface"
	case 12:
		return "func"
	case 13:
		return "var"
	case 14:
		return "const"
	case 15, 16, 17, 18, 19, 20, 21:
		return "var"
	case 22:
		return "type"
	case 23:
		return "type"
	case 24:
		return "field"
	case 25:
		return "type"
	case 26:
		return "type"
	}
	return "var"
}

// LSPSeverityName maps LSP severity integer to the fixed agent vocabulary.
// 1=error, 2=warning, 3=info, 4=hint.
func LSPSeverityName(sev int) Severity {
	switch sev {
	case 1:
		return SevError
	case 2:
		return SevWarn
	case 3:
		return SevInfo
	case 4:
		return SevHint
	}
	return SevInfo
}
