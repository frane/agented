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

.PHONY: build test test-short lint release install clean fmt staticcheck

build:
	$(GO_ENV) go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	$(GO_ENV) go test ./... -race -cover

test-short:
	$(GO_ENV) go test ./... -short -race

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
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "installed to $(INSTALL_DIR)/$(BINARY)"

clean:
	rm -f $(BINARY)
	rm -rf dist
