package lsp

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/frane/agented/internal/config"
)

// Daemon hosts language servers, accepts socket connections from the CLI,
// and writes diagnostics to SQLite as LSPs deliver them.
type Daemon struct {
	DB            *sql.DB
	Config        *config.IDECfg
	WorkspaceRoot string
	WorkspaceDir  string
	LogWriter     io.Writer

	mu       sync.RWMutex
	clients  map[string][]*languageClient // language id -> all spawned servers
	openURIs map[string]openFile
	listener net.Listener
}

type languageClient struct {
	language string  // "go", "typescript", ...
	name     string  // server name within the language ("gopls", "eslint", ...)
	cfg      config.IDEServerCfg
	client   *Client
}

type openFile struct {
	language string
	uri      string
	path     string
	editID   *int64
	fileID   int64
	version  int
}

// NewDaemon constructs a Daemon. Call Run to take over the process.
func NewDaemon(db *sql.DB, cfg *config.IDECfg, workspaceRoot, workspaceDir string, logW io.Writer) *Daemon {
	if logW == nil {
		logW = io.Discard
	}
	return &Daemon{
		DB:            db,
		Config:        cfg,
		WorkspaceRoot: workspaceRoot,
		WorkspaceDir:  workspaceDir,
		LogWriter:     logW,
		clients:       map[string][]*languageClient{},
		openURIs:      map[string]openFile{},
	}
}

// SocketPath returns the canonical socket path inside .agented/.
func SocketPath(workspaceDir string) string { return filepath.Join(workspaceDir, "lsp.sock") }

// PIDPath returns the canonical pid file path.
func PIDPath(workspaceDir string) string { return filepath.Join(workspaceDir, "lsp.pid") }

// LogPath returns the canonical log path.
func LogPath(workspaceDir string) string { return filepath.Join(workspaceDir, "lsp.log") }

// Run blocks until SIGTERM/SIGINT or context cancellation.
func (d *Daemon) Run(ctx context.Context) error {
	if d.Config == nil || !d.Config.Enabled {
		return errors.New("ide.enabled is false")
	}
	pidFile := PIDPath(d.WorkspaceDir)
	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
			if alive, _ := processAlive(pid); alive {
				return fmt.Errorf("daemon already running, pid=%d", pid)
			}
		}
		_ = os.Remove(pidFile)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	defer os.Remove(pidFile)

	sockPath := SocketPath(d.WorkspaceDir)
	_ = os.Remove(sockPath)
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sockPath, err)
	}
	d.listener = listener
	defer os.Remove(sockPath)
	defer listener.Close()

	d.startLanguages(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go d.handleConn(ctx, conn)
		}
	}()

	select {
	case <-ctx.Done():
	case <-sigCh:
	}
	d.shutdownAll(ctx)
	return nil
}

func (d *Daemon) startLanguages(ctx context.Context) {
	// Drop status rows from prior runs. Stale "crashed" entries from a
	// previous daemon (e.g. gopls when Go isn't installed on this machine)
	// would otherwise leak into ae lsp status until the file is opened
	// fresh. Languages we're about to spawn re-create their rows below.
	_, _ = d.DB.Exec(`DELETE FROM lsp_status`)

	for lang, cfg := range d.Config.Languages {
		if !cfg.AutoStart {
			continue
		}
		for _, srv := range cfg.ResolvedServers() {
			name := serverName(srv)
			// Preflight: if the binary isn't on PATH, skip silently with a log
			// line. Avoids cluttering ae lsp status with crashed rows for
			// languages the user's machine doesn't even have installed.
			if _, err := exec.LookPath(srv.Command); err != nil {
				fmt.Fprintf(d.LogWriter, "skip %s/%s: %s not on PATH\n", lang, name, srv.Command)
				continue
			}
			if err := d.spawnServer(ctx, lang, name, srv); err != nil {
				fmt.Fprintf(d.LogWriter, "spawn %s/%s: %v\n", lang, name, err)
				_ = SetStatus(d.DB, lang, name, StateCrashed, nil, d.WorkspaceRoot, err.Error())
			}
		}
	}
}

