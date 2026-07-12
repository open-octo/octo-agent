#!/bin/sh
# octo installer for macOS and Linux.
#
#   curl -fsSL https://octo-agent.dev/install.sh | sh
#
# Detects your OS/arch, downloads the matching release archive, verifies its
# SHA-256 against the release's checksums.txt, and installs the `octo` binary
# to a directory on your PATH (default /usr/local/bin, else ~/.local/bin).
#
# Overrides (env):
#   OCTO_INSTALL_DIR=/path   install here instead of the default
#   OCTO_VERSION=1.2.3       install this version instead of the latest
#
# Windows users: run the PowerShell one-liner instead —
#   irm https://octo-agent.dev/install.ps1 | iex
# or download the double-click installer (octo-setup.exe, which also gives you
# the desktop app) from https://github.com/open-octo/octo-agent/releases/latest

set -eu

REPO="open-octo/octo-agent"

err() { printf 'octo install: %s\n' "$1" >&2; exit 1; }

# --- need curl and tar -------------------------------------------------------
command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar  >/dev/null 2>&1 || err "tar is required"

# --- detect OS ---------------------------------------------------------------
os=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux)  os=linux ;;
  darwin) os=darwin ;;
  *) err "unsupported OS '$os' — see https://github.com/$REPO/releases" ;;
esac

# --- detect arch -------------------------------------------------------------
arch=$(uname -m 2>/dev/null)
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) err "unsupported architecture '$arch' — see https://github.com/$REPO/releases" ;;
esac

# --- resolve version ---------------------------------------------------------
version="${OCTO_VERSION:-}"
if [ -z "$version" ]; then
  # Read the latest tag from the GitHub API and strip the leading "v" so it
  # matches the goreleaser archive name ({{.Version}}, e.g. 1.0.0).
  version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -n1 | sed -E 's/.*"tag_name": *"v?([^"]+)".*/\1/')
fi
[ -n "$version" ] || err "could not determine the latest version"

archive="octo_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/v${version}"

# --- download into a temp dir ------------------------------------------------
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t octo)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'octo install: downloading %s\n' "$archive"
curl -fSL "$base/$archive"     -o "$tmp/$archive"     || err "download failed: $base/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || err "could not fetch checksums.txt"

# --- verify SHA-256 ----------------------------------------------------------
want=$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}' | head -n1)
[ -n "$want" ] || err "no checksum listed for $archive"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
  err "need sha256sum or shasum to verify the download"
fi
[ "$got" = "$want" ] || err "checksum mismatch for $archive (expected $want, got $got)"

# --- extract -----------------------------------------------------------------
tar -xzf "$tmp/$archive" -C "$tmp" octo || err "could not extract octo from $archive"
chmod +x "$tmp/octo"

# --- choose install dir ------------------------------------------------------
dir="${OCTO_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    dir=/usr/local/bin
  else
    dir="$HOME/.local/bin"
  fi
fi
mkdir -p "$dir" || err "cannot create install dir $dir"

# --- install -----------------------------------------------------------------
if mv "$tmp/octo" "$dir/octo" 2>/dev/null; then
  :
elif command -v sudo >/dev/null 2>&1; then
  printf 'octo install: %s is not writable; using sudo\n' "$dir"
  sudo mv "$tmp/octo" "$dir/octo" || err "could not install to $dir"
else
  err "cannot write to $dir (set OCTO_INSTALL_DIR to a writable directory)"
fi

printf 'octo install: installed octo %s to %s/octo\n' "$version" "$dir"

# --- PATH setup ---------------------------------------------------------------
case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    marker="# added by octo installer"

    # Detect the user's shell and pick the right rc file
    shell_name=$(basename "${SHELL:-}" 2>/dev/null || echo "")
    case "$shell_name" in
      zsh)
        # .zshrc is read by every interactive zsh — login or not. macOS Terminal
        # opens a login shell (which also reads .zshrc), while VS Code's terminal
        # and other non-login shells read only .zshrc, never .zprofile. So .zshrc
        # is the reliable target on both platforms; picking .zprofile on macOS
        # would leave octo off PATH in non-login shells.
        rc="$HOME/.zshrc"
        ;;
      bash)
        # macOS bash as login → .bash_profile; Linux → .bashrc
        if [ "$os" = "darwin" ]; then
          rc="$HOME/.bash_profile"
        else
          rc="$HOME/.bashrc"
        fi
        ;;
      *)
        rc="$HOME/.profile"
        ;;
    esac

    # Write to the primary rc file (idempotent — marker prevents duplication)
    [ -f "$rc" ] || touch "$rc"
    if ! grep -qF "$marker" "$rc" 2>/dev/null; then
      printf '\nexport PATH="%s:$PATH"  %s\n' "$dir" "$marker" >> "$rc"
      printf 'octo install: added %s to %s\n' "$dir" "$rc"
    fi

    # Also add to ~/.profile as a universal fallback (skip if same file already written)
    if [ "$rc" != "$HOME/.profile" ]; then
      [ -f "$HOME/.profile" ] || touch "$HOME/.profile"
      if ! grep -qF "$marker" "$HOME/.profile" 2>/dev/null; then
        printf '\nexport PATH="%s:$PATH"  %s\n' "$dir" "$marker" >> "$HOME/.profile"
      fi
    fi

    printf 'octo install: restart your shell or run:\n  export PATH="%s:$PATH"\n' "$dir"
    ;;
esac

# --- next steps --------------------------------------------------------------
opencmd="open"
[ "$os" = "linux" ] && opencmd="xdg-open"
cat <<EOF

Next steps — start the server and onboard in your browser:

  octo serve -d                    # run the local server in the background
  $opencmd http://127.0.0.1:8088   # open the dashboard → pick a provider, paste a key

127.0.0.1 is loopback, so no access key is needed. Stop it later with
\`octo serve --stop\`. Or skip the web UI and run \`octo\` for the terminal.
EOF
