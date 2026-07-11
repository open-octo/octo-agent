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

echo "==> building octo-desktop binary"
( cd "$MOD_DIR" && CGO_ENABLED=1 go build -o "$ROOT/octo-desktop" . )

echo "==> assembling $APP (version $VERSION)"
rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources"
mv "$ROOT/octo-desktop" "$CONTENTS/MacOS/octo-desktop"
sed "s/__VERSION__/$VERSION/g" "$MOD_DIR/build/darwin/Info.plist" > "$CONTENTS/Info.plist"
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
