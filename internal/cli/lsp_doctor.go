package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/lsp"
)

func newLSPDoctorCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "doctor [language]",
		Aliases: []string{"dr"},
		Short:   "Diagnose LSP setup: binary, config files, daemon state",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if a.cfg == nil {
				return wrapErrCode(2, errors.New("config not loaded"))
			}
			wsDir, err := workspaceDirFromEngine(a)
			if err != nil {
				return wrapErrCode(2, err)
			}
			wsRoot := workspaceRootFromDir(wsDir)

			out := a.Stdout

			// Top-level: ide.enabled.
			if a.cfg.IDE.Enabled {
				doctorRow(out, "ide", "enabled", "-", "ok", "true")
			} else {
				doctorRow(out, "ide", "enabled", "-", "warn", "set ide.enabled: true in .agented/config.json")
			}

			target := ""
			if len(args) == 1 {
				target = args[0]
			}

			languages := []string{}
			for name := range a.cfg.IDE.Languages {
				if target == "" || name == target {
					languages = append(languages, name)
				}
			}
			sort.Strings(languages)

			if target != "" && len(languages) == 0 {
				doctorRow(out, target, "language", "-", "fail", "not configured in ide.languages")
				return nil
			}

			var statusRows []lsp.LSPStatus
			if a.engine != nil && a.engine.Store != nil {
				statusRows, _ = lsp.ListStatus(a.engine.Store.DB())
			}

			for _, lang := range languages {
				cfg := a.cfg.IDE.Languages[lang]
				doctorAutoStart(out, lang, cfg)
				doctorBinaries(out, lang, cfg)
				doctorConfigFiles(out, lang, wsRoot)
				doctorDaemon(out, lang, cfg, statusRows)
			}
			return nil
		},
	}
	return c
}

// doctorRow writes one tab-delimited diagnostic line:
//
//	doctor  <lang>  <check>  <subject>  <result>  <detail>
//
// result is one of: ok | warn | fail | info.
func doctorRow(w io.Writer, lang, check, subject, result, detail string) {
	if subject == "" {
		subject = "-"
	}
	fmt.Fprintf(w, "doctor\t%s\t%s\t%s\t%s\t%s\n", lang, check, subject, result, detail)
}

func doctorAutoStart(w io.Writer, lang string, cfg config.IDELanguageCfg) {
	if cfg.AutoStart {
		doctorRow(w, lang, "auto_start", "-", "ok", "true")
	} else {
		doctorRow(w, lang, "auto_start", "-", "warn", "false; daemon will not spawn this language unless you set auto_start: true")
	}
}

func doctorBinaries(w io.Writer, lang string, cfg config.IDELanguageCfg) {
	servers := cfg.ResolvedServers()
	if len(servers) == 0 {
		doctorRow(w, lang, "servers", "-", "fail", "no servers configured")
		return
	}
	for _, s := range servers {
		name := s.Name
		if name == "" {
			name = s.Command
		}
		path, err := exec.LookPath(s.Command)
		if err != nil {
			doctorRow(w, lang, "binary", name, "fail", fmt.Sprintf("%q not on PATH", s.Command))
			continue
		}
		version := probeVersion(s.Command)
		detail := path
		if version != "" {
			detail = path + " (" + version + ")"
		}
		doctorRow(w, lang, "binary", name, "ok", detail)
	}
}

// probeVersion runs <bin> --version with a short timeout and returns the
// first non-empty line of output, or "" on any failure. Best-effort; some
// LSPs use --version, others -V, others don't print anything useful.
func probeVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil || len(out) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 80 {
				line = line[:80] + "..."
			}
			return line
		}
	}
	return ""
}

