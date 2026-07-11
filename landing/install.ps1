# octo installer for Windows (the octo CLI).
#
#   irm https://octo-agent.dev/install.ps1 | iex
#
# Detects your arch, downloads the matching release archive, verifies its
# SHA-256 against the release's checksums.txt, installs octo.exe to a per-user
# directory, and adds it to your PATH. No admin/UAC.
#
# Overrides (env):
#   $env:OCTO_INSTALL_DIR = 'C:\path'   install here instead of the default
#   $env:OCTO_VERSION     = '1.2.3'     install this version instead of latest
#
# Prefer a double-click install (and the desktop app)? Get octo-setup.exe from
# https://github.com/open-octo/octo-agent/releases/latest

$ErrorActionPreference = 'Stop'
$repo = 'open-octo/octo-agent'

function Fail($msg) { Write-Error "octo install: $msg"; exit 1 }

# --- detect arch (only windows_amd64 is published today) ---------------------
$arch = 'amd64'
if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -and -not $env:OCTO_INSTALL_DIR) {
  Write-Host "octo install: no native windows/arm64 build yet — installing amd64 (runs under emulation)."
}

# --- resolve version ---------------------------------------------------------
$version = $env:OCTO_VERSION
if (-not $version) {
  try {
    $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $version = ($rel.tag_name -replace '^v', '')
  } catch { Fail "could not determine the latest version" }
}
if (-not $version) { Fail "could not determine the latest version" }

$archive = "octo_${version}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/v$version"

$tmp = Join-Path $env:TEMP ("octo-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  # --- download --------------------------------------------------------------
  Write-Host "octo install: downloading $archive"
  try { Invoke-WebRequest "$base/$archive" -OutFile "$tmp\$archive" }
  catch { Fail "download failed: $base/$archive" }
  try { Invoke-WebRequest "$base/checksums.txt" -OutFile "$tmp\checksums.txt" }
  catch { Fail "could not fetch checksums.txt" }

  # --- verify SHA-256 --------------------------------------------------------
  $line = Get-Content "$tmp\checksums.txt" |
    Where-Object { $_ -match ("\s" + [regex]::Escape($archive) + "$") } | Select-Object -First 1
  if (-not $line) { Fail "no checksum listed for $archive" }
  $want = ($line -split '\s+')[0].ToLower()
  $got  = (Get-FileHash "$tmp\$archive" -Algorithm SHA256).Hash.ToLower()
  if ($got -ne $want) { Fail "checksum mismatch for $archive (expected $want, got $got)" }

  # --- extract ---------------------------------------------------------------
  Expand-Archive -Path "$tmp\$archive" -DestinationPath "$tmp\extract" -Force
  $exe = Get-ChildItem "$tmp\extract" -Recurse -Filter octo.exe | Select-Object -First 1
  if (-not $exe) { Fail "could not find octo.exe in $archive" }

  # --- install ---------------------------------------------------------------
  $dir = $env:OCTO_INSTALL_DIR
  if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\octo' }
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  Copy-Item $exe.FullName (Join-Path $dir 'octo.exe') -Force
  Write-Host "octo install: installed octo $version to $dir\octo.exe"

  # --- PATH (per-user; no admin) ---------------------------------------------
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not $userPath) { $userPath = '' }
  if (($userPath -split ';') -notcontains $dir) {
    $newPath = if ($userPath.TrimEnd(';')) { $userPath.TrimEnd(';') + ';' + $dir } else { $dir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    Write-Host "octo install: added $dir to your user PATH (restart your terminal to pick it up)."
  }
}
finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Next steps — start the server and onboard in your browser:"
Write-Host ""
Write-Host "  octo serve -d                       # run the local server in the background"
Write-Host "  start http://127.0.0.1:8088         # open the dashboard -> pick a provider, paste a key"
Write-Host ""
Write-Host "127.0.0.1 is loopback, so no access key is needed. Stop it later with"
Write-Host "``octo serve --stop``. Or run ``octo`` for the terminal UI, or install the"
Write-Host "desktop app (octo-setup.exe) for a native window."
