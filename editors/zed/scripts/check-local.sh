#!/usr/bin/env bash
# Check the Zed adapters against this checkout's production server and VS Code manifest.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPOSITORY_ROOT="$(cd "$HERE/../../.." && pwd)"
CHECK_DIR="$(mktemp -d)"
trap 'rm -rf "$CHECK_DIR"' EXIT
export SHOPWARE_LSP_CACHE_DIR="$CHECK_DIR/cache"

mkdir -p "$CHECK_DIR/fixture/.config/shopware"
printf 'version: 1\n' > "$CHECK_DIR/fixture/.config/shopware/lsp.yaml"
# Build the shipped Rust server. CARGO_TARGET_DIR is honoured when set.
TARGET_DIR="${CARGO_TARGET_DIR:-$REPOSITORY_ROOT/target}"
(cd "$REPOSITORY_ROOT" && cargo build --locked -p swlsp-cli --bin shopware-lsp)
cp "$TARGET_DIR/debug/shopware-lsp" "$CHECK_DIR/shopware-lsp"

python3 "$HERE/contract-check.py" --offline --binary "$CHECK_DIR/shopware-lsp"
python3 "$HERE/inventory.py" --check \
  --binary "$CHECK_DIR/shopware-lsp" \
  --extension-dir "$REPOSITORY_ROOT/editors/vscode" \
  --root "$CHECK_DIR/fixture"