// doctorConfigFiles checks for language-specific config files in the
// workspace root. Each language has its own list of expected files:
//   - go:         go.mod (required)
//   - typescript: package.json, tsconfig.json, .eslintrc (when eslint is wired)
//   - python:     pyproject.toml or setup.py, pyrightconfig.json (optional), venv detection
//   - rust:       Cargo.toml (required)
func doctorConfigFiles(w io.Writer, lang, root string) {
	switch lang {
	case "go":
		checkRequiredFile(w, lang, root, "go.mod", "without go.mod, gopls treats files as ad-hoc and produces noisy errors")
		checkOptionalFile(w, lang, root, "go.sum", "ok if you haven't fetched deps yet")
	case "typescript":
		checkRequiredFile(w, lang, root, "package.json", "tsserver needs the package context to resolve imports")
		checkOptionalFile(w, lang, root, "tsconfig.json", "without it tsserver falls back to defaults; project errors may be noisy")
		// eslint scaffolding: any of these is fine.
		eslintCfgs := []string{".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.json", ".eslintrc.yaml", ".eslintrc.yml", "eslint.config.js", "eslint.config.mjs", "eslint.config.ts"}
		hit := ""
		for _, f := range eslintCfgs {
			if _, err := os.Stat(filepath.Join(root, f)); err == nil {
				hit = f
				break
			}
		}
		if hit != "" {
			doctorRow(w, lang, "config", "eslint", "ok", filepath.Join(root, hit))
		} else {
			doctorRow(w, lang, "config", "eslint", "warn", "no .eslintrc.* found; eslint will spawn but report no diagnostics")
		}
		// node_modules presence is a strong hint about install state.
		if _, err := os.Stat(filepath.Join(root, "node_modules")); err != nil {
			doctorRow(w, lang, "deps", "node_modules", "warn", "missing; run npm install / pnpm install / yarn before expecting type info")
		} else {
			doctorRow(w, lang, "deps", "node_modules", "ok", filepath.Join(root, "node_modules"))
		}
	case "python":
		hasPyProject := fileExistsAt(root, "pyproject.toml")
		hasSetupPy := fileExistsAt(root, "setup.py")
		hasSetupCfg := fileExistsAt(root, "setup.cfg")
		switch {
		case hasPyProject:
			doctorRow(w, lang, "config", "pyproject.toml", "ok", filepath.Join(root, "pyproject.toml"))
		case hasSetupPy || hasSetupCfg:
			which := "setup.py"
			if hasSetupCfg {
				which = "setup.cfg"
			}
			doctorRow(w, lang, "config", which, "ok", filepath.Join(root, which))
		default:
			doctorRow(w, lang, "config", "pyproject.toml", "warn", "no pyproject.toml / setup.py / setup.cfg; ruff and pyright will run with weak project context")
		}
		checkOptionalFile(w, lang, root, "pyrightconfig.json", "optional; pyright reads pyproject.toml [tool.pyright] otherwise")
		// Venv detection: $VIRTUAL_ENV, .venv/, venv/.
		venv := os.Getenv("VIRTUAL_ENV")
		switch {
		case venv != "":
			doctorRow(w, lang, "venv", "VIRTUAL_ENV", "ok", venv)
		case fileExistsAt(root, ".venv", "bin", "python"):
			doctorRow(w, lang, "venv", ".venv", "warn", "found .venv/ but VIRTUAL_ENV not set; pyright may pick system Python instead")
		case fileExistsAt(root, "venv", "bin", "python"):
			doctorRow(w, lang, "venv", "venv", "warn", "found venv/ but VIRTUAL_ENV not set; pyright may pick system Python instead")
		default:
			doctorRow(w, lang, "venv", "-", "info", "no venv detected; pyright will use whichever python is on PATH")
		}
	case "rust":
		checkRequiredFile(w, lang, root, "Cargo.toml", "rust-analyzer needs a Cargo workspace; outside one it spawns but does nothing useful")
		checkOptionalFile(w, lang, root, "rust-toolchain.toml", "pin a specific toolchain")
	default:
		doctorRow(w, lang, "config", "-", "info", "no language-specific config checks defined")
	}
}

func checkRequiredFile(w io.Writer, lang, root, name, why string) {
	p := filepath.Join(root, name)
	if _, err := os.Stat(p); err != nil {
		doctorRow(w, lang, "config", name, "fail", "missing in workspace root: "+why)
		return
	}
	doctorRow(w, lang, "config", name, "ok", p)
}

func checkOptionalFile(w io.Writer, lang, root, name, why string) {
	p := filepath.Join(root, name)
	if _, err := os.Stat(p); err != nil {
		doctorRow(w, lang, "config", name, "info", "not present ("+why+")")
		return
	}
	doctorRow(w, lang, "config", name, "ok", p)
}

func fileExistsAt(root string, parts ...string) bool {
	p := filepath.Join(append([]string{root}, parts...)...)
	_, err := os.Stat(p)
	return err == nil
}

func doctorDaemon(w io.Writer, lang string, cfg config.IDELanguageCfg, rows []lsp.LSPStatus) {
	servers := cfg.ResolvedServers()
	for _, s := range servers {
		name := s.Name
		if name == "" {
			name = s.Command
		}
		var hit *lsp.LSPStatus
		for i := range rows {
			if rows[i].Language == lang && rows[i].Server == name {
				hit = &rows[i]
				break
			}
		}
		if hit == nil {
			doctorRow(w, lang, "daemon", name, "warn", "no lsp_status row; daemon may not have spawned this server yet")
			continue
		}
		switch hit.State {
		case lsp.StateReady:
			detail := "pid=" + ifaceInt(hit.PID)
			doctorRow(w, lang, "daemon", name, "ok", detail+" state=ready")
		case lsp.StateStarting:
			doctorRow(w, lang, "daemon", name, "warn", "still starting; retry shortly")
		case lsp.StateCrashed:
			doctorRow(w, lang, "daemon", name, "fail", "crashed: "+hit.LastError)
		case lsp.StateStopped:
			doctorRow(w, lang, "daemon", name, "info", "stopped (run ae lsp --background to start)")
		}
	}
}

func ifaceInt(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
