# System Architecture

This document explains how Shopware Language Server is put together: the
process model, the layers and their dependency rules, what happens between
process start and a completion popup, and where to look when you need to change
something.

It is written for someone who has not worked on the codebase before. Read this
first, then follow the links into the subsystem documents.

## Table of contents

- [What the server is](#what-the-server-is)
- [Non-negotiable invariants](#non-negotiable-invariants)
- [Process model](#process-model)
- [Layers and dependency direction](#layers-and-dependency-direction)
- [Crate inventory](#crate-inventory)
- [Lifecycle: from process start to a served request](#lifecycle-from-process-start-to-a-served-request)
- [The three data sources](#the-three-data-sources)
- [Concurrency model](#concurrency-model)
- [Configuration](#configuration)
- [A worked example, end to end](#a-worked-example-end-to-end)
- [Where does my change go?](#where-does-my-change-go)
- [Testing strategy](#testing-strategy)
- [Performance guardrails](#performance-guardrails)
- [Reading order](#reading-order)

## What the server is

A single Rust binary that provides IDE intelligence for Shopware and Symfony
projects — completion, go-to-definition, hover, diagnostics, quick fixes,
refactoring, symbols, code lenses, and more — across PHP, Twig, XML, YAML,
JSON, JavaScript/TypeScript, SCSS, and Vue.

The server is a Cargo workspace under `crates/`: one `swlsp-<name>` crate per
subsystem in `crates/<name>`, and `swlsp-cli` produces the `shopware-lsp`
executable. `crates/CONVENTIONS.md` records the conventions the crates
share.

The same binary is three frontends over one engine:

| Frontend | Entry | Transport |
| --- | --- | --- |
| Language server | `shopware-lsp` (no command) or `shopware-lsp serve` | JSON-RPC over stdin/stdout |
| CLI | `shopware-lsp check`, `definition`, `rename`, … | In-process LSP session over an in-memory stream |
| MCP server | `shopware-lsp mcp` | MCP over stdio, backed by the same session |

The CLI and MCP paths are *not* a second implementation. They start the real
workspace, the real indexes, and the real providers, then speak LSP to
themselves. That is a deliberate invariant: a bug reproducible in the editor is
reproducible from the CLI, and a feature works everywhere as soon as it is
registered once.

A thin TypeScript VS Code client lives in `editors/vscode/`. It discovers the
binary, launches one server per workspace folder, and provides configuration and
generator UI. All analysis lives in the server.

The Rust/WASM Zed client lives alongside it in `editors/zed/`. It starts the
same language server and MCP transport, normalizes editor configuration, and
provides terminal task adapters for generators that need interactive input.
Its contract tests exercise the locally built server and sibling VS Code manifest.

## Non-negotiable invariants

These are architectural constraints, not preferences. Breaking one is a design
change, not a bug fix.

1. **One workspace root per process.** Multi-root clients start one process per
   effective root. Overlapping supported roots are owned by the outermost root
   so files are never indexed twice.
2. **One analysis path.** Editor, CLI, and MCP share the workspace,
   configuration, indexes, diagnostics, providers, and rewrite engine.
3. **One parser stack.** All languages use the native lossless CST under
   `swlsp-syntax` and the `crates/parser-<language>` crates. No tree-sitter, no second parser, no regex-based
   "parsing" in consumers.
4. **Byte offsets internally, UTF-16 only at the protocol boundary.** CST
   ranges are byte offsets into an exact source snapshot. Conversion happens in
   `swlsp-lsp` (and `swlsp-lsp-protocol`), nowhere else.
5. **Parsing is total and lossless.** Incomplete editor input still produces a
   usable tree that reproduces the source exactly. Syntax errors are data.
6. **Trees are immutable.** Nothing mutates a CST. Source changes are compiled
   to byte edits through `swlsp-rewrite`.
7. **One namespaced SQLite store per workspace.** No feature opens its own
   unrelated database.
8. **One filesystem event source.** `FileScanner` owns watching. Feature
   packages never add their own watcher.
9. **Request-time features query, never rescan.** A request reads existing
   indexes and the current open document. It never walks the workspace.

## Process model

```
                       ┌──────────────────────────────┐
   editor / CLI / MCP  │  one OS process per workspace │
   ───────────────────►│                               │
                       │  crates/cli (main.rs)         │
                       │    └─ swlsp_cli::Runner       │  command dispatch, flags
                       │         └─ swlsp_lsp::Server  │  JSON-RPC, documents
                       │              └─ swlsp-app     │  composition root
                       │                   ├─ indexes  │  domain crates
                       │                   ├─ providers│  crates/ide-<domain>  
                       │                   └─ store    │  one SQLite file
                       └──────────────────────────────┘
                                     │
                       ~/.cache/shopware-lsp/<root hash>/
                            indexes.db  file_scanner.db  index_version  …
```

`crates/cli/src/main.rs` is short: it installs an interrupt handler that
cancels the run, selects mimalloc as the allocator on Linux builds, and hands
the arguments to `swlsp_cli::Runner`.

The cache directory is derived from the workspace root
(`crates/app/src/cache.rs`), using SHA-256 of the normalized absolute root. This
avoids collisions between distinct paths and bounds cache directory name length.
Existing caches under separator-substituted names are left untouched; the first
start with the new identity performs a cold index. Set
`SHOPWARE_LSP_CACHE_DIR` to relocate it — always do this in tests so you never
clobber a developer's real cache.

## Layers and dependency direction

Dependencies point **downward** only. A lower layer never imports a higher one.

```
┌────────────────────────────────────────────────────────────────┐
│  Frontends            crates/cli  ·  editors/vscode  ·  editors/zed │
├────────────────────────────────────────────────────────────────┤
│  Composition          crates/app                               │
│                       workspace.rs    → constructs indexes     │
│                       domains.rs      → lists the domains      │
│                       precedence.rs   → orders their providers │
├────────────────────────────────────────────────────────────────┤
│  Domains              crates/ide-<domain>, crates/ide          │
│                       register providers and inspections       │
├────────────────────────────────────────────────────────────────┤
│  Protocol             crates/lsp, crates/lsp-protocol          │
│                       server, documents, capabilities,         │
│                       diagnostics infrastructure, protocol DTOs │
│                       crates/ide-<domain>   → providers        │
├────────────────────────────────────────────────────────────────┤
│  Domain               crates/symfony  crates/twig              │
│                       crates/php      crates/admin             │
│                       crates/doctrine crates/snippet  …        │
│                       (indexes + framework intelligence)        │
├────────────────────────────────────────────────────────────────┤
│  Infrastructure       crates/indexer   crates/rewrite          │
│                       crates/language  crates/projectconfig    │
├────────────────────────────────────────────────────────────────┤
│  Syntax               crates/syntax                            │
│                       crates/parser-<language>                 │
└────────────────────────────────────────────────────────────────┘
```

Two rules follow from this and are worth stating explicitly:

- **`swlsp-app` is the only place that knows the whole system.** It is a
  composition root: it constructs, wires, and registers. It contains no
  analysis logic. If you find yourself adding a `switch` over domains inside a
  provider, the wiring probably belongs in `app`.
- **Domain crates do not depend on `swlsp-lsp`.** They expose plain Rust APIs
  over their indexes. The adapters that turn those into protocol responses live
  in `crates/ide-<domain>`
  and are registered by the domain's `register` function.
  (Analyzers in the `diagnostics` modules of the domain crates are the deliberate exception: they
  sit in the LSP layer precisely because they produce `Problem` values.)

## Crate inventory

Crate names below drop the `swlsp-` prefix; each lives in `crates/<name>`.

### Syntax layer

| Crate | What it does |
| --- | --- |
| `syntax` | Language-neutral lossless tree (rowan green trees), `Kind` registry, byte `TextRange`, `LineIndex`, the event/marker `Parser` with recovery and diagnostics |
| `parser-php`, `parser-twig`, `parser-yaml`, `parser-xml`, `parser-json`, `parser-js`, `parser-scss`, `parser-vue`, `parser-xpath` | One frontend each: `lexer` / `parser` / `syntax` / `query` modules |
| `language` | Extension → frontend registry |

See [`parser-architecture.md`](parser-architecture.md).

### Infrastructure

| Crate | What it does |
| --- | --- |
| `indexer` | `FileScanner`, coordinated `Mutation`s, `DataIndexer` repositories, cache versioning, workspace-symbol FTS, filesystem watchers, PHAR sources |
| `rewrite` | Validated immutable source edits, `ElementHandle`, `WorkspacePlan` |
| `projectconfig` | Committed and editor configuration, domains, diagnostic policy, JSON schema, structural fingerprint |
| `projectdetect` | Is this root a Shopware or Symfony project? |
| `phar` | Reads `.phar` archives without invoking PHP |
| `uriutil`, `pathmatch`, `textutil` | URI conversion, glob matching, small text transforms |

See [`indexing.md`](indexing.md) and
[`refactoring-engine.md`](refactoring-engine.md).

### PHP engine

| Crate | What it does |
| --- | --- |
| `php` | Project model, `PhpIndex`, name resolution, embedded-language detection |
| `php-binder` | Declarations, scopes, imports, references, PHPDoc — file-local and deterministic |
| `php-semantic` | Immutable document and workspace generations, persisted graph, open-file overlays |
| `php-resolver` | Names, inheritance, members, call signatures, generics |
| `php-inference` | Expression and control-flow evaluation, bounded fixed point, framework extensions |
| `php-types` | The immutable type algebra and relation rules |
| `php-stubs` | Version-aware runtime catalog generated from pinned phpstorm-stubs (by `php-stubs-generate`) |
| `php-phpdoc`, `php-literal`, `php-languagelevel`, `php-suppression`, `php-rewrite`, `php-imports`, `php-project`, `php-phpstormmeta`, `php-refactoring` | Supporting stages |

See [`php-semantic-engine.md`](php-semantic-engine.md).

### Domain indexes

Each of these owns one or more namespaced repositories and exposes lookup APIs.
They are registered as `swlsp_indexer::Indexer` implementations in
`crates/app/src/workspace.rs`.

| Crate | Domain |
| --- | --- |
| `symfony` | Service container, routes, route usage, DI aliases |
| `doctrine` | DBAL references and custom type registrations, using Shopware DAL schemas |
| `twig` | Templates, blocks, extends/include graph, versioning |
| `admin-twigmigration` | Administration Twig template migration |
| `twigcomponent` | Twig component catalog |
| `admin` | Shopware Administration components, TypeScript declarations, Vue contracts |
| `snippet`, `translation` | Storefront/admin snippets and Symfony translations |
| `theme`, `style`, `asset` | Theme config and icons, SCSS classes, public assets and Encore entries |
| `feature`, `systemconfig`, `extension` | Feature flags, system config, extension metadata |
| `console`, `event`, `messenger`, `serializer`, `environment`, `symfonyconfig`, `httpclient`, `validation` | Symfony subsystems |
| `shopware`, `shopware-dal`, `shopware-entityschema`, `appscript` | Shopware version resolution, DAL, entity schemas, app scripts |

### Protocol and features

| Crate | What it does |
| --- | --- |
| `lsp` | JSON-RPC server, method dispatch, document manager, capabilities, inspection/quick-fix infrastructure, protocol conversion |
| `lsp-protocol` | Wire DTOs on top of `lsp-types`. No parser or domain state |
| `ide` | The contract of the domain crates (`Registrar`, `Services`, `Domain`), the analyzer and inspection contracts, and per request kind the helpers the domains share |
| `ide-php`, `ide-symfony`, `ide-twig`, `ide-style`, `ide-admin`, `ide-shopware` | One domain each: its providers for every request kind (`completion`, `definition`, `hover`, `reference`, `codelens`, `codeaction`, …), its analyzers (`diagnostics`) and inspections with quick fixes (`inspections`), and the `register` function that adds them to the server |
| `lsp-phpsemantic`, `lsp-phpanalysis` | Adapters from the PHP semantic graph to LSP features |

See [`lsp-server.md`](lsp-server.md) and
[`diagnostics-pipeline.md`](diagnostics-pipeline.md).

## Lifecycle: from process start to a served request

### 1. Process start

`main` → `Runner::run` (`crates/cli`) parses the command line with `usage-rs`
(`crates/cli/src/args.rs`: global flags such as `--root`, `--json`,
`--allow-unsupported-project` and the profiling flags, accepted before or after
the command) and dispatches a command. With no command it runs the server on
stdin/stdout.

### 2. `initialize`

`Server::initialize` (`crates/lsp/src/workspace.rs`) does, in order:

1. Resolve the workspace root from the `initialize` params.
2. Configure client integration from `initializationOptions.shopwareClient` —
   protocol version, presentation profile, supported client commands.
3. **Project detection.** Unless explicitly allowed, `swlsp-projectdetect`
   must find Shopware Composer metadata, an app manifest,
   `symfony/framework-bundle`, `config/bundles.php`, or a committed
   `.config/shopware/lsp.yaml`. An unsupported root makes the server return an
   *inactive* capability set rather than indexing a random directory. In CLI
   mode it is a hard error.
4. Load configuration (workspace file, nested extension scopes, editor
   overrides) and compute the effective configuration.
5. Record client capabilities that change behavior — notably
   `codeAction.resolveSupport` for lazy quick fixes.
6. **Build the workspace** through the injected `WorkspaceFactory`, which in
   production is `swlsp_app::workspace_factory()` (building a `Workspace`).
7. Return `server_capabilities()`, computed from *what was actually registered*
   crossed with the effective feature and domain configuration. A domain that
   is off does not advertise its capability.

### 3. `Workspace::new` — the composition root

`crates/app/src/workspace.rs` builds the workspace in one long, explicit
function (`build`). It:

1. Resolves the cache directory.
2. Runs two cache migrations, either of which forces a full reindex:
   - `check_and_migrate_cache` compares the stored `index_version` against
     `swlsp_indexer::INDEX_VERSION`;
   - `check_and_migrate_configuration` compares the stored fingerprint against
     `structural_fingerprint()`. This matters because file hashes
     are shared by all indexers — re-enabling a disabled indexer must not skip
     files that changed while it was off.
3. Opens the single `Store` (`indexes.db`) and the workspace-symbol
   catalog.
4. Creates the `FileScanner` (`file_scanner.db`).
5. Constructs each domain index, in dependency order, injecting the
   dependencies each one needs (`services.set_php_index(…)`,
   `twig.set_dependencies(…)`, …) and registering PHP type extensions
   (`swlsp_php_inference::FakerTypes`, Shopware, Symfony, event).
6. Resolves the Shopware target version.
7. Registers each index with the scanner **if its domain is enabled**
   (`domain_for_indexer(index.id())` → domain name → configuration lookup).
8. Calls `domains::register_features`: every domain crate in `DOMAINS`
   registers its providers and inspections on a `Registrar`, which then
   applies them to the server in the order of `precedence.rs`.

`close_workspace` tears down in reverse construction order: scanner, then indexers
back-to-front, then the store.

### 4. `initialized` — indexing starts

`handle_initialized` schedules a background job that runs `index_all` and then
starts the filesystem watcher. A CLI-mode client only reaches the watcher when it
sets `initializationOptions.watchFiles`, as the MCP server does. Indexing failure
is reported to the client via a `shopware/indexingFailed` notification but does
not kill the server; features degrade to whatever is indexed.

### 5. Steady state

- Editor edits update the document manager and schedule debounced diagnostics.
- Filesystem changes reach `FileScanner` through the watcher, are debounced
  200 ms, and become indexing and removal calls on the scanner.
- Requests are served from indexes plus the current open document.

Generated Symfony container and route catalogs use supplemental scanner indexers.
Only selected files directly under cache environment directories are admitted;
cache pools and generated class trees remain excluded. Catalog records persist
in the shared store and publish after commit, including deletion and recreation.
Domain constructors do not start watchers or read generated catalogs.

Resource completion queries the scanner's persisted path index, with bounded
results and inferred parent directories, instead of walking the filesystem.

## The three data sources

Every feature answers from some combination of exactly three sources. Knowing
which one you need is most of the design work.

| Source | Owner | Freshness | Use for |
| --- | --- | --- | --- |
| **Open document snapshot** | `DocumentManager` (`swlsp-lsp`) | Current unsaved editor buffer | Anything about the file the cursor is in: position context, ranges, local analysis |
| **Workspace index** | `swlsp_indexer::Store` repositories | Last committed scan | Cross-file lookups: "which service has this id", "which template defines this block" |
| **On-disk source** | filesystem | Whatever is saved | Rarely, and only through an index or a deliberate one-off read |

The precedence rule is: **an open document always wins over the index.** An
unsaved edit must be visible to the feature operating on that file. Where a
domain index needs to see unsaved editor state — the Administration component
registry, for example — it does so through a narrow
`DocumentObserver` overlay (`Server::register_document_observer`), not by
writing editor buffers into the persistent generation. An observer carries
callbacks for open/change and close (see `crates/lsp/src/document.rs`).

Observers are replayed for already-open documents when they register, which
makes workspace initialization order irrelevant.

`swlsp_extension::ConfigIndex` stores full shopware-cli extension configuration
from the native YAML CST in the shared store. `get_config(extension_root)` selects
`.config/shopware-extension.yml`, `.shopware-extension.yml`, or
`.shopware-extension.yaml` in that order, querying only those indexed paths.
Editor overlays preserve that precedence and refresh dependent Composer
diagnostics; closing a config restores the current disk snapshot.

## Concurrency model

Four distinct concurrency regimes, each with different rules.

**1. Normal requests and document updates stay ordered.** A bounded queue and
one worker in `crates/lsp/src/transport.rs` serialize normal handlers. The transport
reader handles `$/cancelRequest` immediately, cancelling the context of a
running or queued request by ID. Disconnect cancels active work before teardown.
CLI pull diagnostics retain their background execution so `check` can analyze
several files concurrently.

**2. Diagnostics run in cancellable background jobs.** One job per URI,
debounced 150 ms, superseded (and cancelled) by the next edit. See
[`diagnostics-pipeline.md`](diagnostics-pipeline.md#scheduling-debouncing-and-caching).

**3. Indexing is a parallel fan-out with a serialized writer.** The scanner
runs `min(configured workers, number of files)` workers. Each worker reads,
hashes, parses, and lets every `PreparingIndexer` do read-only extraction
concurrently. Persistence funnels through a single-writer gate on the store, so
exactly one SQLite write transaction is open at a time. See
[`indexing.md`](indexing.md#the-indexing-run).

**4. Background lifecycle work** goes through `Server::start_background`,
which refuses new work once closing has begun and counts running jobs so
shutdown can wait for them.

Shared mutable state to be careful with:

- Domain indexes publish immutable generations and swap a pointer. Readers take
  the pointer once; they never see a half-updated catalog.
- `TextDocument` is an immutable snapshot. Never mutate one; publish a new one.

## Configuration

Configuration is layered, in increasing precedence:

1. built-in defaults (`swlsp-projectconfig`);
2. workspace `.config/shopware/lsp.yaml`;
3. nested extension `.config/shopware/lsp.yaml` files, applying to files below
   that extension — **diagnostics-only**, with path patterns relative to the
   extension root;
4. editor-local overrides supplied by the client.

Three orthogonal switches gate behavior:

- **Features** (`completion`, `hover`, `formatting`, `codeActions`, …) gate LSP capabilities
  and method dispatch. A disabled feature's method returns a disabled result.
- **Domains** (`php`, `symfony.routes`, `shopware.snippets`, `administration`,
  …) gate whole subsystems: an index may not even be constructed, and its
  providers and inspections are not registered.
- **Diagnostic rules** set per-code severity or `off`.

Unknown settings and unknown diagnostic IDs **fail validation** rather than
being ignored — silent typos in configuration are worse than a startup error.

Structural settings (domains, indexing, PHP extensions, target versions) feed
`structural_fingerprint()`. Changing one invalidates the whole workspace cache
on next start, because the set of things that were indexed changed.

## A worked example, end to end

Feature flag completion. It touches the parser, an index, and a provider, and
crosses four languages — a good map of the whole system.

**1. Parse.** `config/services/feature.yaml` is a YAML file, so
the `swlsp-language` registry picks the YAML frontend and produces a lossless
CST.

**2. Index.** `swlsp_feature::FeatureIndexer` implements `Indexer`:

```rust
// crates/feature/src/indexer.rs
fn index(&self, file: &ParsedFile) -> Result<()> {
    let path = file.path();
    if !path.ends_with(".yaml") && !path.ends_with(".yml") {
        return Ok(()); // cheap rejection first
    }
    if !path.to_lowercase().contains("feature") {
        return Ok(());
    }
    let features = match file.syntax_tree() {
        Some(tree) => parse_feature_tree(tree, file.line_index(), path),
        // ...
    };
    // ...
    self.features
        .batch_save_items_in(file.mutation(), [(path, items)])
        .context("saving features")
}
```

Note three habits: reject non-candidates before touching the tree; take the
tree from `ParsedFile` (already parsed once, shared by every indexer); persist
through the coordinated `file.mutation()`.

**3. Extract.** `swlsp_feature::parse_feature_tree` uses the YAML `query`
module rather than walking children by index:

```rust
// crates/feature/src/lib.rs
for pair in tree.root().descendants().filter(|node| node.kind() == YAML_PAIR) {
    if query::pair_key(&pair).is_none_or(|key| query::scalar_value(&key) != "name") {
        continue;
    }
    let Some(value) = query::pair_value(&pair) else { continue };
    if value.kind() != YAML_SCALAR || query::is_null(&value) {
        continue;
    }
    let (line, _) = line_index.position(value.range_trimmed_trivia().start().into());
    // push Feature { name, file, line: line + 1 }
}
```

**4. Serve.** `FeatureCompletionProvider` (`swlsp-ide-shopware`) answers in
four languages by asking each language's query module the same question:

```rust
// crates/ide-shopware/src/completion/shopware/feature.rs
let matches = match swlsp_uriutil::filepath::ext(&request.params.text_document.uri) {
    ".twig" => swlsp_parser_twig::query::string_in_function(node, &["feature"]),
    ".scss" => swlsp_parser_scss::query::string_in_function(node, &["feature"]),
    ".php" => swlsp_parser_php::query::string_in_call(node, 0, &["Feature::isActive"]),
    ".js" | ".ts" => swlsp_parser_js::query::string_in_call(
        node,
        0,
        &["Feature.isActive", "Shopware.Feature.isActive"],
    ),
    _ => false,
};
// matches → self.index.all_features() → Vec<CompletionItem>
```

`request.node` is the CST node under the cursor, already resolved by
`SyntaxContext`. The provider does no parsing and no scanning.

**5. Register.** In the `register` function of the feature's domain crate
(`crates/ide-<domain>`), constructed with its index from `Services`. That is
the only place the wiring exists.

The whole feature is a small amount of production code across three files,
because every layer it needs already exists.

## Where does my change go?

| I want to… | Go to | Also read |
| --- | --- | --- |
| Support new syntax in an existing language | `crates/parser-<lang>/src/{lexer,parser,syntax}.rs` | [parser](parser-architecture.md) |
| Add a whole new language | a new `crates/parser-<lang>` + `crates/language/src/lib.rs` | [parser: adding a language](parser-architecture.md#adding-a-language) |
| Ask a new question about a tree | `crates/parser-<lang>/src/query` | [parser: querying](parser-architecture.md#querying-nodes) |
| Index a new kind of file or field | the domain crate + `crates/app/src/workspace.rs` | [indexing](indexing.md) |
| Add completion / definition / hover / … | `crates/ide-<domain>/src/<request kind>` + its `register` function | [lsp server](lsp-server.md) |
| Add a diagnostic | `crates/ide-<domain>/src/diagnostics` (analyzer) + `src/inspections` (inspection, quick fix, registration) | [diagnostics](diagnostics-pipeline.md) |
| Add a code lens or code action | `crates/ide-<domain>/src/codelens`, `src/codeaction` | [lsp server](lsp-server.md) |
| Add a quick fix or refactoring | `crates/rewrite`, `crates/ide-<domain>/src/inspections`, `crates/ide-<domain>/src/refactor` | [refactoring](refactoring-engine.md) |
| Change PHP type or inference behavior | `crates/php-types`, `crates/php-resolver`, `crates/php-inference` | [php engine](php-semantic-engine.md) |
| Add a setting, domain, or rule | `crates/projectconfig` (+ schema, + gating, + VS Code model) | [reference](reference.md#project-configuration) |
| Add a CLI command | `crates/cli` (`src/args.rs` declares commands and flags) | — |
| Change the editor client | `editors/vscode/src` | [phpstorm integration](phpstorm-integration.md) |

Two anti-patterns worth naming, because they are the ones newcomers reach for:

- **Parsing with regular expressions or manual token walks in a consumer.**
  The tree already exists; add a query helper instead.
- **Scanning the workspace at request time.** If the data is not indexed, index
  it. A request that walks the filesystem will be unusably slow on a real
  Shopware checkout.

## Testing strategy

Test at the lowest layer that can express the behavior, then add exactly one
higher-level test for the boundary a unit test cannot cover.

| Layer | Test | Asserts |
| --- | --- | --- |
| Parser | `crates/parser-<lang>` tests | Losslessness (root text == source), recovery on incomplete input, debug-tree shape |
| Index | the domain crate | Extraction, update, removal, persistence across restart |
| Query | `crates/parser-<lang>/src/query` tests | Structural questions, including negative cases |
| Provider / analyzer | `crates/ide-<domain>` | Domain behavior from a source string |
| Server | `crates/lsp`, `crates/app` | Routing, UTF-16 positions, document lifecycle, lazy fixes |
| Adapter | `crates/cli`, `editors/vscode`, `editors/zed` | One-based positions, diff previews, MCP shapes |

Conventions: plain `#[test]` functions in a `tests` module or `src/tests/`,
`assert_eq!`/`assert!` for comparisons, `tempfile::tempdir()` for cache
directories (kept alive for the test's duration), and explicit `close()` of
indexes where the test checks persistence. No network access. Pass an explicit
cache directory (for example `Workspace::new_in`) rather than touching a real
cache.

Commands:

```bash
cargo test -p swlsp-twig                 # one crate while iterating
make rust-check                           # fmt, clippy, tests, integration-suite compile
make build                                # release build of swlsp-cli
make check                                # Rust, VS Code and Zed validation
```

Opt-in real-world tests run against a Shopware checkout behind the
`integration` feature of `swlsp-app`:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
  cargo test --release -p swlsp-app --features integration --lib -- \
  --exact tests::real_world::indexing_main::shopware_trunk_indexing
```

Real-world fixtures move independently of this repository. If an unrelated
assertion fails, confirm it on the baseline revision before changing production
code.

Clippy runs with `-D warnings`. Size and complexity are reviewed by
responsibility rather than enforced by a metric; grammar dispatch is
legitimately branch-heavy. See [`maintainability.md`](maintainability.md).

## Performance guardrails

The target workload is a full Shopware monorepo — hundreds of thousands of
files. The costs that matter, and where they are controlled:

| Cost | Controlled by |
| --- | --- |
| Cold index wall time | Scanner worker fan-out feeding one writer thread, `PreparingIndexer` moving work out of the write transaction, mimalloc on Linux, cheap path/content rejection before parsing |
| Cold index peak RSS | Per-file memoized analysis released right after preparation, a two-batch writer queue, interned PHP graph identifiers |
| Warm start time | Persisted compact index values, `visit_all_values` streaming instead of materializing catalogs, FTS symbol catalog read directly |
| Disk size | MessagePack-encoded values, per-path rows, index-version invalidation instead of migrations |
| Request latency | Indexes answer lookups; documents are parsed once per snapshot; `TextDocument::memoized_analysis` shares derived analysis across features |

Measurement entry points:

```bash
cargo bench -p swlsp-bench                # parser and PHP benchmarks (CodSpeed in CI)
scripts/bench/lsp_bench.py --server target/release/shopware-lsp --root ~/shopware --out out.json
SHOPWARE_LSP_TRACE_DIAGNOSTICS=1 shopware-lsp
shopware-lsp --root /path/to/project stats
```

When comparing revisions, use isolated cache directories and reverse the run
order. Filesystem cache warmth and RSS vary enough that one sample is not
evidence of a regression.

## Reading order

For a new contributor:

1. **This document** — the shape of the system.
2. [`parser-architecture.md`](parser-architecture.md) — everything sits on the
   CST; understanding kinds, ranges, and the query packages unblocks most work.
3. The subsystem you are changing:
   - [`indexing.md`](indexing.md) — scanning, repositories, persistence
   - [`lsp-server.md`](lsp-server.md) — protocol, providers, documents
   - [`diagnostics-pipeline.md`](diagnostics-pipeline.md) — inspections and
     quick fixes
   - [`refactoring-engine.md`](refactoring-engine.md) — edits and workspace
     plans
   - [`php-semantic-engine.md`](php-semantic-engine.md) — the PHP pipeline
4. [`AGENTS.md`](../AGENTS.md) — the contributor rulebook: conventions,
   checklists, definition of done.
5. [`reference.md`](reference.md) — the exhaustive feature, command, and
   configuration catalog.
