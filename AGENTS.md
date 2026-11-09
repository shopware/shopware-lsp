# Shopware LSP Contributor Guide

This file applies to the entire repository. Add a more specific `AGENTS.md`
inside a subdirectory only when that area needs additional rules.

This file is the rulebook: what to do and what not to do. For the *why* — how
each subsystem actually works — see [`docs/README.md`](docs/README.md), starting
with [`docs/architecture.md`](docs/architecture.md).

## Product and architectural invariants

Shopware LSP provides completion, navigation, hover, diagnostics, refactoring,
symbols, and other IDE features for Shopware and Symfony projects. The server
is written in Rust: a Cargo workspace under `crates/`, one crate per subsystem
(`crates/<name>`, package `swlsp-<name>`), with the `shopware-lsp` binary in
`crates/cli`. The VS Code client is TypeScript and the Zed client is
Rust/WASM. `crates/CONVENTIONS.md` records the conventions the crates
share.

Keep these invariants intact:

- The server supports one workspace folder per process. Clients implement
  multi-root support by starting one process per effective workspace folder;
  overlapping supported roots are owned by the outermost root so files are not
  indexed twice.
- The editor, CLI, and MCP server use the same production workspace,
  configuration, indexes, diagnostics, providers, and rewrite engine. Do not
  implement a second analysis path for one frontend.
- Language parsing uses the native, lossless CST infrastructure in
  `crates/syntax` and the `crates/parser-<language>` crates. Do not introduce
  Tree-sitter or a second parser stack.
- CST ranges are byte offsets into the exact source snapshot. Convert to or
  from UTF-16 positions only at the LSP boundary.
- Parsers must tolerate incomplete editor input and return a usable, lossless
  tree. Syntax errors are data, not a reason to discard the tree.
- Workspace data is stored in one namespaced SQLite store. SQLite is bundled
  through `rusqlite`'s `bundled` feature (a C compiler is needed, no system
  library).
- The `swlsp-indexer` watcher (the `notify` crate) feeding `FileScanner` is
  the only source of filesystem index events. Do not add independent watchers
  in feature crates.
- Request-time features must query existing indexes or the current open
  document. They must not rescan the workspace.

## Toolchain and common commands

`rust-toolchain.toml` pins the Rust toolchain (rustup installs it on first
use; keep it in step with `rust-version` in `Cargo.toml`). CI pins Node.js 24
for the VS Code client. The `Makefile` holds the common tasks. The Zed
client's Rust toolchain and WASM target are pinned separately in
`editors/zed/rust-toolchain.toml` for compatibility with Zed's rustup build.

```bash
# Server
make build                       # cargo build --locked --release -p swlsp-cli
cargo test -p swlsp-twig         # one crate while iterating
cargo test -p swlsp-twig name    # tests whose path contains "name"
make rust-test                   # cargo test --locked --workspace (no doc-tests)
make rust-clippy                 # cargo clippy --workspace --all-targets -D warnings
make rust-fmt-check              # cargo fmt --all --check
make rust-test-integration-compile   # clippy over the opt-in real-world suite
make rust-check                  # all of the above
make notices                     # regenerate THIRD_PARTY_NOTICES.md (cargo-about)
cargo run -p swlsp-parser-php --bin dump-cst-php -- FILE   # print a CST
scripts/bench/lsp_bench.py --server BIN --root ~/shopware --out OUT.json
scripts/bench/compare.py A.json B.json       # end-to-end comparison of two runs

# VS Code extension
npm --prefix editors/vscode ci
npm --prefix editors/vscode run check-types
npm --prefix editors/vscode run compile
npm --prefix editors/vscode test
npm --prefix editors/vscode run test:configuration
npm --prefix editors/vscode run test:entity-designer
npm --prefix editors/vscode run test:mcp

# Zed extension (requires rustup and Python 3)
make zed-check
make zed-test
make zed-build
make zed-contracts               # builds the server, checks LSP/MCP contracts

# Releases
make release                     # local VSIX build
make release-dry-run             # package staged binaries into dist/

# Full local validation
make check
```

Set `SHOPWARE_LSP_VERSION=<version>` when building to embed a version (the
binary reports `dev` otherwise). Release binaries are built natively per
target by `.github/workflows/build-binaries.yml` and packaged by
`scripts/release/` (archives, packages, checksums) and
`scripts/build-vsix-release.mjs` (VSIX); see `RELEASING.md`.

