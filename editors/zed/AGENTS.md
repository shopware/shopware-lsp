# Zed extension

The repository-wide contributor guide applies. This directory contains the
Rust/WASM client, terminal task adapters, and their tests; analysis stays in the
shared Go server.

- Build for `wasm32-wasip2` with the toolchain in `rust-toolchain.toml`.
- Keep host-independent logic in pure helpers and Zed API hooks thin. Native
  Rust tests cannot call the extension host's imported functions.
- The WASM sandbox can inspect managed downloads in its work directory, but
  cannot stat paths supplied by settings or `Worktree::which`.
- Keep initialization, workspace configuration, and MCP editor overrides in
  the same normalized server configuration shape.
- Preserve upstream attribution in `LICENSE` and `README.md`.
- Task positions use one-based UTF-8 byte columns; LSP uses zero-based UTF-16.
  Use the existing conversion helpers before applying edits.
- Tasks that consume file contents must save the current document first.
  Keep task labels and keymap references consistent with the Python tests.
- Review inventory changes before updating `inventory/snapshot.json`; each
  palette/client command needs an explicit entry in `inventory/parity.json`.
- Do not install tasks into the developer's Zed configuration during tests.
  Use `--print` or an isolated `ZED_CONFIG_DIR`.

From the repository root, run `mise run zed:check`, `mise run zed:test`,
`mise run zed:build`, and `mise run zed:contracts`. The contract task builds
the local server and checks its LSP/MCP contracts and the sibling VS Code
manifest without downloading a published server. Registry checks remain in
the separate scheduled workflow. See `TESTING.md` for manual Zed checks.
