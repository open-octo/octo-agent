#!/usr/bin/env bash
# Package the octo desktop shell into a portable Linux AppImage. Runs on Linux
# only (CI: the Desktop workflow's ubuntu job). The web UI is embedded in the
# in-process server, so this bundles just the binary + AppRun launcher + desktop
# entry + icon. GTK4/WebKitGTK 6.0 are NOT bundled — taken from the host; the
# AppRun preflight (build/linux/AppRun) guides the user if they're absent.
#
# Requires the GTK4/WebKitGTK dev packages (to compile) and downloads
# appimagetool. Usage: scripts/package-desktop-linux.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD_DIR="$ROOT/cmd/octo-desktop"
LINUX="$MOD_DIR/build/linux"
APPDIR="$ROOT/Octo.AppDir"
OUT="$ROOT/Octo-x86_64.AppImage"

echo "==> building octo-desktop binary"
( cd "$MOD_DIR" && CGO_ENABLED=1 go build -o "$ROOT/octo-desktop" . )

echo "==> assembling AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin" "$APPDIR/usr/share/icons/hicolor/256x256/apps" "$APPDIR/usr/share/applications"
mv "$ROOT/octo-desktop" "$APPDIR/usr/bin/octo-desktop"
install -m 0755 "$LINUX/AppRun" "$APPDIR/AppRun"
cp "$LINUX/octo-desktop.desktop" "$APPDIR/octo-desktop.desktop"
cp "$LINUX/octo-desktop.desktop" "$APPDIR/usr/share/applications/octo-desktop.desktop"
cp "$LINUX/icon.png" "$APPDIR/octo-desktop.png"
cp "$LINUX/icon.png" "$APPDIR/usr/share/icons/hicolor/256x256/apps/octo-desktop.png"

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
