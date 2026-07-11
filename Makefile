# Octo — Go build & test orchestration (Go 1.22+)
#
# Usage:
#   make            same as `make test`
#   make build      build the octo binary at repo root (./octo)
#   make install    install to $GOPATH/bin
#   make test       go test -race ./...
#   make cover      generate coverage.out + coverage.html
#   make vet        go vet ./...
#   make fmt        gofmt -w on all .go files
#   make fmt-check  fail if anything would be reformatted
#   make tidy       go mod tidy
#   make clean      remove build artefacts
#
# octo-eval — lightweight eval (manual; needs a model key, NOT in CI):
#   make eval-build           build the ./octo-eval tool
#   make eval-list            list the task suite
#   make eval EVAL_FLAGS=...  run the suite (pass --provider/--model/… via EVAL_FLAGS)
# See dev-docs/octo-eval.md.

GOTAGS ?=
GOFLAGS ?=

# Build tag that enables embedding the ripgrep binary. CI builds without
# this tag so go:embed does not require binaries/rg to be present.
RG_TAGS := embedrg

# Inject version + commit at build time so `octo version` reports a real SHA.
#
# Auto-detection via `git describe` is intentionally avoided because the repo
# still carries Ruby-era tags (v0.11.2, v0.11.2-final-ruby) that would pollute
# the reported version. The single source of truth for the version number is
# internal/version/version.go (what release bumps already edit); dev builds
# derive "<that>-dev" from it, so the two never drift. Release builds set
# VERSION explicitly:
#
#   VERSION=0.12.0 make build
#
BASE_VERSION := $(shell sed -n 's/^var Version = "\(.*\)"/\1/p' internal/version/version.go)
VERSION ?= $(BASE_VERSION)-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/open-octo/octo-agent/internal/version.Version=$(VERSION) \
           -X github.com/open-octo/octo-agent/internal/version.Commit=$(COMMIT) \
           -X github.com/open-octo/octo-agent/internal/tools/rgembed.version=$(RG_VERSION)

GOFILES := $(shell find . -name '*.go' -not -path './vendor/*' -not -path '*/_vendor/*')

RG_EMBED_DIR := internal/tools/rgembed/binaries
RG_EMBED_BIN := $(RG_EMBED_DIR)/rg

.PHONY: all build install test cover vet fmt fmt-check tidy clean \
        eval-build eval-list eval \
        rg-embed rg-embed-clean \
        bundle-tools-windows bundle-tools-macos \
        web-build web-dev dev build-full desktop desktop-app

all: test

# Build the Vite + Svelte web UI into internal/server/webdist/.
# Requires Node.js and npm installed.
web-build:
	cd web && npm install && npm run build

# Start both the Go server and Vite dev server simultaneously.
# Ctrl-C stops both. Access the hot-reload UI at http://localhost:5173.
dev:
	@cd web && npm install --prefer-offline --silent
	@trap 'kill 0' INT; \
	  go run ./cmd/octo serve & \
	  cd web && npm run dev & \
	  wait

# Start only the Vite dev server (assumes `octo serve` is running separately).
web-dev:
	cd web && npm run dev

# Download and embed ripgrep for the current GOOS/GOARCH before building.
build: web-build rg-embed
	go build $(GOFLAGS) -tags='$(GOTAGS) $(RG_TAGS)' -ldflags='$(LDFLAGS)' -o octo ./cmd/octo

build-full: build

# Build the native desktop shell (Wails v3). cmd/octo-desktop is a nested
# module, so its CGO + webview dependencies never touch the CLI's go.mod. Needs
# a platform webview toolchain (macOS: Xcode command-line tools; Windows:
# WebView2). Embeds the web UI first (the in-process server go:embeds webdist).
# Produces a bare binary; use `wails3 build` inside cmd/octo-desktop for a
# packaged .app / installer.
desktop: web-build
	cd cmd/octo-desktop && CGO_ENABLED=1 go build -o ../../octo-desktop .

# Package the desktop shell into a double-clickable macOS Octo.app bundle
# (embeds the web UI, ad-hoc signed for local use). Real Developer ID
# notarization is a release step. Windows packaging rides the Inno Setup
# installer track.
desktop-app: web-build
	bash scripts/package-desktop-macos.sh