// spawnServer starts one LSP server inside a language and tracks it on the
// language's slice. Multiple servers per language coexist; the first to
// register wins symbol/reference/definition queries; all contribute
// diagnostics tagged by server name.
func (d *Daemon) spawnServer(ctx context.Context, lang, name string, srv config.IDEServerCfg) error {
	_ = SetStatus(d.DB, lang, name, StateStarting, nil, d.WorkspaceRoot, "")
	onPub := func(uri string, version *int, ds []LSPDiagnostic) {
		d.recordDiagnostics(name, uri, ds)
	}
	onLog := func(line string) {
		fmt.Fprintf(d.LogWriter, "[%s/%s] %s\n", lang, name, line)
	}
	c, err := SpawnClient(ctx, srv.Command, srv.Args, d.WorkspaceRoot, onPub, onLog)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.clients[lang] = append(d.clients[lang], &languageClient{
		language: lang, name: name, cfg: srv, client: c,
	})
	d.mu.Unlock()
	pid := c.PID()
	_ = SetStatus(d.DB, lang, name, StateReady, &pid, d.WorkspaceRoot, "")
	return nil
}

// serverName picks the display name for a server config: explicit Name
// when set, otherwise the Command basename.
func serverName(srv config.IDEServerCfg) string {
	if srv.Name != "" {
		return srv.Name
	}
	return srv.Command
}

func (d *Daemon) recordDiagnostics(sourceServer string, uri string, lspDiags []LSPDiagnostic) {
	d.mu.RLock()
	open, ok := d.openURIs[uri]
	d.mu.RUnlock()
	var fileID int64
	// v0.3.0: always use NULL edit_id ("current"). Historical-snapshot
	// tagging is tracked but not yet wired through emit/query paths.
	var editID *int64 = nil
	if ok {
		fileID = open.fileID
	}
	if fileID == 0 {
		// Try direct path lookup, then EvalSymlinks (macOS /tmp -> /private/tmp).
		path := URIToPath(uri)
		fileID = lookupFileID(d.DB, path)
		if fileID == 0 {
			if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
				fileID = lookupFileID(d.DB, resolved)
			}
		}
		if fileID == 0 {
			return
		}
	}
	diags := make([]Diagnostic, 0, len(lspDiags))
	for _, ld := range lspDiags {
		var endLine, endCol *int
		el, ec := ld.Range.End.Line+1, ld.Range.End.Character+1
		endLine = &el
		endCol = &ec
		ruleID := ""
		if s, ok := ld.Code.(string); ok {
			ruleID = s
		}
		diags = append(diags, Diagnostic{
			Severity: LSPSeverityName(ld.Severity),
			Line:     ld.Range.Start.Line + 1,
			Col:      ld.Range.Start.Character + 1,
			EndLine:  endLine,
			EndCol:   endCol,
			Message:  ld.Message,
			Source:   ld.Source,
			RuleID:   ruleID,
		})
	}
	if err := ReplaceDiagnostics(d.DB, fileID, editID, sourceServer, diags); err != nil {
		fmt.Fprintf(d.LogWriter, "record diagnostics: %v\n", err)
	}
}

func (d *Daemon) shutdownAll(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, list := range d.clients {
		for _, lc := range list {
			_ = lc.client.Shutdown(deadline)
			_ = SetStatus(d.DB, lc.language, lc.name, StateStopped, nil, d.WorkspaceRoot, "")
		}
	}
	d.clients = map[string][]*languageClient{}
}

// LanguageFor returns the language id configured for path's extension, or "".
func (d *Daemon) LanguageFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if d.Config == nil {
		return ""
	}
	return d.Config.Extensions[ext]
}

