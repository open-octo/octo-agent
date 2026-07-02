#!/bin/sh
# octo-setup.pkg — per-user macOS installer for octo.
#
# Installs octo to ~/Library/Application Support/octo/bin, adds that
# directory to the user's PATH (shell rc files — no root, no admin password),
# registers a login LaunchAgent, and opens the onboarding dashboard. Per-user
# is deliberate, matching packaging/windows/octo.iss: the install directory
# stays user-writable, so `octo upgrade` can overwrite the binary in place.
#
# Usage:
#   AppVersion=1.6.0 SourceDir=path/to/bits OutDir=path/to/out packaging/macos/build.sh
#
# SourceDir must contain the `octo` binary (built universal — see the
# universal_binaries entry in .goreleaser.yaml — so one .pkg runs on both
# Intel and Apple Silicon) and LICENSE.txt.
set -eu

: "${AppVersion:?set AppVersion, e.g. 1.6.0}"
: "${SourceDir:?set SourceDir to the folder holding octo + LICENSE.txt}"
: "${OutDir:=$SourceDir}"

script_dir=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

payload="$work/payload"
mkdir -p "$payload/bin"
cp "$SourceDir/octo" "$payload/bin/octo"
chmod +x "$payload/bin/octo"
cp "$SourceDir/LICENSE.txt" "$payload/LICENSE.txt"

pkgbuild \
  --root "$payload" \
  --scripts "$script_dir/scripts" \
  --identifier dev.octo-agent.octo \
  --version "$AppVersion" \
  --install-location '~/Library/Application Support/octo' \
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
