# Crate conventions

How the crates under `crates/` are organized and the rules they share.
`AGENTS.md` holds the contributor rules; `docs/` explains how each subsystem
works.

## Layout

- One crate per subsystem, named `swlsp-<name>`, in `crates/<name>`. The
  `shopware-lsp` binary is `crates/cli`; `crates/app` is the composition
  root.
- Shared utilities are small crates with one job: `swlsp-uriutil` (file
  URIs and lexical path strings), `swlsp-textutil` (case mapping, integer
  literals, ASCII matching), `swlsp-pathmatch` (globs), `swlsp-suggestion`
  (did-you-mean).
- Prefer an established crate to a hand-written one: `globset`, `url` and
  `percent-encoding`, `strsim`, `lru`, `similar`, `humantime`, `thiserror`,
  `tokio`, `tower-lsp-server`, `rmcp`, `reqwest`.

## Syntax trees and parsers

- `swlsp-syntax` holds the language-neutral tree: rowan green trees
  (`Tree`, `SyntaxNode`, `SyntaxToken`) with byte ranges
  (`TextRange`/`TextSize`), and the event parser `Parser` (`peek`, `at`,
  `at_set`, `bump`, `bump_as`, `bump_next_n_as`,
  `start`/`complete`/`precede` with a move-only `Marker`, `expect`,
  `recover`, `add_error`, `finish`). `SyntaxNodeExt` adds navigation
  (`child_of_kind`, `child_token_of_kind`, `ancestor_of_kind`, `token_at`,
  `descendant_for_range`, `node_at`, `range_trimmed_trivia`).
- A language declares its kinds with `swlsp_syntax::language!`: tokens
  first, then nodes, from a reserved, non-overlapping base. Kind values and
  debug names are a contract (CST dumps, tests); add new kinds at the end.
- Parsers expose `pub fn parse(source: &str) -> swlsp_syntax::Parse`, a
  `query` module with the helpers consumers use, and a
  `src/bin/dump-cst-<lang>.rs` calling `swlsp_syntax::dump::run`.
- Use `Tree::node_text` or `token.text()` for text; avoid
  `node.text().to_string()` in hot paths.
- Tokens never split a UTF-8 character: an unknown multi-byte character is
  one token. Sources are decoded as lossy UTF-8.

## Text and paths

- Paths are `&str`/`String` throughout (index keys, URIs, configuration
  scopes). Normalize them with `swlsp_uriutil::filepath` (`clean`, `join`,
  `rel`, `dir`, `base`, `ext`) so equal paths are equal strings; convert
  to and from URIs only with `swlsp_uriutil`.
- Case-insensitive identifier handling uses `swlsp_textutil::case`
  (`lowercase`, `uppercase`, `eq_ignore_case`): one character in, one
  character out, so offsets computed on a folded copy stay valid. Use
  `to_ascii_lowercase` for known-ASCII input.
- Messages quote values with Rust's `{:?}`.

## Errors

- Library code returns typed errors (`thiserror`), keeps the underlying
  error as the source, and uses lowercase messages without trailing
  punctuation.
- Request and index paths do not panic; they return errors or tolerant
  empty results.
- JSON-RPC errors carry a real code: `InvalidParams` for bad parameters,
  `RequestCancelled` for cancellation, `InternalError` otherwise.

## Persistence

- Workspace data lives in one SQLite store with namespaced repositories;
  values are MessagePack. Bump `swlsp_indexer::INDEX_VERSION` whenever
  persisted structure or extraction semantics change.
- The index store checkpoints its write-ahead log from a background thread
  (`Checkpointer`), so indexing workers never wait for an `fsync` inside a
  commit.
- Indexers with substantial extraction implement `PreparingIndexer`:
  parsing and extraction run outside the write transaction and the recorded
  writes are replayed in order.
- `swlsp-php-semantic` interns identifiers of published graphs in a
  generational, sharded, append-only arena (`atom.rs`); its cache format is
  its own.
- The entity-schema snapshot encoding (`swlsp-shopware-entityschema`) is a
  fixed format: snapshot IDs committed in users' plugins are digests of it.

## Determinism

Results reaching a client are deterministic: repository reads order by
`file_path, rowid`, catalogs are sorted with explicit tie-breaks, and maps
that leak into output are ordered (`BTreeMap`) or sorted before use.

## Embedded artifacts

- `crates/projectconfig/schema.json`: the configuration JSON schema,
  embedded with `include_str!`; editors reference the file.
- `crates/php-stubs/data/phpstorm-stubs.msgpack`: PHP runtime stubs written
  by `crates/php-stubs-generate` from `third_party/phpstorm-stubs`.
- `crates/cli/src/mcp/tools.json`: the source of truth for MCP tool names
  and input schemas.

## Concurrency

Tokio runs at the I/O edges only (LSP and MCP transports, sockets, the CLI
session); analysis, indexing and diagnostics stay synchronous on threads.
Requests and document notifications execute in arrival order on one worker
thread. See `docs/tokio-migration.md`.

## Benchmarks and corpora

`scripts/bench/lsp_bench.py` drives a server binary over stdio with a fixed
request mix on files sampled from a Shopware checkout and records index
time, RSS, per-request latency and a hash of every response;
`scripts/bench/compare.py` diffs two runs. Compare revisions one after the
other, never concurrently, with separate cache directories:

```bash
python3 scripts/bench/lsp_bench.py --server target/release/shopware-lsp \
    --root ~/shopware --out target/bench/new.json --cold
python3 scripts/bench/compare.py target/bench/base.json target/bench/new.json
```

A Shopware checkout used as corpus is read-only: never modify it.

## Releases

Shipped binaries use the `dist` profile (stripped, fat LTO); the local
`release` profile keeps line tables for profiles and backtraces. Linux
releases ship a dynamically linked glibc build (glibc 2.17 floor via
cargo-zigbuild) and a static musl build with mimalloc per architecture.
`THIRD_PARTY_NOTICES.md` is generated by cargo-about (`make notices`); CI
fails when it is stale. See `RELEASING.md`.