Benchmarks live in `crates/bench` (criterion-compatible benches under
`crates/bench/benches`) and run in CI through CodSpeed
(`.github/workflows/codspeed.yml`, `cargo codspeed build -p swlsp-bench`
then `cargo codspeed run`). Run them locally with
`cargo bench -p swlsp-bench`.

Every benchmark case costs CI time on every run, so benchmarks are for
tracking production code paths. Keep A/B comparisons against rejected or
test-only alternatives, and wide size sweeps, out of the committed suite: use
them locally while tuning, then keep one representative case (or a short and
a long input).

Clippy runs with `-D warnings`, so new warnings fail CI. Keep files and
functions focused on one responsibility; split a module by responsibility
instead of reformatting code or adding `#[allow]` to quiet a lint.

Prefer targeted tests while iterating, then run validation proportional to the
change. `make rust-check` (fmt, clippy, tests, integration-suite compile) is
the normal backend pre-handoff check. Run the VS Code checks when protocol
shape, configuration, commands, server startup, MCP wiring, or extension code
changes, and the Zed checks when the Zed client or the server's LSP/MCP
contract changes.

Use `npm ci`, not `npm install`, unless intentionally changing dependencies and
the lockfile.

## Repository map

| Path | Responsibility |
|---|---|
| `crates/cli` | Shipped `shopware-lsp` binary: `serve`, `check`, `index`, feature commands, MCP; flags declared in `src/args.rs` |
| `crates/app` | Workspace construction, ownership, indexer wiring, inspections (`src/inspections.rs`), and provider registration (`src/providers*.rs`) |
| `crates/lsp` | JSON-RPC/LSP server, document lifecycle, configuration, protocol conversion, inspection and quick-fix infrastructure |
| `crates/lsp-protocol` | LSP types (`lsp-types`) plus Shopware protocol extensions |
| `crates/lsp-<feature>` | Providers: completion, definition, hover, reference, symbol, codeaction, refactor, inspections, diagnostics, and others |
| `crates/syntax` | Language-neutral lossless tree, byte ranges, kinds, line index, and the event parser |
| `crates/parser-<language>` | Native lexer, parser, syntax kinds, and `query` helpers for one language (PHP, Twig, JS, XML, XPath, YAML, JSON, SCSS, Vue) |
| `crates/language` | Built-in language registry and extension-to-parser mapping |
| `crates/indexer` | File scanning, watcher, coordinated mutations, SQLite store and repositories, cache versions, and workspace-symbol FTS |
| `crates/rewrite` | Validated immutable source edits and workspace edit plans |
| `crates/php`, `crates/php-<stage>` | PHP project model, binder, semantic graph, types, resolver, inference, PHPDoc, stubs, and rewrites |
| `crates/symfony`, `crates/doctrine`, `crates/twig`, etc. | Domain indexes and framework intelligence |
| `crates/shopware`, `crates/shopware-*`, `crates/admin` | Shopware versioning and Shopware-specific domains such as DAL, entity schemas, and the Administration |
| `crates/projectconfig` | Root/extension configuration, validation, domains, diagnostics policy, and JSON schema |
| `crates/uriutil`, `crates/pathmatch`, `crates/textutil`, etc. | Small shared utilities |
| `crates/bench` | CodSpeed benchmarks |
| `editors/vscode/src` | Thin VS Code client, command UI, configuration UI, generators, and MCP registration |
| `editors/zed` | Rust/WASM Zed client, MCP configuration, terminal task adapters, and contract tests |
| `scripts` | Release and packaging helpers (`scripts/release/`), `bench/` end-to-end LSP benchmark |
| `third_party/phpstorm-stubs` | Pinned source metadata for generated PHP runtime stubs |

Avoid fixed provider or crate counts in documentation or tests; registration
changes frequently. `crates/app/src/providers.rs` and
`crates/app/src/inspections.rs` are the authoritative composition roots.

## Runtime data flow

### Startup and indexing

1. `crates/cli/src/main.rs` parses arguments and starts `serve` (the default
   with no command).
2. The LSP `initialize` request supplies the workspace root and client options.
3. `swlsp_app::Workspace::new` opens the shared store, constructs indexes and
   their dependencies, and registers enabled indexers with `FileScanner`.
4. `register_features` and `register_diagnostic_inspections` connect domain
   services to protocol capabilities.
5. The scanner parses a candidate file once through `swlsp-language`, lets
   indexers prepare read-only data concurrently, and commits per-file changes
   through a coordinated mutation.

### Open documents

The document manager owns the newest in-memory source and CST for an open file.
Features and diagnostics must use that snapshot so unsaved edits take
precedence over disk and index state. On close, diagnostics must be cleared and
future reads may return to indexed/disk state.

