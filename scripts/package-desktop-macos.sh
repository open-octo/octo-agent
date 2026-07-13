#!/usr/bin/env bash
# Package the octo desktop shell into a macOS .app bundle around the Go-built
# binary. The web UI is embedded in the in-process server (go:embed webdist),
# so this only assembles the bundle — run `make web-build` first (the
# `desktop-app` Makefile target does). Needs the macOS toolchain (Xcode
# command-line tools). A .app (with a bundle identifier) is also what native
# notifications require at runtime.
#
# Usage: scripts/package-desktop-macos.sh [version]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD_DIR="$ROOT/cmd/octo-desktop"
VERSION="${1:-$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)}"
VERSION="${VERSION#v}"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
APP="$ROOT/Octo.app"
CONTENTS="$APP/Contents"

# Build a universal (x86_64 + arm64) octo-desktop so Octo.app runs natively on
# both Intel and Apple Silicon. Each arch is compiled separately and lipo'd
# together — Wails links WKWebView via CGO, and the macOS SDK ships both arch
# slices, so CC="clang -arch <arch>" cross-compiles the C/ObjC side while GOARCH
# handles the Go side.
#
# ripgrep is go:embed'd per arch (the grep tool shells out to it): each slice
# must embed its OWN arch's rg, or grep breaks on the other arch. rg-embed is a
# file target keyed on GOARCH, so wipe the embedded binary between arches to
# force a re-download for the arch being built.
RG_EMBED="$ROOT/internal/tools/rgembed/binaries/rg"
# Inject Version/Commit so the in-app update check recognizes a real release
# build — without them internal/upgrade.Eligible sees an empty Commit and the
# app reports "up to date" forever.
LDFLAGS="-X github.com/open-octo/octo-agent/internal/version.Version=$VERSION -X github.com/open-octo/octo-agent/internal/version.Commit=$COMMIT"
slices=()
for arch in amd64 arm64; do
	case "$arch" in
		amd64) cc_arch=x86_64 ;;
		arm64) cc_arch=arm64 ;;
	esac
	echo "==> embedding ripgrep (darwin/$arch)"
	rm -f "$RG_EMBED"
	make -C "$ROOT" rg-embed GOOS=darwin GOARCH="$arch"

	echo "==> building octo-desktop (darwin/$arch)"
	out="$ROOT/octo-desktop-$arch"
	( cd "$MOD_DIR" && \
		GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 CC="clang -arch $cc_arch" \
		go build -tags embedrg -ldflags "$LDFLAGS" -o "$out" . )
	slices+=("$out")
done

echo "==> lipo -> universal octo-desktop"
lipo -create -output "$ROOT/octo-desktop" "${slices[@]}"
rm -f "${slices[@]}"

echo "==> assembling $APP (version $VERSION)"
rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources"
mv "$ROOT/octo-desktop" "$CONTENTS/MacOS/octo-desktop"
sed "s/__VERSION__/$VERSION/g" "$MOD_DIR/build/darwin/Info.plist" > "$CONTENTS/Info.plist"

# Make the bundle self-contained: embed the octo CLI (put on PATH by the
# installer) and uv (seeded into ~/.octo/bin on first launch by the app). Both
# optional — a plain `make desktop-app` without them still produces a runnable
# GUI. The release/installer sets OCTO_CLI and UV_BINARY.
if [ -n "${OCTO_CLI:-}" ] && [ -f "$OCTO_CLI" ]; then
	install -m 0755 "$OCTO_CLI" "$CONTENTS/Resources/octo"
	echo "    embedded octo CLI"
fi
if [ -n "${UV_BINARY:-}" ] && [ -f "$UV_BINARY" ]; then
	install -m 0755 "$UV_BINARY" "$CONTENTS/Resources/uv"
	echo "    embedded uv"
fi
if [ -f "$MOD_DIR/build/darwin/icon.icns" ]; then
	cp "$MOD_DIR/build/darwin/icon.icns" "$CONTENTS/Resources/icon.icns"
else
	echo "    (no build/darwin/icon.icns — bundle uses the default icon)"
fi

# Ad-hoc sign so the local bundle runs without a Developer ID. A real notarized
# signature is a release step, tracked with the .pkg installer effort.
codesign --force --deep --sign - "$APP" >/dev/null 2>&1 || \
	echo "    (codesign --sign - failed; the bundle still runs locally)"

echo "==> done: $APP"
