// Package cli wires the cobra command tree to the cmd Engine.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/actor"
	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
	"github.com/frane/agented/internal/workspace"
)

// Build constructs the root cobra command with all subcommands wired up.
// versionInfo carries -ldflags-supplied version metadata for `ae version`.
func Build(versionInfo cmd.VersionInput, stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	app := &App{
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
		Version: versionInfo,
	}
	root := &cobra.Command{
		Use:           "ae",
		Short:         "Stateful editor for LLM agents.",
		Version:       versionInfo.Version + " (commit " + versionInfo.Commit + ", built " + versionInfo.Date + ")",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return app.preRun(cmd, args)
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			return app.postRun(cmd, args)
		},
	}
	root.SetVersionTemplate("ae version {{.Version}}\n")
	root.PersistentFlags().StringVarP(&app.AsActor, "as", "a", "", "Actor identity for this invocation (overrides AE_ACTOR and config)")
	root.PersistentFlags().StringVar(&app.OutputFormat, "format", "", "Output format: tab | json")
	root.PersistentFlags().BoolVarP(&app.JSONFlag, "json", "j", false, "Shortcut for --format=json")
	root.PersistentFlags().BoolVarP(&app.Header, "header", "H", false, "Print a header line above tab output")
	root.PersistentFlags().StringVar(&app.WorkspaceOverride, "workspace-dir", "", "Path to .agented dir; overrides discovery")
	root.PersistentFlags().BoolVar(&app.NoAutoWorkspace, "no-auto-workspace", false, "Skip project-root auto-creation; fall back to global workspace")
	root.PersistentFlags().BoolVar(&app.NoColor, "no-color", false, "Disable ANSI color output (also honors NO_COLOR env var)")
	root.PersistentFlags().BoolVarP(&app.NoAutoLSP, "no-auto-lsp", "Z", false, "Skip auto-starting the LSP daemon for IDE-relevant verbs")
	root.PersistentFlags().BoolVarP(&app.NoDiagnostics, "no-diagnostics", "N", false, "Suppress diag lines on mutating verb responses")
	root.PersistentFlags().StringVarP(&app.DiagnosticsFlag, "diagnostics", "G", "", "Diagnostics severity filter: errors|warnings|all|none")

	registerVerbs(app, root)
	return root
}

// App carries flags and lazy state for all CLI invocations.
type App struct {
	Stdin             io.Reader
	Stdout, Stderr    io.Writer
	Version           cmd.VersionInput

	AsActor           string
	OutputFormat      string
	JSONFlag          bool
	Header            bool
	WorkspaceOverride string
	NoAutoWorkspace   bool
	NoColor           bool
	NoAutoLSP         bool
	NoDiagnostics     bool
	DiagnosticsFlag   string

	// resolved during preRun
	cfg     *config.Config
	sources config.Sources
	engine  *cmd.Engine
	conn    *_dbHandle
}

type _dbHandle struct {
	db   *coreDB
	path string
}

type coreDB = struct {
	closer func() error
}

