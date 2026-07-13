#!/usr/bin/env pwsh
# Download ripgrep for the target Windows arch and place it at the go:embed path
# so a desktop build with -tags=embedrg bundles a working `rg` (the grep tool
# shells out to it). This mirrors the Makefile's rg-embed target for the one
# platform that can't use it: windows-latest runners aren't guaranteed to have
# GNU Make, and the release pipeline avoids adding a `choco install make`
# dependency just for a curl+unzip (same reason the uv fetch is inlined in
# pwsh). Keep $rgVersion in sync with RG_VERSION in the Makefile.
#
# -Arch is the GO arch of the desktop binary being built (amd64 | arm64), NOT
# the runner's arch: the windows/arm64 desktop build cross-compiles on an
# amd64 runner, so the embedded rg must match the target, not the host.
param(
	[ValidateSet('amd64', 'arm64')]
	[string]$Arch = 'amd64'
)
$ErrorActionPreference = 'Stop'
$rgVersion = '15.1.0'
$embedDir = 'internal/tools/rgembed/binaries'
$rgArch = if ($Arch -eq 'arm64') { 'aarch64' } else { 'x86_64' }
$asset = "ripgrep-$rgVersion-$rgArch-pc-windows-msvc.zip"
$url = "https://github.com/BurntSushi/ripgrep/releases/download/$rgVersion/$asset"

New-Item -ItemType Directory -Force -Path $embedDir, dl\rg | Out-Null
Invoke-WebRequest -Uri $url -OutFile dl\rg.zip
Expand-Archive -Path dl\rg.zip -DestinationPath dl\rg -Force
$rg = Get-ChildItem -Path dl\rg -Recurse -Filter rg.exe | Select-Object -First 1
# go:embed reads a file named "rg" (no extension) on every platform; the runtime
# renames the extracted copy to rg.exe on Windows.
Copy-Item $rg.FullName "$embedDir/rg" -Force
Write-Host "Embedded rg $rgVersion for windows/$Arch"
