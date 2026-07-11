#!/bin/sh
# octo-setup.pkg — per-user macOS installer for the Octo desktop app.
#
# Installs Octo.app (a self-contained bundle: the desktop GUI + the octo CLI +
# uv, see scripts/package-desktop-macos.sh) into ~/Applications, puts the octo
# CLI on PATH, and opens the app. Per-user (no root/admin) is deliberate and
# mirrors packaging/windows/octo.iss.
#
# This replaces the old "install the CLI + autostart `octo serve -d` + open the
# browser dashboard" flow — the desktop app is that UI now, natively.
#
# Usage:
#   AppVersion=1.6.0 SourceDir=path/to/bits OutDir=path/to/out packaging/macos/build.sh
#
# SourceDir must contain the universal `octo` CLI binary, LICENSE.txt, and
# (release builds) `uv`. The desktop GUI binary is built here from source.
set -eu

: "${AppVersion:?set AppVersion, e.g. 1.6.0}"
: "${SourceDir:?set SourceDir to the folder holding octo + LICENSE.txt}"
: "${OutDir:=$SourceDir}"

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Build the self-contained Octo.app, embedding the released CLI + uv.
uv_arg=""
[ -f "$SourceDir/uv" ] && uv_arg="$SourceDir/uv"
OCTO_CLI="$SourceDir/octo" UV_BINARY="$uv_arg" \
  sh "$repo_root/scripts/package-desktop-macos.sh" "$AppVersion"

# Payload: Octo.app under the user's Applications (currentUserHome domain, so
# --install-location is relative to the home dir).
payload="$work/payload/Applications"
mkdir -p "$payload"
cp -R "$repo_root/Octo.app" "$payload/Octo.app"

pkgbuild \
  --root "$work/payload" \
  --scripts "$script_dir/scripts" \
  --identifier dev.octo-agent.octo \
  --version "$AppVersion" \
  --install-location 'Applications' \
  "$work/octo-component.pkg"

distribution="$work/distribution.xml"
sed "s/{{VERSION}}/$AppVersion/" "$script_dir/distribution.xml" > "$distribution"

mkdir -p "$OutDir"
productbuild \
  --distribution "$distribution" \
  --package-path "$work" \
  --version "$AppVersion" \
  "$OutDir/octo-setup.pkg"

echo "built $OutDir/octo-setup.pkg"
