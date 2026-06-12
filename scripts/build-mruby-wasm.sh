#!/usr/bin/env bash
# Regenerate internal/workflow/mruby.wasm — the embedded mruby interpreter the
# Ruby workflow runtime runs in wazero. This is NOT part of `make build` or CI:
# the .wasm is committed, and only needs regenerating when a host primitive is
# added/removed (runtime.c) or mruby is upgraded. Contributors do not need
# wasi-sdk for a normal build.
#
# Requires: a C toolchain capable of nothing special — this script downloads a
# pinned wasi-sdk (~173 MB) and mruby into a scratch dir, builds, and copies the
# resulting .wasm into the repo.
#
# Usage: scripts/build-mruby-wasm.sh
set -euo pipefail

WASI_SDK_VERSION=33
MRUBY_VERSION=3.4.0
SCRATCH="${MRUBY_WASM_SCRATCH:-$HOME/.cache/octo-mruby-wasm}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$REPO_ROOT/internal/workflow/mruby.wasm"
SRC="$REPO_ROOT/internal/workflow/mruby"

uname_s=$(uname -s); uname_m=$(uname -m)
case "$uname_s-$uname_m" in
  Darwin-arm64)  SDK_ASSET="wasi-sdk-${WASI_SDK_VERSION}.0-arm64-macos" ;;
  Darwin-x86_64) SDK_ASSET="wasi-sdk-${WASI_SDK_VERSION}.0-x86_64-macos" ;;
  Linux-x86_64)  SDK_ASSET="wasi-sdk-${WASI_SDK_VERSION}.0-x86_64-linux" ;;
  Linux-aarch64) SDK_ASSET="wasi-sdk-${WASI_SDK_VERSION}.0-arm64-linux" ;;
  *) echo "unsupported host $uname_s-$uname_m for building mruby.wasm" >&2; exit 1 ;;
esac

mkdir -p "$SCRATCH"; cd "$SCRATCH"
export WASI_SDK="$SCRATCH/wasi-sdk"

if [ ! -d "$WASI_SDK" ]; then
  echo "==> downloading $SDK_ASSET"
  curl -sL "https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-${WASI_SDK_VERSION}/${SDK_ASSET}.tar.gz" -o wasi-sdk.tar.gz
  tar xzf wasi-sdk.tar.gz
  mv "$SDK_ASSET" wasi-sdk
fi

if [ ! -d mruby ]; then
  echo "==> cloning mruby $MRUBY_VERSION"
  git clone --depth 1 --branch "$MRUBY_VERSION" https://github.com/mruby/mruby mruby
fi

echo "==> building libmruby.a (wasm32-wasi)"
cd mruby
MRUBY_CONFIG="$SRC/build_config.rb" ./minirake >/dev/null

echo "==> linking mruby.wasm"
"$WASI_SDK/bin/clang" \
  --target=wasm32-wasip1 --sysroot="$WASI_SDK/share/wasi-sysroot" \
  -mllvm -wasm-enable-sjlj -mllvm -wasm-use-legacy-eh=false -O2 \
  -Wl,--export=malloc -Wl,--export=free \
  -I include \
  "$SRC/runtime.c" build/wasi/lib/libmruby.a \
  -lsetjmp -lwasi-emulated-signal -lwasi-emulated-process-clocks \
  -o "$OUT"

# minirake writes a lock next to MRUBY_CONFIG; it's a build artifact, not source.
rm -f "$SRC/build_config.rb.lock"

echo "==> wrote $OUT ($(du -h "$OUT" | cut -f1))"
