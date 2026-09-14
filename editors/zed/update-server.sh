#!/usr/bin/env bash
# Install or update the shopware-lsp binary into ~/.local/bin.
#
# Reuse the platform server bundled in the published Open VSX extension.
set -euo pipefail

DEST="${DEST:-$HOME/.local/bin}"

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64) TARGET=darwin-arm64 ;;
  Darwin-x86_64) TARGET=darwin-x64 ;;
  Linux-aarch64) TARGET=linux-arm64 ;;
  Linux-x86_64) TARGET=linux-x64 ;;
  *) echo "unsupported platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

API="https://open-vsx.org/api/shopware/shopware-lsp/$TARGET/latest"
VERSION="$(curl -fsSL "$API" | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])')"
URL="$(curl -fsSL "$API" | python3 -c 'import json,sys; print(json.load(sys.stdin)["files"]["download"])')"

echo "shopware-lsp $VERSION ($TARGET)"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -fsSL -o "$TMP/ext.vsix" "$URL"
unzip -q -o "$TMP/ext.vsix" -d "$TMP/ext"

BIN="$(find "$TMP/ext" -name shopware-lsp -type f -perm -u+x -print -quit)"
[ -n "$BIN" ] || BIN="$(find "$TMP/ext" -name shopware-lsp -type f -print -quit)"
[ -n "$BIN" ] || { echo "no shopware-lsp binary inside the vsix" >&2; exit 1; }

mkdir -p "$DEST"
install -m 755 "$BIN" "$DEST/shopware-lsp"
echo "installed -> $DEST/shopware-lsp"
"$DEST/shopware-lsp" version