### Configuration precedence

Configuration is layered in this order:

1. built-in defaults;
2. workspace `.config/shopware/lsp.yaml`;
3. nested extension `.config/shopware/lsp.yaml` files for files below
   that extension;
4. editor-local overrides supplied by the client.

Nested extension files are diagnostics-only. Their path patterns are relative
to the extension root. Structural settings such as domains, indexing, PHP
extensions, and target versions belong at workspace/editor scope and may
require a restart and cache invalidation.

## Parser work

When changing or adding a parser:

- Keep lexing, parsing, syntax kinds, and queries in their existing separate
  modules (`lexer.rs`, `parser.rs`, `syntax.rs`, `query`).
- Declare a new language's kinds with `swlsp_syntax::language!` and reserve a
  non-overlapping kind base.
- Preserve all input, including whitespace, comments, unknown constructs, and
  malformed fragments.
- Assert that the root text equals the source in parser tests
  (`assert_eq!(parse.root().text().to_string(), source)`).
- Add recovery tests for incomplete constructs likely to occur while typing.
- Use syntax/query helpers in consumers instead of duplicating token walks or
  parsing source with regular expressions.
- Add the frontend and extensions to `crates/language` only after losslessness
  and recovery are covered.
- Provide a `src/bin/dump-cst-<lang>.rs` that calls `swlsp_syntax::dump::run`.
- Run the parser benchmarks in `crates/bench` when changing hot lexer/parser
  paths.

Do not mutate CST nodes. Parse trees represent immutable source snapshots;
source changes are compiled through `swlsp-rewrite`.

## Indexing and persistence

Indexers implement `swlsp_indexer::Indexer` (`crates/indexer/src/indexer.rs`).
Follow these rules:

- Parsing and expensive extraction should happen before the SQLite write
  transaction. Implement `PreparingIndexer` (exposed through
  `Indexer::as_preparing`) when preparation is substantial.
- `index_prepared` should persist prepared data and arrange post-commit
  publication; it should not repeat parsing or expensive inference.
- Save and remove records per source path so file changes and deletions cannot
  leave stale rows.
- Use the shared `ParsedFile::mutation()` and namespaced repositories
  (`DataIndexer`). Do not open an unrelated feature database.
- Keep publication deterministic. Sort results where repository iteration or
  maps could otherwise leak nondeterministic ordering to LSP clients.
- Protect mutable published state with the existing synchronization pattern
  and add concurrency tests after lifecycle or concurrency changes.
- Bump `swlsp_indexer::INDEX_VERSION` (`crates/indexer/src/version.rs`)
  whenever persisted structure or extraction semantics makes existing caches
  incompatible. Update its test in the same change.
- Consider the cold-index, cached-start, disk-size, and request-latency cost of
  every new indexed field. Do not load an entire catalog into memory when a
  targeted SQLite lookup is sufficient.

Use `SHOPWARE_LSP_CACHE_DIR` (`swlsp_app::CACHE_DIR_ENV`) in tests and
diagnostics instead of touching a developer's normal workspace cache.

## LSP providers

Provider traits live in `crates/lsp/src/providers.rs`; implementations live in
the `crates/lsp-<feature>` crates. A normal new provider change should:

1. depend on domain indexes/services through its constructor;
2. return quickly for unsupported languages or contexts;
3. honor `Context::is_cancelled` in non-trivial loops;
4. use `swlsp-uriutil` for file URI conversion;
5. use the current `SyntaxContext` rather than reparsing when it is available;
6. return deterministic, deduplicated results with exact ranges;
7. be constructed and registered in `crates/app/src/providers*.rs`;
8. be gated by the appropriate feature and domain configuration;
9. have focused provider tests plus a server-level test when protocol routing
   or document lifecycle matters.

Do not put workspace construction, scanning, or global singletons inside a
provider.

## Diagnostics and code actions

New diagnostics should use the typed inspection system, not an independent
code-action rediscovery pass.

1. Define stable diagnostic and inspection IDs. IDs are configuration and API
   contracts; do not rename them casually.
2. Analyze a `TextDocument` and report byte-oriented `swlsp_lsp::Problem`
   values.
3. Declare every possible problem in an `InspectionDefinition`, including its
   default severity and whether it is disabled by default.
4. Put serializable context in the problem payload.
5. Bind exact quick-fix IDs and payloads when reporting the problem.
6. Implement deterministic edits as `RewriteQuickFix` implementations using
   `swlsp_rewrite::Builder` and `swlsp_rewrite::WorkspacePlan`.
