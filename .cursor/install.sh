#!/usr/bin/env bash
# Idempotent repository bootstrap for the Shopware LSP Cloud Agent environment.
# Runs after the repository is checked out. Safe to run repeatedly.
set -euo pipefail

export PATH="$HOME/.local/share/mise/shims:$HOME/.local/bin:$HOME/.cargo/bin:$PATH"
export MISE_YES=1

# Install the toolchains pinned in mise.toml (Go, Node.js, golangci-lint, vsce)
# and regenerate shims so they resolve on PATH.
mise trust
mise install
mise reshim

# Refresh project dependencies through the repository's own setup task:
# `go mod download` plus `npm ci` for the VS Code extension.
mise run setup

# Warm the Zed extension's Rust dependency cache so the first WASM build is fast.
if [ -d editors/zed ]; then
    ( cd editors/zed && cargo fetch --locked )
fi