// preRun resolves config/workspace/store/actor before each subcommand. We do
// this here rather than in main so subcommands can run independently.
func (a *App) preRun(c *cobra.Command, args []string) error {
	// Skip for the `init` and `version` and `who` and `config` subcommands
	// when there's no workspace.
	skip := map[string]bool{"init": true, "version": true}
	name := c.Name()
	if c.Parent() != nil && c.Parent().Name() == "config" {
		// config subcommands don't need the engine fully wired (most don't).
	}

	// Resolve config first.
	gpath := config.GlobalPath()
	wsDir := a.WorkspaceOverride
	var ppath string
	if wsDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		// Read the global config alone to find the auto-create policy.
		// Project config can't be read yet (we don't know the workspace).
		gcfg, _, gerr := config.Resolve(gpath, "", nil)
		autoCreate := "root-only"
		if gerr == nil && gcfg.Workspace.AutoCreate != "" {
			autoCreate = gcfg.Workspace.AutoCreate
		}
		// `init` and `version` skip workspace creation (init creates explicitly,
		// version doesn't need a DB).
		opts := workspace.LocateOptions{
			AutoCreate:      autoCreate,
			NoAutoWorkspace: a.NoAutoWorkspace || skip[name],
			Stderr:          a.Stderr,
		}
		// If the first positional argument is an absolute file path, use it
		// to seed discovery so the workspace tracks where the file lives, not
		// where the shell happens to be. This lets agents avoid passing
		// --workspace-dir on every call.
		filePath := firstAbsArg(args)
		dir, _, err := workspace.LocateForFile(filePath, cwd, opts)
		if err != nil {
			if !skip[name] {
				return err
			}
		}
		wsDir = dir
	}
	if wsDir != "" {
		ppath = workspace.ConfigProjectPath(wsDir)
	}
	cfg, srcs, err := config.Resolve(gpath, ppath, nil)
	if err != nil {
		return err
	}
	a.cfg = cfg
	a.sources = srcs
	if a.OutputFormat == "" {
		a.OutputFormat = cfg.Output.DefaultFormat
	}
	if a.JSONFlag {
		a.OutputFormat = "json"
	}

	// Determine actor.
	act, err := actor.Resolve(a.AsActor, cfg.Actor)
	if err != nil {
		return err
	}

	// Skip DB setup for verbs that don't need it.
	if skip[name] {
		a.engine = &cmd.Engine{Store: nil, Config: cfg, Actor: act, DBPath: ""}
		return nil
	}

	if wsDir == "" {
		return errors.New("no workspace found and no global path; run `ae init`")
	}
	if err := workspace.EnsureDir(wsDir); err != nil {
		return err
	}
	dbPath := workspace.DBPath(wsDir)
	conn, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	a.conn = &_dbHandle{db: &coreDB{closer: conn.Close}, path: dbPath}
	st := store.New(conn)
	a.engine = &cmd.Engine{Store: st, Config: cfg, Actor: act, DBPath: dbPath}

	// Skill subcommands manage the skill itself; never refuse to run them
	// because of a version mismatch on the very content they exist to fix.
	underSkill := c.HasParent() && c.Parent().Name() == "skill"

	// Skill version check (only when a skill is installed; warn-or-error per config).
	if !underSkill {
		if err := checkSkillVersion(a); err != nil {
			return err
		}
	}

	// Auto-maintenance (idle-tx auto-rollback, scheduled prune).
	if err := a.engine.AutoMaintenance(); err != nil {
		fmt.Fprintf(a.Stderr, "auto-maintenance: %v\n", err)
	}
	return nil
}

// postRun closes resources opened by preRun.
func (a *App) postRun(_ *cobra.Command, _ []string) error {
	if a.conn != nil && a.conn.db != nil && a.conn.db.closer != nil {
		_ = a.conn.db.closer()
	}
	return nil
}

// Execute runs the root command with the provided args. Returns the exit code.
func Execute(ctx context.Context, args []string, versionInfo cmd.VersionInput, stdin io.Reader, stdout, stderr io.Writer) int {
	root := Build(versionInfo, stdin, stdout, stderr)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		var ec *ExitError
		if errors.As(err, &ec) {
			fmt.Fprintln(stderr, "error:", ec.Err)
			return ec.Code
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// ExitError carries an exit code along with an underlying error so the root
// command can map domain errors to spec'd exit codes.
type ExitError struct {
	Code int
	Err  error
}

// Error implements error.
func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return ""
}

// Unwrap supports errors.Is/As.
func (e *ExitError) Unwrap() error { return e.Err }

// firstAbsArg returns the first positional argument that looks like an
// absolute file path. Returns "" if no such argument exists. Used by preRun
// to seed workspace discovery from the file argument when commands are
// invoked from outside the project directory.
func firstAbsArg(args []string) string {
	for _, a := range args {
		if a == "" {
			continue
		}
		// Skip flag-looking tokens. preRun receives positional args only,
		// but be defensive.
		if a[0] == '-' {
			continue
		}
		if filepath.IsAbs(a) {
			return a
		}
	}
	return ""
}