// EnsureOpen registers a file with the right LSP, sending didOpen if it isn't
// already tracked. Caller passes file_id and (optional) edit_id so future
// publishDiagnostics rows are tagged correctly.
func (d *Daemon) EnsureOpen(uri, path string, fileID int64, editID *int64, content string) {
	lang := d.LanguageFor(path)
	if lang == "" {
		return
	}
	d.mu.RLock()
	clients, ok := d.clients[lang]
	d.mu.RUnlock()
	if !ok || len(clients) == 0 {
		return
	}
	d.mu.Lock()
	existing, isOpen := d.openURIs[uri]
	d.mu.Unlock()
	if !isOpen {
		// Tell every server in this language about the file. Each gets its
		// own internal model of the buffer; diagnostics from each will be
		// tagged with the server name in recordDiagnostics.
		for _, lc := range clients {
			_ = lc.client.DidOpen(uri, lang, 1, content)
		}
		d.mu.Lock()
		d.openURIs[uri] = openFile{language: lang, uri: uri, path: path, fileID: fileID, editID: editID, version: 1}
		d.mu.Unlock()
		return
	}
	if existing.fileID != fileID {
		d.mu.Lock()
		existing.fileID = fileID
		existing.editID = editID
		d.openURIs[uri] = existing
		d.mu.Unlock()
	}
}

// NotifyChange forwards the change to the LSP and updates internal version.
func (d *Daemon) NotifyChange(uri, path string, fileID int64, editID *int64, content string) {
	lang := d.LanguageFor(path)
	if lang == "" {
		return
	}
	d.mu.Lock()
	clients, hasLang := d.clients[lang]
	if !hasLang || len(clients) == 0 {
		d.mu.Unlock()
		return
	}
	open, exists := d.openURIs[uri]
	if !exists {
		open = openFile{language: lang, uri: uri, path: path, fileID: fileID, editID: editID, version: 0}
	}
	open.version++
	open.fileID = fileID
	open.editID = editID
	d.openURIs[uri] = open
	d.mu.Unlock()
	for _, lc := range clients {
		if !exists {
			_ = lc.client.DidOpen(uri, lang, open.version, content)
		} else {
			_ = lc.client.DidChange(uri, open.version, content)
		}
	}
}

func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		req, err := DecodeRequest(r)
		if err != nil {
			return
		}
		resps := d.dispatch(ctx, req)
		if err := EncodeResponses(conn, resps); err != nil {
			return
		}
	}
}

func (d *Daemon) dispatch(ctx context.Context, req *Request) []Response {
	switch req.Verb {
	case "ping":
		return []Response{{Kind: "ok"}}
	case "sym":
		return d.handleSymbols(ctx, req)
	case "wsym":
		return d.handleWorkspaceSymbols(ctx, req)
	case "ref":
		return d.handleReferences(ctx, req)
	case "def":
		return d.handleDefinition(ctx, req)
	case "notify":
		return d.handleNotify(ctx, req)
	}
	return ErrorResponse("unknown_verb: " + req.Verb)
}

func (d *Daemon) firstReadyClient() *languageClient {
	d.mu.RLock()
	defer d.mu.RUnlock()
	// Prefer "go" since v0.3 ships with gopls validated; fall back to any.
	if list, ok := d.clients["go"]; ok && len(list) > 0 {
		return list[0]
	}
	for _, list := range d.clients {
		if len(list) > 0 {
			return list[0]
		}
	}
	return nil
}

