#!/usr/bin/env pwsh
# Download ripgrep for windows/amd64 and place it at the go:embed path so a
# desktop build with -tags=embedrg bundles a working `rg` (the grep tool shells
# out to it). This mirrors the Makefile's rg-embed target for the one platform
# that can't use it: windows-latest runners aren't guaranteed to have GNU Make,
# and the release pipeline avoids adding a `choco install make` dependency just
# for a curl+unzip (same reason the uv fetch is inlined in pwsh). Keep
# $rgVersion in sync with RG_VERSION in the Makefile.
$ErrorActionPreference = 'Stop'
$rgVersion = '15.1.0'
$embedDir = 'internal/tools/rgembed/binaries'
$asset = "ripgrep-$rgVersion-x86_64-pc-windows-msvc.zip"
$url = "https://github.com/BurntSushi/ripgrep/releases/download/$rgVersion/$asset"

New-Item -ItemType Directory -Force -Path $embedDir, dl\rg | Out-Null
Invoke-WebRequest -Uri $url -OutFile dl\rg.zip
Expand-Archive -Path dl\rg.zip -DestinationPath dl\rg -Force
$rg = Get-ChildItem -Path dl\rg -Recurse -Filter rg.exe | Select-Object -First 1
# go:embed reads a file named "rg" (no extension) on every platform; the runtime
# renames the extracted copy to rg.exe on Windows.
Copy-Item $rg.FullName "$embedDir/rg" -Force
Write-Host "Embedded rg $rgVersion for windows/amd64"
