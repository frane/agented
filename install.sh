#!/bin/sh
# agented installer
# Downloads the latest release binary from GitHub and installs it.
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/frane/agented/master/install.sh | sh
#
# Environment variables:
#   AE_INSTALL_DIR  - directory to install to (default: $HOME/.local/bin or /usr/local/bin)
#   AE_VERSION      - version to install (default: latest)

set -e

REPO="frane/agented"
BINARY="ae"

# Detect platform
OS=""
ARCH=""

case "$(uname -s)" in
    Linux*)  OS="linux" ;;
    Darwin*) OS="darwin" ;;
    *)
        echo "unsupported os: $(uname -s)" >&2
        echo "for windows or other platforms, download from https://github.com/$REPO/releases" >&2
        exit 1
        ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ARCH="x86_64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)
        echo "unsupported arch: $(uname -m)" >&2
        exit 1
        ;;
esac

# Determine version
VERSION="${AE_VERSION:-}"
if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | \
        grep '"tag_name"' | head -n 1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
fi
if [ -z "$VERSION" ]; then
    echo "failed to determine latest version; pass AE_VERSION explicitly" >&2
    exit 1
fi

# Determine install directory
INSTALL_DIR="${AE_INSTALL_DIR:-}"
if [ -z "$INSTALL_DIR" ]; then
    if [ -w "/usr/local/bin" ] 2>/dev/null; then
        INSTALL_DIR="/usr/local/bin"
    else
        INSTALL_DIR="$HOME/.local/bin"
        mkdir -p "$INSTALL_DIR"
    fi
fi

# Download and extract
VERSION_NO_V="${VERSION#v}"
ARCHIVE="agented_${VERSION_NO_V}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$ARCHIVE"
CHECKSUMS_URL="https://github.com/$REPO/releases/download/$VERSION/checksums.txt"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "downloading $ARCHIVE"
curl -fsSL "$URL" -o "$TMP/$ARCHIVE"

# Verify checksum
echo "verifying checksum"
curl -fsSL "$CHECKSUMS_URL" -o "$TMP/checksums.txt"
EXPECTED=$(grep "$ARCHIVE" "$TMP/checksums.txt" | awk '{print $1}')
if [ -z "$EXPECTED" ]; then
    echo "checksum not found for $ARCHIVE; aborting" >&2
    exit 1
fi
ACTUAL=$(shasum -a 256 "$TMP/$ARCHIVE" 2>/dev/null | awk '{print $1}' || \
         sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "checksum mismatch; expected $EXPECTED got $ACTUAL" >&2
    exit 1
fi

echo "extracting"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"

# Install
echo "installing to $INSTALL_DIR/$BINARY"
mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

# Verify
if ! command -v "$BINARY" >/dev/null 2>&1; then
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) ;;
        *)
            echo ""
            echo "warning: $INSTALL_DIR is not in your PATH"
            echo "add to your shell config:"
            echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
            ;;
    esac
fi

echo ""
echo "agented $VERSION installed to $INSTALL_DIR/$BINARY"
echo "run 'ae --help' to get started"