// clientForPath returns the first server in the language's server list,
// which is the one that answers symbol/reference/definition queries.
// Diagnostics-only servers later in the list don't handle these queries.
func (d *Daemon) clientForPath(path string) *languageClient {
	lang := d.LanguageFor(path)
	if lang == "" {
		return d.firstReadyClient()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if list, ok := d.clients[lang]; ok && len(list) > 0 {
		return list[0]
	}
	return nil
}

func (d *Daemon) handleSymbols(ctx context.Context, req *Request) []Response {
	if len(req.Args) == 0 {
		return ErrorResponse("usage: sym <path>")
	}
	path := req.Args[0]
	abs, _ := filepath.Abs(path)
	lc := d.clientForPath(abs)
	if lc == nil {
		return ErrorResponse("no language server for " + filepath.Ext(path))
	}
	uri := PathToURI(abs)
	d.ensureFileOpenForLSP(abs, uri)
	syms, err := lc.client.DocumentSymbol(ctx, uri)
	if err != nil {
		return ErrorResponse(err.Error())
	}
	out := make([]Response, 0, len(syms))
	for _, s := range syms {
		loc := FormatLocation(URIToPath(s.Location.URI), s.Location.Range.Start.Line+1, s.Location.Range.Start.Character+1)
		out = append(out, SymbolResponse(LSPKindName(s.Kind), loc, s.Name))
	}
	return out
}

func (d *Daemon) handleWorkspaceSymbols(ctx context.Context, req *Request) []Response {
	query := ""
	if len(req.Args) > 0 {
		query = strings.Join(req.Args, " ")
	}
	lc := d.firstReadyClient()
	if lc == nil {
		return ErrorResponse("no language server running")
	}
	syms, err := lc.client.WorkspaceSymbol(ctx, query)
	if err != nil {
		return ErrorResponse(err.Error())
	}
	out := make([]Response, 0, len(syms))
	for _, s := range syms {
		loc := FormatLocation(URIToPath(s.Location.URI), s.Location.Range.Start.Line+1, s.Location.Range.Start.Character+1)
		out = append(out, SymbolResponse(LSPKindName(s.Kind), loc, s.Name))
	}
	return out
}

func (d *Daemon) handleReferences(ctx context.Context, req *Request) []Response {
	if len(req.Args) == 0 {
		return ErrorResponse("usage: ref <symbol>")
	}
	name := req.Args[0]
	lc := d.firstReadyClient()
	if lc == nil {
		return ErrorResponse("no language server running")
	}
	syms, err := lc.client.WorkspaceSymbol(ctx, name)
	if err != nil {
		return ErrorResponse(err.Error())
	}
	if len(syms) == 0 {
		return ErrorResponse("symbol not found: " + name)
	}
	first := syms[0]
	locs, err := lc.client.References(ctx, first.Location.URI, first.Location.Range.Start.Line, first.Location.Range.Start.Character, true)
	if err != nil {
		return ErrorResponse(err.Error())
	}
	out := make([]Response, 0, len(locs))
	for _, l := range locs {
		path := URIToPath(l.URI)
		line := l.Range.Start.Line + 1
		ctxLine := readContextLine(path, line)
		out = append(out, RefResponse(FormatLocation(path, line, l.Range.Start.Character+1), classifyUsage(name, ctxLine), strings.TrimSpace(ctxLine)))
	}
	return out
}

func (d *Daemon) handleDefinition(ctx context.Context, req *Request) []Response {
	if len(req.Args) == 0 {
		return ErrorResponse("usage: def <symbol> [path:line:col]")
	}
	name := req.Args[0]
	lc := d.firstReadyClient()
	if lc == nil {
		return ErrorResponse("no language server running")
	}
	if len(req.Args) >= 2 {
		path, line, col, err := ParseLocation(req.Args[1])
		if err != nil {
			return ErrorResponse(err.Error())
		}
		uri := PathToURI(path)
		locs, err := lc.client.Definition(ctx, uri, line-1, col-1)
		if err != nil {
			return ErrorResponse(err.Error())
		}
		if len(locs) == 0 {
			return []Response{{Kind: "def", Fields: []string{"none"}}}
		}
		l := locs[0]
		return []Response{DefResponse(FormatLocation(URIToPath(l.URI), l.Range.Start.Line+1, l.Range.Start.Character+1), name, "")}
	}
	syms, err := lc.client.WorkspaceSymbol(ctx, name)
	if err != nil {
		return ErrorResponse(err.Error())
	}
	if len(syms) == 0 {
		return []Response{{Kind: "def", Fields: []string{"none"}}}
	}
	first := syms[0]
	loc := FormatLocation(URIToPath(first.Location.URI), first.Location.Range.Start.Line+1, first.Location.Range.Start.Character+1)
	return []Response{DefResponse(loc, first.Name, LSPKindName(first.Kind))}
}

func (d *Daemon) handleNotify(_ context.Context, req *Request) []Response {
	if len(req.Args) < 2 {
		return ErrorResponse("usage: notify changed <path> [edit_id]")
	}
	if req.Args[0] != "changed" {
		return ErrorResponse("unknown_notify: " + req.Args[0])
	}
	path := req.Args[1]
	abs, _ := filepath.Abs(path)
	uri := PathToURI(abs)
	content := strings.Join(req.Content, "\n")
	if len(req.Content) > 0 {
		content += "\n"
	}
	row := d.DB.QueryRow(`SELECT id FROM files WHERE path = ? AND closed_at IS NULL`, abs)
	var fileID int64
	if err := row.Scan(&fileID); err != nil {
		return ErrorResponse("file not registered: " + abs)
	}
	var editID *int64
	if len(req.Args) >= 3 {
		v, err := strconv.ParseInt(req.Args[2], 10, 64)
		if err == nil {
			editID = &v
		}
	}
	d.NotifyChange(uri, abs, fileID, editID, content)
	return []Response{{Kind: "notify_ack"}}
}

// readContextLine reads one line (1-indexed) from a file. Used to attach a
// snippet of code to a reference response.
func readContextLine(path string, line int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for i := 1; sc.Scan(); i++ {
		if i == line {
			return sc.Text()
		}
	}
	return ""
}

// classifyUsage gives a coarse label for a reference using the surrounding
// line. Genuine static analysis would require a richer LSP, but this is what
// the v0.3 spec calls for: call, read, write, import, definition, other.
func classifyUsage(name, ctxLine string) string {
	t := strings.TrimSpace(ctxLine)
	switch {
	case strings.HasPrefix(t, "import ") || strings.Contains(t, `"`+name+`"`):
		return "import"
	case strings.Contains(t, name+"(") :
		return "call"
	case strings.Contains(t, name+" =") || strings.Contains(t, name+":="):
		return "write"
	case strings.HasPrefix(t, "func ") && strings.Contains(t, name):
		return "definition"
	case strings.HasPrefix(t, "type ") && strings.Contains(t, name):
		return "definition"
	}
	return "read"
}

// processAlive checks whether pid is a running process, OS-portable.
// processAlive checks whether pid is a running process. The implementation
// is platform-specific (see processalive_unix.go / processalive_windows.go).

// Connect dials the daemon socket. Returns ErrUnavailable when the daemon
// isn't running so callers can return lsp_unavailable to the agent.
func Connect(workspaceDir string) (net.Conn, error) {
	sock := SocketPath(workspaceDir)
	conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
	if err != nil {
		return nil, ErrUnavailable
	}
	return conn, nil
}

// ErrUnavailable indicates the daemon isn't reachable.
var ErrUnavailable = errors.New("lsp_unavailable")

// SendRequest dials the daemon, sends one request, and decodes the reply.
// Convenience wrapper for CLI verbs.
func SendRequest(workspaceDir string, req Request, timeout time.Duration) ([]Response, error) {
	conn, err := Connect(workspaceDir)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	if err := req.Encode(conn); err != nil {
		return nil, err
	}
	return DecodeResponses(bufio.NewReader(conn))
}

// ensureFileOpenForLSP reads the file from disk, looks up its file_id and
// head_edit_id in the store, and tells the LSP about it via didOpen. Called
// from per-file handlers when the daemon hasn't seen the file yet so gopls
// has source to analyze.
func (d *Daemon) ensureFileOpenForLSP(absPath, uri string) {
	d.mu.RLock()
	_, isOpen := d.openURIs[uri]
	d.mu.RUnlock()
	if isOpen {
		return
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	var fileID int64
	var editID *int64
	row := d.DB.QueryRow(`SELECT id FROM files WHERE path = ? AND closed_at IS NULL`, absPath)
	if err := row.Scan(&fileID); err != nil {
		// Not yet registered in the workspace — still tell the LSP, but skip
		// diagnostic tagging.
		fileID = 0
	} else {
		var head int64
		if err := d.DB.QueryRow(`SELECT edit_id FROM heads WHERE file_id = ?`, fileID).Scan(&head); err == nil {
			editID = &head
		}
	}
	d.EnsureOpen(uri, absPath, fileID, editID, string(content))
}

// lookupFileID returns the open file_id for path, or 0 if not registered.
func lookupFileID(db *sql.DB, path string) int64 {
	var fid int64
	row := db.QueryRow(`SELECT id FROM files WHERE path = ? AND closed_at IS NULL`, path)
	if err := row.Scan(&fid); err != nil {
		return 0
	}
	return fid
}