install: web-build rg-embed
	go install $(GOFLAGS) -tags='$(GOTAGS) $(RG_TAGS)' -ldflags='$(LDFLAGS)' ./cmd/octo

test:
	go test -race $(GOFLAGS) -tags='$(GOTAGS)' ./...

cover:
	go test $(GOFLAGS) -tags='$(GOTAGS)' -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Open coverage.html in a browser to see line coverage."

vet:
	go vet ./...

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@unformatted=$$(gofmt -l $(GOFILES)); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt found unformatted files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

tidy:
	go mod tidy

clean:
	rm -f octo octo.exe octo-eval coverage.out coverage.html
	rm -rf dist/
	rm -f $(RG_EMBED_BIN) $(RG_EMBED_BIN).exe

# ── ripgrep embed (build-time only) ──────────────────────────────────────────
# Downloads the matching rg release for GOOS/GOARCH, extracts the binary,
# and places it where go:embed will pick it up. No-op if already present.

RG_VERSION := 15.1.0

# Default to the host platform when GOOS/GOARCH are not set explicitly.
_RG_GOOS   := $(or $(GOOS),$(shell go env GOOS))
_RG_GOARCH := $(or $(GOARCH),$(shell go env GOARCH))

rg-embed: $(RG_EMBED_BIN)

$(RG_EMBED_BIN):
	@echo "Downloading ripgrep $(RG_VERSION) for $(_RG_GOOS)/$(_RG_GOARCH)..."
	@mkdir -p $(RG_EMBED_DIR)
	@bash -c ' \
		GOOS="$(_RG_GOOS)"; GOARCH="$(_RG_GOARCH)"; RG_VERSION="$(RG_VERSION)"; \
		case "$${GOOS}_$${GOARCH}" in \
			darwin_amd64)   asset="ripgrep-$${RG_VERSION}-x86_64-apple-darwin.tar.gz" ;; \
			darwin_arm64)   asset="ripgrep-$${RG_VERSION}-aarch64-apple-darwin.tar.gz" ;; \
			linux_amd64)    asset="ripgrep-$${RG_VERSION}-x86_64-unknown-linux-musl.tar.gz" ;; \
			linux_arm64)    asset="ripgrep-$${RG_VERSION}-aarch64-unknown-linux-gnu.tar.gz" ;; \
			windows_amd64)  asset="ripgrep-$${RG_VERSION}-x86_64-pc-windows-msvc.zip" ;; \
			*) echo "Unsupported platform: $${GOOS}/$${GOARCH} — rg embed skipped"; touch '"$(RG_EMBED_BIN)"'; exit 0 ;; \
		esac; \
		url="https://github.com/BurntSushi/ripgrep/releases/download/$${RG_VERSION}/$${asset}"; \
		if [ "$${GOOS}" = "windows" ]; then \
			curl -sL "$$url" -o /tmp/rg-embed.zip; \
			unzip -q -o /tmp/rg-embed.zip -d /tmp/rg-embed; \
			cp /tmp/rg-embed/ripgrep-$${RG_VERSION}-*/rg.exe $(RG_EMBED_BIN); \
			rm -rf /tmp/rg-embed.zip /tmp/rg-embed; \
		else \
			curl -sL "$$url" | tar -xzf - -C /tmp; \
			cp /tmp/ripgrep-$${RG_VERSION}-*/rg $(RG_EMBED_BIN); \
			rm -rf /tmp/ripgrep-$${RG_VERSION}-*; \
		fi; \
		echo "Embedded rg ready for $${GOOS}/$${GOARCH}" \
	'

rg-embed-clean:
	rm -f $(RG_EMBED_BIN) $(RG_EMBED_BIN).exe