7. Use `CommandQuickFix` only when editor interaction, snippets, or another
   command-only workflow is genuinely required. MCP can apply workspace edits
   but cannot execute editor-only commands.
8. Prefer lazy fixes for non-trivial or cross-file rewrites so element handles
   can reject stale document versions.
9. Declare the inspection's configuration domain and scope in its
   definition (`DeclaredAnalyzer::INSPECTION` for a single analyzer), next to
   the code, and register it in `crates/app/src/inspections.rs`. Registration
   fails on a missing or unknown domain.
   A rule about the whole project (facts from several files) is a
   `ProjectAnalysis` run through `Project`: a summary memoized per index
   revision, checked per document against its live tree. Declare the
   indexes an analyzer reads in `ProblemAnalyzer::input_revision`, beside its
   fields, only when the list is complete (no disk reads, no undeclared
   index); a refresh then reuses its results while they are unchanged. Cover each new input in
   `crates/app/src/tests/inspection_inputs_test.rs`.
10. Test the diagnostic, fix presentation, generated edit, stale-state
    behavior, configuration override, and MCP/CLI path when externally useful.
    Write diagnostic and fix tests with the fixture markup helpers
    (`check_problems`, `check_diagnostics`, `check_fix`; see
    `docs/diagnostics-pipeline.md#debugging-and-testing`).

Diagnostic ranges and edits must be derived from CST elements or validated byte
ranges. Never construct UTF-16 offsets by counting bytes. Code actions must not
write files directly; return a workspace edit so the editor, CLI, and MCP can
validate and apply it consistently.

## PHP analysis

- Keep parsing, binding, semantic storage, name resolution, and type inference
  as separate stages.
- Prefer the shared `swlsp-php-types` representation and semantic snapshots over
  feature-specific type strings.
- Resolve aliases, inheritance, interfaces, traits, PHPDoc, generics, and
  nullability through the central resolver/inference APIs.
- Do not rebuild the semantic workspace or reverse-reference graph for each
  request. Reuse immutable snapshots and existing lazy indexes.
- Changes to compact persisted PHP graph data require persistence and restart
  tests and may require an index-version bump.
- Generated runtime stubs come from phpstorm-stubs through
  `crates/php-stubs-generate` (`generate-phpstorm-stubs`), which writes
  `crates/php-stubs/data/phpstorm-stubs.msgpack`; do not hand-maintain a
  competing builtin catalog.

## Shopware version-aware features

Use `swlsp_shopware::VersionResolver` and the configured target version.
Version-aware diagnostics should clearly define their version interval, remain
safe when the installed version is unknown, and include tests on both sides of
each boundary. Prefer reusable analyzers and rewrite utilities over copying one
Rector rule verbatim into each inspection.

## Configuration changes

When adding a feature, domain, inspection, or rule, check all applicable
surfaces:

- `crates/projectconfig` defaults, catalog, validation, merging, and
  structural fingerprint;
- `crates/projectconfig/schema.json`;
- inspection-to-domain routing in `crates/lsp/src/configuration.rs`;
- provider/inspection gating in `crates/app`;
- VS Code configuration model/UI when users must control it locally;
- root and nested configuration tests;
- README examples for user-visible settings.

Unknown settings and unknown diagnostic IDs should fail validation rather than
being silently ignored. Preserve backward compatibility when the VS Code
extension can connect to a custom older server binary.

## VS Code extension

Keep `editors/vscode/src/extension.ts` as the composition entry point. Put
commands, configuration models, executable discovery, MCP wiring, and complex
webviews in their existing focused modules.

- The extension is a client and UI layer; domain analysis belongs in the
  Rust server.
- Launch one language-server and MCP definition per effective workspace
  folder. For overlapping supported roots, the outermost root owns its
  descendants; an enabled child may run when its parent is inactive.
- Pass the same editor-local diagnostic overrides to LSP and MCP processes.
- Accept both `WorkspaceEdit.changes` and `documentChanges`, including create
  operations, when handling server responses.
- Keep webview messages typed and validate untrusted message data.
- Do not edit generated `dist/`, packaged binaries, `out/`, or `node_modules/`
  as source.
- Update command contributions in `package.json` when adding a VS Code command.

## CLI and MCP

CLI positions and MCP positions are one-based; LSP protocol positions are
zero-based UTF-16. Keep conversion at the adapter boundary.

The CLI uses GNU-style flags (`--root DIR`, `--json`, `-v`), declared with
`usage-rs` in `crates/cli/src/args.rs`; help and parse errors are generated
from those declarations. MCP lives in `crates/cli/src/mcp`.

