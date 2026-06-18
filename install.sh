#!/bin/sh
# agentdash installer: fetches the latest release binary, verifies its
# checksum, installs to ~/.local/bin (override with BIN_DIR; pin a
# version with VERSION=v2.0.0).
set -eu
REPO=yrstm/agentdash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "agentdash: unsupported arch: $ARCH" >&2; exit 1 ;;
esac
[ "$OS" = linux ] || { echo "agentdash: prebuilt binaries are linux-only (use brew or go install)" >&2; exit 1; }
TAG=${VERSION:+download/$VERSION}; TAG=${TAG:-latest/download}
BASE="https://github.com/$REPO/releases/$TAG"
BIN_DIR=${BIN_DIR:-$HOME/.local/bin}; mkdir -p "$BIN_DIR"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
curl -fsSL "$BASE/agentdash-$OS-$ARCH" -o "$TMP/agentdash"
curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt"
( cd "$TMP" && grep " agentdash-$OS-$ARCH\$" checksums.txt \
    | sed "s/agentdash-$OS-$ARCH/agentdash/" | sha256sum -c - >/dev/null )
install -m 0755 "$TMP/agentdash" "$BIN_DIR/agentdash"
echo "agentdash installed to $BIN_DIR/agentdash"
case ":$PATH:" in *":$BIN_DIR:"*) ;; *) echo "note: add $BIN_DIR to your PATH" ;; esac
