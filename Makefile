# agented build & test orchestration.
#
# Targets:
#   make build      - native binary at ./ae
#   make test       - go test with -race -cover
#   make lint       - go vet (and staticcheck if installed)
#   make release    - cross-compiled binaries to ./dist
#   make install    - install ae to ~/.local/bin

BINARY      := ae
PKG         := ./cmd/ae
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
INSTALL_DIR ?= $(HOME)/.local/bin

GO_ENV      := CGO_ENABLED=0

.PHONY: build test test-short test-property bench lint release install clean fmt staticcheck stage-skill publish-skill stage-mcpb publish-smithery-skill publish-smithery-mcp stage-plugin verify-plugin-skill publish-all

build:
	$(GO_ENV) go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	$(GO_ENV) go test ./... -race -cover

test-short:
	$(GO_ENV) go test ./... -short -race

test-property:
	$(GO_ENV) go test ./test/property/... -race

bench:
	$(GO_ENV) go run ./cmd/ae-bench --output test/benchmark/results.md
	@echo "wrote test/benchmark/results.md"

lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./... ; else echo "(skipping staticcheck: not installed)" ; fi

fmt:
	gofmt -l -w .

# Cross-compiled release binaries.
release:
	@mkdir -p dist
	@for os in linux darwin windows ; do \
		for arch in amd64 arm64 ; do \
			[ "$$os/$$arch" = "windows/arm64" ] && continue ; \
			ext=""; [ "$$os" = "windows" ] && ext=".exe" ; \
			out=dist/$(BINARY)-$$os-$$arch$$ext ; \
			echo ">>> $$out" ; \
			$(GO_ENV) GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o $$out $(PKG) || exit 1 ; \
			shasum -a 256 $$out > $$out.sha256 ; \
		done ; \
	done

install: build
	@mkdir -p $(INSTALL_DIR)
	install -m 755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@if command -v xattr >/dev/null 2>&1 ; then xattr -c $(INSTALL_DIR)/$(BINARY) 2>/dev/null || true ; fi
	@echo "installed to $(INSTALL_DIR)/$(BINARY)"

clean:
	rm -f $(BINARY)
	rm -rf dist

# ClawHub publish workflow.
#
# `make stage-skill`   stages a clean publish folder at dist/clawhub/agented/
#                      with SKILL.md, LICENSE, CHANGELOG.md, README.md.
#                      No Go source, no clawhub-irrelevant files.
#
# `make publish-skill` runs stage-skill then `clawhub skill publish` with
#                      the version pulled from SKILL.md frontmatter.
#                      Requires `clawhub login` first.
#
# Override the version with: make publish-skill PUBLISH_SKILL_VERSION=1.3.0
PUBLISH_SKILL_DIR     := dist/clawhub/agented
PUBLISH_SKILL_VERSION ?= $(shell awk '/^version:/{print $$2; exit}' internal/skill/SKILL.md)

stage-skill:
	@scripts/stage-clawhub-skill.sh $(PUBLISH_SKILL_DIR)

publish-skill: stage-skill
	@if ! command -v clawhub >/dev/null 2>&1; then \
	  echo "error: clawhub CLI not on PATH. Install with: bun install -g clawhub" >&2 ; \
	  exit 1 ; \
	fi
	@if [ -z "$(PUBLISH_SKILL_VERSION)" ]; then \
	  echo "error: could not determine version from SKILL.md frontmatter" >&2 ; \
	  exit 1 ; \
	fi
	clawhub skill publish $(PUBLISH_SKILL_DIR) --version $(PUBLISH_SKILL_VERSION)

# Smithery publish targets.
#
# Skill: official `smithery skill publish` consumes the staged ClawHub
# folder directly (zips it, uploads, registers under namespace/agented).
# The folder already has SKILL.md + LICENSE + CHANGELOG + README.
#
# MCP: official `mcpb pack` (Anthropic) builds the .mcpb from a
# manifest-only staging folder. Then `smithery mcp publish` uploads
# the bundle to the MCP registry.
#
# Both require `smithery auth login` once. The MCP path also needs
# Node (npx is used to invoke @anthropic-ai/mcpb without a global
# install).
SMITHERY_NAMESPACE  ?= frane
SMITHERY_MCPB_SRC   := dist/smithery/mcpb-src
SMITHERY_MCPB       := dist/smithery/agented.mcpb

stage-mcpb: build
	@scripts/stage-mcpb.sh $(SMITHERY_MCPB_SRC)

publish-smithery-skill: stage-skill
	@command -v smithery >/dev/null 2>&1 || (echo "error: smithery CLI not on PATH. Install with: npm install -g @smithery/cli" >&2 ; exit 1)
	smithery skill publish $(PUBLISH_SKILL_DIR) -n agented

publish-smithery-mcp: stage-mcpb
	@command -v smithery >/dev/null 2>&1 || (echo "error: smithery CLI not on PATH. Install with: npm install -g @smithery/cli" >&2 ; exit 1)
	# .mcpb is a plain zip per spec. We bypass `mcpb pack` because it
	# rejects inputSchema on tool entries while Smithery's registry
	# requires it. zip preserves the manifest verbatim.
	rm -f $(SMITHERY_MCPB)
	cd $(SMITHERY_MCPB_SRC) && zip -qr $(abspath $(SMITHERY_MCPB)) .
	smithery mcp publish $(SMITHERY_MCPB) -n $(SMITHERY_NAMESPACE)/agented

publish-all: stage-plugin verify-plugin-skill publish-skill publish-smithery-skill publish-smithery-mcp
	@echo
	@echo "All catalog publishes complete."
	@echo "GitHub release / Homebrew cask / Claude+Codex+Gemini plugins update automatically when you push the new tag."

# Claude Code plugin sync.
#
# `make stage-plugin`         copies internal/skill/SKILL.md (the Go-embedded
#                             canonical) to plugin/skills/agented/SKILL.md and
#                             rewrites plugin/.claude-plugin/plugin.json with
#                             the current git tag. Both files are checked in
#                             so the plugin is installable from a fresh clone
#                             via `/plugin marketplace add frane/agented`.
#
# `make verify-plugin-skill`  fails when the staged copy drifts from the
#                             canonical. Wired into the Go test suite via
#                             internal/skill/plugin_sync_test.go, so plain
#                             `go test ./...` catches drift in CI.
stage-plugin:
	@scripts/stage-plugin.sh

verify-plugin-skill:
	@diff -q internal/skill/SKILL.md plugin/skills/agented/SKILL.md \
	  || (echo "plugin/skills/agented/SKILL.md is stale; run make stage-plugin and commit" >&2; exit 1)