- Feature CLI commands should call the production LSP session rather than
  feature packages directly.
- Read-only commands must not mutate workspace files.
- Refactoring commands preview diffs by default and write only with explicit
  write flags.
- MCP tools must validate all paths against the workspace root.
- List code actions before applying one and require an exact action title.
- Workspace-symbol search should keep its fast direct FTS path unless
  `--fresh` is explicitly requested.

## Testing strategy

Tests are plain `#[test]` functions, usually in a `tests.rs` file or `tests/`
module next to the code (`#[cfg(test)] mod tests;`). Use `assert_eq!` for
comparisons, `unwrap()`/`expect("context")` for prerequisites, and
`tempfile::tempdir()` for filesystem fixtures; keep the `TempDir` alive for the
whole test and close stores explicitly:

```rust
#[test]
fn saves_and_reads_values() {
    let directory = tempfile::tempdir().unwrap();
    let store = Store::open(directory.path().join("indexes.db")).unwrap();
    let values = DataIndexer::<i64>::in_store(&store, "numbers").unwrap();
    values.save_item("/a", "key", &42).unwrap();
    assert_eq!(values.get_values("key").unwrap(), [42]);
    store.close().unwrap();
}
```

Add tests at the lowest useful layer, then add an integration test only for a
boundary the unit test cannot cover:

- parser test for syntax and recovery;
- index test for extraction, update, removal, and persistence;
- provider/analyzer test for domain behavior;
- server test for routing, positions, diagnostics, lifecycle, or lazy fixes;
- CLI/MCP/VS Code test for adapter behavior.

Tests should not require network access. External checkouts are read-only
fixtures and must never be modified by tests.

The real-world suite in `crates/app/src/tests/real_world` sits behind the
`integration` feature of `swlsp-app` and defaults to `~/Developer/sw-trunk`;
override it with `SHOPWARE_LSP_REAL_WORLD_ROOT`:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
  cargo test --release -p swlsp-app --features integration --lib \
  -- --exact tests::real_world::indexing_main::shopware_trunk_indexing --nocapture
```

Real-world fixtures move independently. If an unrelated assertion fails,
confirm it on the baseline revision before changing production code or
weakening the assertion.

For a cold/warm indexing resource profile, run `shopware_trunk_indexing_profile`
(`crates/app/src/tests/real_world/index_profile.rs`) the same way. Its
`SHOPWARE_LSP_PROFILE_*` environment variables select the cache directory
(`SHOPWARE_LSP_PROFILE_CACHE_DIR`), worker count, and optional semantic-storage,
reverse-reference and atom measurements:

```bash
SHOPWARE_LSP_PROFILE_CACHE_DIR=/tmp/swlsp-profile \
  cargo test --release -p swlsp-app --features integration --lib \
  shopware_trunk_indexing_profile -- --nocapture
```

Use isolated cache directories and reverse run order when comparing revisions;
filesystem cache warmth and RSS vary enough that one sample is not evidence of
a regression.

## Code quality and change discipline

- Run `cargo fmt` and keep `cargo clippy --all-targets -- -D warnings` clean.
- Add operation context to errors (keep the source error as the cause); keep
  reusable error text lowercase.
- Avoid panics (`unwrap`, indexing, `expect`) in request/index paths. Return
  errors or tolerant empty results where editor-time degradation is
  preferable.
- Keep hot loops allocation-aware, but prefer clear measured improvements over
  speculative complexity.
- Preserve existing uncommitted work. Inspect `git status` and relevant diffs
  before editing; never discard unrelated changes.
- Do not modify `target`, `node_modules`, generated release outputs, or a
  user's external Shopware/plugin checkout unless explicitly requested.
- Update documentation when behavior, commands, configuration, or user-facing
  defaults change.
- Use Conventional Commits when asked to commit, for example
  `feat(twig): add block version diagnostics` or
  `fix(indexer): clear stale file records`.
- Do not commit or push unless explicitly requested.

## Definition of done

Before handing off a change, confirm that:

- the implementation follows the shared parser/index/provider architecture;
- unsaved documents, changed files, removed files, and cache restarts are
  handled where relevant;
- diagnostics and actions respect configuration and domain toggles;
- user-visible edits are represented as validated workspace edits;
- focused regression tests pass;
- `make rust-check`, and the VS Code and Zed checks, were run as
  appropriate;
- index or request-time performance was measured for potentially expensive
  changes;
- any skipped check or pre-existing fixture failure is reported precisely.