# ── bundled tools (installer-only): uv ───────────────────────────────────────
# Fetches upstream release binaries for uv (astral-sh/uv) and stages them
# under dist/bundled-tools/<platform>/ so the Windows/macOS installers
# (packaging/windows/octo.iss, packaging/macos/build.sh) can bundle it — a
# fresh install then has `uv run` working with zero manual download, for the
# office-xlsx skill (issue #1054's P1 priority: the non-technical-user path).
#
# The generic bundled-dir mechanism (bundledBinDir/withBundledBinPath in
# internal/tools/sandbox.go, and the toolchain.go presence probe) is generic
# — it picks up whatever's in ~/.octo/bin, so a future bundle-tools-* target
# needs no code change, only a Makefile/packaging addition.
#
# Deliberately NOT go:embed'd into the octo binary itself: that would bloat
# every build on every platform (CLI-only Linux users included) with a binary
# they'll never use. Instead this target runs once per release. A plain
# `git clone && make build` / `go install` never runs it and never needs
# dist/bundled-tools to exist — that's why it isn't in git and isn't a
# dependency of the `build` target.
#
# release.yml's macos-installer job runs bundle-tools-macos directly (macOS
# runners have `curl`/`unzip`/`lipo` out of the box). The windows-installer
# job does NOT run bundle-tools-windows — windows-latest isn't guaranteed to
# have GNU Make, so that job fetches the same asset with native PowerShell
# instead (see release.yml) and its version pin must be kept in sync with
# UV_VERSION below by hand. bundle-tools-windows is still here for local
# testing (works from any host with curl/unzip, Windows included via
# WSL/Git-Bash) and to keep both platforms documented in one place.
#
# Version is pinned (like RG_VERSION above) rather than tracking "latest", so
# a release build is reproducible; bump deliberately.
UV_VERSION := 0.11.26

BUNDLE_TOOLS_DIR := dist/bundled-tools

# octo.iss only ever produces a windows/amd64 installer (ArchitecturesAllowed
# x64compatible), so only that one asset is needed.
bundle-tools-windows:
	@echo "Fetching uv $(UV_VERSION) for windows/amd64..."
	@mkdir -p $(BUNDLE_TOOLS_DIR)/windows-amd64
	@set -eu; work=$$(mktemp -d); trap 'rm -rf "$$work"' EXIT; \
		curl -sL -o "$$work/uv.zip" "https://github.com/astral-sh/uv/releases/download/$(UV_VERSION)/uv-x86_64-pc-windows-msvc.zip"; \
		unzip -q -o "$$work/uv.zip" -d "$$work/uv"; \
		cp "$$work/uv/uv.exe" $(BUNDLE_TOOLS_DIR)/windows-amd64/uv.exe; \
		chmod +x $(BUNDLE_TOOLS_DIR)/windows-amd64/uv.exe
	@echo "Staged $(BUNDLE_TOOLS_DIR)/windows-amd64/uv.exe"

# octo-setup.pkg ships one universal (amd64+arm64) binary via lipo, same as
# the octo binary itself (universal_binaries in .goreleaser.yaml) — so uv gets
# lipo'd into a universal binary here too, rather than shipping two separate
# per-arch copies in a single pkg.
bundle-tools-macos:
	@echo "Fetching uv $(UV_VERSION) for darwin (universal)..."
	@mkdir -p $(BUNDLE_TOOLS_DIR)/darwin-universal
	@set -eu; work=$$(mktemp -d); trap 'rm -rf "$$work"' EXIT; \
		curl -sL "https://github.com/astral-sh/uv/releases/download/$(UV_VERSION)/uv-aarch64-apple-darwin.tar.gz" | tar -xzf - -C "$$work"; \
		curl -sL "https://github.com/astral-sh/uv/releases/download/$(UV_VERSION)/uv-x86_64-apple-darwin.tar.gz" | tar -xzf - -C "$$work"; \
		lipo -create -output $(BUNDLE_TOOLS_DIR)/darwin-universal/uv \
			"$$work/uv-aarch64-apple-darwin/uv" "$$work/uv-x86_64-apple-darwin/uv"; \
		chmod +x $(BUNDLE_TOOLS_DIR)/darwin-universal/uv
	@echo "Staged $(BUNDLE_TOOLS_DIR)/darwin-universal/uv"

# ── octo-eval — lightweight eval (manual; see dev-docs/octo-eval.md) ─────────
# Each task under evals/tasks/ is a local fixture; no Docker, no clone. Calls a
# real model, so never run in CI. Pass provider/model/etc through EVAL_FLAGS,
# e.g. make eval EVAL_FLAGS='--provider openai --model deepseek-v4-flash --allow-net'.
EVAL_FLAGS ?=

eval-build:
	go build $(GOFLAGS) -o octo-eval ./cmd/octo-eval

eval-list: eval-build
	./octo-eval list

eval: eval-build build
	./octo-eval run --octo ./octo $(EVAL_FLAGS)
