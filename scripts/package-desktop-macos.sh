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
APP="$ROOT/Octo.app"
CONTENTS="$APP/Contents"

# Bundle ripgrep so the desktop app's grep tool has an `rg` to shell out to
# (the app can't rely on one being on the user's PATH). Mirrors `make build`:
# download rg for the host platform, then build with -tags=embedrg to go:embed
# it. Without this the grep tool fails at runtime with "no binary was embedded".
echo "==> embedding ripgrep"
make -C "$ROOT" rg-embed

echo "==> building octo-desktop binary"
( cd "$MOD_DIR" && CGO_ENABLED=1 go build -tags embedrg -o "$ROOT/octo-desktop" . )

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
