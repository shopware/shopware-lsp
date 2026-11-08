#!/usr/bin/env bash
# Idempotent repository bootstrap for the Shopware LSP Cloud Agent environment.
# Runs after the repository is checked out. Safe to run repeatedly.
set -euo pipefail

export PATH="$HOME/.local/go/bin:$HOME/go/bin:$HOME/.local/node/bin:$HOME/.local/bin:$HOME/.cargo/bin:$PATH"

# Install the server toolchain pinned in rust-toolchain.toml and fetch the
# locked Rust dependencies of the shipped server.
rustup show active-toolchain >/dev/null
cargo fetch --locked

# Go oracle and VS Code extension dependencies.
go mod download
npm --prefix editors/vscode ci

# Warm the Zed extension's Rust dependency cache so the first WASM build is fast.
if [ -d editors/zed ]; then
    ( cd editors/zed && cargo fetch --locked )
fi
