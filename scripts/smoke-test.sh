#!/usr/bin/env bash
#
# Smoke test for a built davi-nfc-agent binary. Runs --version and asserts
# the output looks right for the target GOOS/GOARCH.
#
# Required env: GOOS, GOARCH (matching the matrix entry that produced the
# binary). The binary is expected at ./davi-nfc-agent-<goos>-<goarch>[.exe]
# in the working directory.
#
# For linux/arm64 cross-built on amd64 hosts the script auto-prefixes
# qemu-aarch64-static (must be installed beforehand). darwin/amd64 on
# darwin/arm64 hosts is run natively and relies on Rosetta 2.

set -euo pipefail

: "${GOOS:?GOOS must be set}"
: "${GOARCH:?GOARCH must be set}"

BIN="./davi-nfc-agent-${GOOS}-${GOARCH}"
if [ "$GOOS" = "windows" ]; then
    BIN="${BIN}.exe"
fi

if [ ! -f "$BIN" ]; then
    echo "FAIL: binary not found at $BIN" >&2
    ls -la . >&2
    exit 1
fi

# Pick a runner for cross-compiled binaries we can't execute directly.
RUNNER=()
HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
    x86_64) HOST_ARCH=amd64 ;;
    aarch64|arm64) HOST_ARCH=arm64 ;;
esac

if [ "$GOOS" = "linux" ] && [ "$GOARCH" = "arm64" ] && [ "$HOST_ARCH" != "arm64" ]; then
    if ! command -v qemu-aarch64-static >/dev/null; then
        echo "FAIL: qemu-aarch64-static not installed; cannot smoke-test cross binary" >&2
        exit 1
    fi
    RUNNER=(qemu-aarch64-static)
fi

echo "Host: ${HOST_OS}/${HOST_ARCH}"
echo "Target: ${GOOS}/${GOARCH}"
echo "Running: ${RUNNER[*]:-} $BIN --version"

# ${RUNNER[@]+"${RUNNER[@]}"} expands to nothing when the array is unset/empty
# (bash 3.2-compatible — macOS ships 3.2 and `set -u` rejects ${arr[@]} alone).
OUT=$(${RUNNER[@]+"${RUNNER[@]}"} "$BIN" --version)
echo "----- output -----"
echo "$OUT"
echo "------------------"

if ! grep -q '^davi-nfc-agent' <<<"$OUT"; then
    echo "FAIL: first line should start with 'davi-nfc-agent'" >&2
    exit 1
fi

EXPECTED="OS/Arch: ${GOOS}/${GOARCH}"
if ! grep -qF "$EXPECTED" <<<"$OUT"; then
    echo "FAIL: expected '$EXPECTED' in output" >&2
    exit 1
fi

echo "Smoke test passed for ${GOOS}/${GOARCH}."
