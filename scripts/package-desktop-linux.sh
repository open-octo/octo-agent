#!/usr/bin/env bash
# Package the octo desktop shell into a portable Linux AppImage. Runs on Linux
# only (CI: the Desktop workflow's ubuntu job). The web UI is embedded in the
# in-process server, so this bundles just the binary + AppRun launcher + desktop
# entry + icon. GTK4/WebKitGTK 6.0 are NOT bundled — taken from the host; the
# AppRun preflight (build/linux/AppRun) guides the user if they're absent.
#
# Requires the GTK4/WebKitGTK dev packages (to compile) and downloads
# appimagetool. Usage: scripts/package-desktop-linux.sh [version]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD_DIR="$ROOT/cmd/octo-desktop"
LINUX="$MOD_DIR/build/linux"
APPDIR="$ROOT/Octo.AppDir"
OUT="$ROOT/Octo-x86_64.AppImage"
VERSION="${1:-$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)}"
VERSION="${VERSION#v}"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"

# Bundle ripgrep so the desktop app's grep tool has an `rg` to shell out to
# (the app can't rely on one being on the user's PATH). Mirrors `make build`:
# download rg for the host platform, then build with -tags=embedrg to go:embed
# it. Without this the grep tool fails at runtime with "no binary was embedded".
echo "==> embedding ripgrep"
make -C "$ROOT" rg-embed

echo "==> building octo-desktop binary"
# Inject Version/Commit so the in-app update check recognizes a real release
# build — without them internal/upgrade.Eligible sees an empty Commit and the
# app reports "up to date" forever.
LDFLAGS="-X github.com/open-octo/octo-agent/internal/version.Version=$VERSION -X github.com/open-octo/octo-agent/internal/version.Commit=$COMMIT"
( cd "$MOD_DIR" && CGO_ENABLED=1 go build -tags embedrg -ldflags "$LDFLAGS" -o "$ROOT/octo-desktop" . )

echo "==> assembling AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin" "$APPDIR/usr/share/icons/hicolor/256x256/apps" "$APPDIR/usr/share/applications"
mv "$ROOT/octo-desktop" "$APPDIR/usr/bin/octo-desktop"
install -m 0755 "$LINUX/AppRun" "$APPDIR/AppRun"
cp "$LINUX/octo-desktop.desktop" "$APPDIR/octo-desktop.desktop"
cp "$LINUX/octo-desktop.desktop" "$APPDIR/usr/share/applications/octo-desktop.desktop"
cp "$LINUX/icon.png" "$APPDIR/octo-desktop.png"
cp "$LINUX/icon.png" "$APPDIR/usr/share/icons/hicolor/256x256/apps/octo-desktop.png"

# Bundle the octo CLI so the app seeds it to ~/.local/bin on first launch (via
# $APPDIR/usr/bin by bundledBinaryPath), matching the mac/win installers that
# put the CLI on PATH. Optional — a plain `make` without OCTO_CLI still produces
# a runnable GUI; the release sets OCTO_CLI to the linux amd64 CLI binary.
if [ -n "${OCTO_CLI:-}" ] && [ -f "$OCTO_CLI" ]; then
	install -m 0755 "$OCTO_CLI" "$APPDIR/usr/bin/octo"
	echo "    embedded octo CLI"
fi

# Bundle uv so it's seeded into ~/.octo/bin on first launch (via $APPDIR/usr/bin
# by bundledBinaryPath). Optional. Release sets UV_BINARY.
if [ -n "${UV_BINARY:-}" ] && [ -f "$UV_BINARY" ]; then
	install -m 0755 "$UV_BINARY" "$APPDIR/usr/bin/uv"
	echo "    embedded uv"
fi

echo "==> fetching appimagetool"
TOOL="$ROOT/appimagetool.AppImage"
if [ ! -x "$TOOL" ]; then
	curl -fsSL -o "$TOOL" \
		"https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage"
	chmod +x "$TOOL"
fi

echo "==> building AppImage"
# --appimage-extract-and-run: no FUSE in CI containers.
ARCH=x86_64 "$TOOL" --appimage-extract-and-run "$APPDIR" "$OUT"
echo "==> done: $OUT"
