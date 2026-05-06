package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
	"github.com/frane/agented/internal/workspace"
)

// Pool lazily constructs and caches one Engine per workspace, keyed by the
// absolute .agented/ directory. It exists so a single `ae serve` process can
// route MCP calls to whichever workspace owns the path argument — the model
// desktop hosts (Claude Desktop, etc.) need, where one MCP server registration
// is expected to handle every project the user mentions.
//
// The CLI does not use a Pool: each invocation has a single workspace and a
// short-lived Engine constructed in App.preRun. Pool only kicks in for
// long-running serve mode.
type Pool struct {
	mu          sync.Mutex
	engines     map[string]*Engine
	conns       map[string]*pooledConn
	actor       string
	cfgResolver func(wsDir string) (*config.Config, error)
	defaultWS   string
}

type pooledConn struct {
	closer func() error
}

// PoolOptions configures a new Pool.
type PoolOptions struct {
	// Actor is the resolved actor identity for every Engine in the pool.
	Actor string
	// CfgResolver returns the merged (global + project) config for a
	// workspace. The Pool calls it once per workspace on first use. When
	// nil, a global-only resolver is used.
	CfgResolver func(wsDir string) (*config.Config, error)
	// DefaultWorkspace, when non-empty, is used by Default() and as the
	// fallback for tool calls that don't carry a path argument.
	DefaultWorkspace string
}

// NewPool returns an empty pool. Engines are created on the first Resolve()
// or Default() call. Close() releases every cached SQLite handle.
func NewPool(opts PoolOptions) *Pool {
	resolver := opts.CfgResolver
	if resolver == nil {
		resolver = func(_ string) (*config.Config, error) {
			cfg, _, err := config.Resolve(config.GlobalPath(), "", nil)
			return cfg, err
		}
	}
	return &Pool{
		engines:     map[string]*Engine{},
		conns:       map[string]*pooledConn{},
		actor:       opts.Actor,
		cfgResolver: resolver,
		defaultWS:   opts.DefaultWorkspace,
	}
}

// Resolve returns the Engine for the workspace owning path. If path is empty
// or non-absolute, falls back to the default workspace. Errors when neither
// path nor default yields a workspace.
//
// Resolution rules:
//   - Absolute path: walk up to the nearest .agented/ via workspace.LocateForFile
//   - Relative or empty: use the default workspace
//   - Neither available: error with a message that names the missing piece.
func (p *Pool) Resolve(path string) (*Engine, error) {
	wsDir, err := p.resolveWorkspaceDir(path)
	if err != nil {
		return nil, err
	}
	return p.engineFor(wsDir)
}

// Default returns the engine for the pool's default workspace. Errors when no
// default was configured.
func (p *Pool) Default() (*Engine, error) {
	if p.defaultWS == "" {
		return nil, errors.New("no default workspace; pass an absolute path or set --workspace-dir at server startup")
	}
	return p.engineFor(p.defaultWS)
}

// HasDefault reports whether a default workspace is configured.
func (p *Pool) HasDefault() bool { return p.defaultWS != "" }

// Register inserts a pre-built Engine for wsDir into the pool. Used by
// `ae serve` to seed the pool with the cwd-resolved Engine the CLI's preRun
// already opened, avoiding a second SQLite handle on the same database. The
// Pool does not claim ownership of the DB connection — the caller (preRun /
// postRun) is responsible for closing it.
func (p *Pool) Register(wsDir string, e *Engine) error {
	abs, err := filepath.Abs(wsDir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.engines[abs]; exists {
		return fmt.Errorf("pool already has an engine for %s", abs)
	}
	p.engines[abs] = e
	// Empty closer marks this entry as externally-owned: Close() leaves the
	// DB handle alone for the registrar to close.
	p.conns[abs] = &pooledConn{closer: nil}
	if p.defaultWS == "" {
		p.defaultWS = abs
	}
	return nil
}

// DefaultDir returns the default workspace directory, or "" when none is set.
func (p *Pool) DefaultDir() string { return p.defaultWS }

// Close closes every cached SQLite connection. The Pool must not be used after.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var firstErr error
	for k, c := range p.conns {
		if c.closer != nil {
			if err := c.closer(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(p.conns, k)
		delete(p.engines, k)
	}
	return firstErr
}

func (p *Pool) resolveWorkspaceDir(path string) (string, error) {
	if path != "" && filepath.IsAbs(path) {
		// LocateForFile would silently fall through to ~/.agented/ when no
		// project workspace exists above path. For pool routing that's the
		// wrong default — we want a loud error so the user knows to run
		// `ae init`. Check isProject and reject the global fallback.
		dir, isProject, err := workspace.LocateForFile(path, "", workspace.LocateOptions{
			AutoCreate:      "false",
			NoAutoWorkspace: true,
		})
		if err == nil && dir != "" && isProject {
			return dir, nil
		}
		return "", fmt.Errorf("no .agented/ workspace found at or above %s — run `ae init` in the project root", path)
	}
	if p.defaultWS != "" {
		return p.defaultWS, nil
	}
	return "", errors.New("path argument required: this server has no default workspace")
}

func (p *Pool) engineFor(wsDir string) (*Engine, error) {
	abs, err := filepath.Abs(wsDir)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.engines[abs]; ok {
		return e, nil
	}
	if err := workspace.EnsureDir(abs); err != nil {
		return nil, err
	}
	dbPath := workspace.DBPath(abs)
	conn, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}
	cfg, err := p.cfgResolver(abs)
	if err != nil {
		conn.Close()
		return nil, err
	}
	e := &Engine{
		Store:  store.New(conn),
		Config: cfg,
		Actor:  p.actor,
		DBPath: dbPath,
	}
	p.engines[abs] = e
	p.conns[abs] = &pooledConn{closer: conn.Close}
	return e, nil
}
