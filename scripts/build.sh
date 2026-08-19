#!/bin/bash
set -e

# Unified build script for all platforms
# Usage: ./scripts/build.sh [os] [arch]
#   os: linux, darwin, windows (defaults to current OS)
#   arch: amd64, arm64 (defaults to current arch)

# Parse arguments or use defaults
TARGET_OS="${1:-$(go env GOOS)}"
TARGET_ARCH="${2:-$(go env GOARCH)}"

# Build info (can be overridden via environment variables)
BUILD_VERSION="${BUILD_VERSION:-dev}"
BUILD_COMMIT="${BUILD_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

# Generate ldflags for buildinfo package
PKG="github.com/dotside-studios/davi-nfc-agent/buildinfo"
LDFLAGS="-X $PKG.Version=$BUILD_VERSION -X $PKG.Commit=$BUILD_COMMIT -X $PKG.BuildTime=$BUILD_TIME"

# Determine binary name
if [ "$TARGET_OS" = "windows" ]; then
    BINARY_NAME="davi-nfc-agent-$TARGET_OS-$TARGET_ARCH.exe"
else
    BINARY_NAME="davi-nfc-agent-$TARGET_OS-$TARGET_ARCH"
fi

echo "=== Building $BINARY_NAME ==="
echo "  Version: $BUILD_VERSION"
echo "  Commit: $BUILD_COMMIT"
echo "  Build Time: $BUILD_TIME"

# PC/SC is reached without cgo (see docs/pure-go-pcsc.md), so only macOS still
# needs a C toolchain: fyne.io/systray talks to Cocoa there.
if [ "$TARGET_OS" = "darwin" ]; then
    export CGO_ENABLED=1
else
    export CGO_ENABLED=0
fi
echo "  CGO_ENABLED: $CGO_ENABLED"

# Determine if we need cross-compilation setup
CURRENT_OS=$(go env GOOS)
CURRENT_ARCH=$(go env GOARCH)

if [ "$TARGET_OS" != "$CURRENT_OS" ] || [ "$TARGET_ARCH" != "$CURRENT_ARCH" ]; then
    echo "  Cross-compiling from $CURRENT_OS/$CURRENT_ARCH to $TARGET_OS/$TARGET_ARCH"

    # macOS can cross-compile between arm64/amd64 with native clang
    if [ "$TARGET_OS" = "darwin" ] && [ "$CURRENT_OS" = "darwin" ]; then
        echo "  Using native clang for macOS cross-compilation"
        if [ "$TARGET_ARCH" = "arm64" ]; then
            export CC="clang -arch arm64"
            export CXX="clang++ -arch arm64"
        elif [ "$TARGET_ARCH" = "amd64" ]; then
            export CC="clang -arch x86_64"
            export CXX="clang++ -arch x86_64"
        fi
    fi

    # Log the compiler being used
    if [ -n "$CC" ]; then
        echo "  Using CC: $CC"
    fi
fi

GOOS=$TARGET_OS GOARCH=$TARGET_ARCH go build -ldflags="$LDFLAGS" -o "$BINARY_NAME" .

echo "✓ Built: $BINARY_NAME"
ls -lh "$BINARY_NAME"
