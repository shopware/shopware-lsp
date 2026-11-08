# Contributing to the Shopware LSP

Thanks for your interest in contributing.

## Before opening a pull request

Small fixes can go straight to a PR. Examples:

- typo fixes
- broken links
- small documentation improvements
- obvious bug fixes with a clear test or reproduction

For anything larger, please open an issue first and describe what you want to change before starting implementation. This includes things like support for new file types or new Twig/XML/YAML/PHP constructs, and large refactors. Doing this helps us confirm the direction, avoid duplicate work, and keep the project maintainable.

A draft PR is welcome if it helps explain the idea, but feature PRs should generally be discussed before they are reviewed or merged.

## Pull requests

When opening a PR, please include:

- what changed
- why it changed
- how it was tested, including which editor(s) and which project type
  (platform, production template, plugin, app, plain Symfony)
- any related issue or discussion

Please keep PRs focused. Smaller PRs are easier to review and merge.

## Development

Branch from `main` and target `main` with your pull request.

Before submitting, make sure your changes work locally as they should, and run the checks below:

```sh
make rust-check   # cargo fmt --check, clippy -D warnings, cargo test, integration-suite compile
```

Add or update tests for bug fixes and new behavior.

### Running and measuring tests

Use a crate and a test-name filter while iterating, then run the full suite
before submitting backend changes:

```sh
cargo test -p swlsp-cli mcp
make rust-test
make rust-test-integration-compile
npm --prefix editors/vscode ci
npm --prefix editors/vscode test
```

The VS Code `test` command type-checks and compiles once, then runs every
`test/*.test.js` file. The individual `test:*` commands still compile before
running their selected tests.

For timing comparisons, build the test binaries first (`cargo test --no-run`)
so compilation and execution are measured separately, repeat measurements in
both revision orders, and keep other builds and tests idle while measuring.

Tests must own their fixtures: create temporary roots with
`tempfile::tempdir()` and point `SHOPWARE_LSP_CACHE_DIR`
(`swlsp_app::CACHE_DIR_ENV`) at a temporary directory instead of the
developer's cache. The CLI test helpers in `crates/cli/src/tests/common.rs` do
this once per test binary; tests that change other environment variables must
not run concurrently with readers of the same variable.

CI runs `cargo test` on Linux, macOS and Windows, checks formatting and clippy,
compiles the opt-in real-world integration suite without running it, builds
the server, and verifies `THIRD_PARTY_NOTICES.md`. The `Test` check aggregates
its prerequisites and fails if any of them fails, is cancelled, or is skipped.
CodSpeed runs only the benchmarks in `crates/bench`.

### Using make

The `Makefile` collects the common tasks. The Rust toolchain comes from
`rust-toolchain.toml` (rustup installs it automatically); Node.js 24 and
Python 3 are only needed for the editor clients.

```sh
make check            # Rust, VS Code and Zed validation
make build            # Build target/release/shopware-lsp
make rust-check       # cargo fmt --check, clippy -D warnings, cargo test
make rust-fmt         # Format Rust sources
make notices          # Regenerate THIRD_PARTY_NOTICES.md after dependency changes
```

### Running your build in an editor

Build the binary and point your LSP client at it instead of the released one:

```sh
make build   # then use target/release/shopware-lsp
```

Server logs go to the client's LSP output channel — the VS Code "Shopware Language Server" channel, or `:LspLog` in Neovim.

## Reviews

Maintainers may ask for changes, suggest a different direction, or decline a PR if the approach was not discussed beforehand. That is not personal; it is how we keep the project consistent and sustainable.
