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
- [Package inventory](#package-inventory)
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

A single Go binary that provides IDE intelligence for Shopware and Symfony
projects — completion, go-to-definition, hover, diagnostics, quick fixes,
refactoring, symbols, code lenses, and more — across PHP, Twig, XML, YAML,
JSON, JavaScript/TypeScript, SCSS, and Vue.

The same binary is three frontends over one engine:

| Frontend | Entry | Transport |
| --- | --- | --- |
| Language server | `shopware-lsp` (no command) or `shopware-lsp serve` | JSON-RPC over stdin/stdout |
| CLI | `shopware-lsp check`, `definition`, `rename`, … | In-process LSP session over an internal socket pair |
| MCP server | `shopware-lsp mcp` | MCP over stdio, backed by the same session |

The CLI and MCP paths are *not* a second implementation. They start the real
workspace, the real indexes, and the real providers, then speak LSP to
themselves. That is a deliberate invariant: a bug reproducible in the editor is
reproducible from the CLI, and a feature works everywhere as soon as it is
registered once.

A thin TypeScript VS Code client lives in `vscode-extension/`. It discovers the
binary, launches one server per workspace folder, and provides configuration and
generator UI. All analysis lives in Go.

## Non-negotiable invariants

These are architectural constraints, not preferences. Breaking one is a design
change, not a bug fix.

1. **One workspace root per process.** Multi-root clients start one process per
   effective root. Overlapping supported roots are owned by the outermost root
   so files are never indexed twice.
2. **One analysis path.** Editor, CLI, and MCP share the workspace,
   configuration, indexes, diagnostics, providers, and rewrite engine.
3. **One parser stack.** All languages use the native lossless CST under
   `internal/parser`. No tree-sitter, no second parser, no regex-based
   "parsing" in consumers.
4. **Byte offsets internally, UTF-16 only at the protocol boundary.** CST
   ranges are byte offsets into an exact source snapshot. Conversion happens in
   `internal/lsp`, nowhere else.
5. **Parsing is total and lossless.** Incomplete editor input still produces a
   usable tree that reproduces the source exactly. Syntax errors are data.
6. **Trees are immutable.** Nothing mutates a CST. Source changes are compiled
   to byte edits through `internal/rewrite`.
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
                       │  main.go                      │
                       │    └─ internal/cli            │  command dispatch, flags
                       │         └─ internal/lsp.Server │  JSON-RPC, documents
                       │              └─ internal/app   │  composition root
                       │                   ├─ indexes   │  domain packages
                       │                   ├─ providers │  internal/lsp/<feature>
                       │                   └─ store     │  one SQLite file
                       └──────────────────────────────┘
                                     │
                       ~/.cache/shopware-lsp/<escaped root>/
                            indexes.db  file_scanner.db  index_version  …
```

`main.go` is 40 lines: it applies the runtime memory policy
(`internal/runtimeconfig`), installs a signal-cancelled context, and hands
`os.Args` to `internal/cli`.

The cache directory is derived from the workspace root
(`internal/app/cache.go`), with `/`, `:`, and `\` replaced by `_`. Set
`SHOPWARE_LSP_CACHE_DIR` to relocate it — always do this in tests so you never
clobber a developer's real cache.

## Layers and dependency direction

Dependencies point **downward** only. A lower layer never imports a higher one.

```
┌────────────────────────────────────────────────────────────────┐
│  Frontends            internal/cli  ·  vscode-extension/       │
├────────────────────────────────────────────────────────────────┤
│  Composition          internal/app                             │
│                       workspace.go   → constructs indexes      │
│                       providers*.go  → registers providers     │
│                       inspections.go → registers inspections   │
├────────────────────────────────────────────────────────────────┤
│  Protocol             internal/lsp                             │
│                       server, documents, capabilities,         │
│                       diagnostics infrastructure, protocol DTOs │
│                       internal/lsp/<feature>  → providers      │
├────────────────────────────────────────────────────────────────┤
│  Domain               internal/symfony  internal/twig          │
│                       internal/php      internal/admin         │
│                       internal/doctrine internal/snippet  …    │
│                       (indexes + framework intelligence)        │
├────────────────────────────────────────────────────────────────┤
│  Infrastructure       internal/indexer   internal/rewrite      │
│                       internal/language  internal/projectconfig │
├────────────────────────────────────────────────────────────────┤
│  Syntax               internal/parser/cst  · parsekit          │
│                       internal/parser/<language>/…             │
│                       internal/parser/bytescan                  │
└────────────────────────────────────────────────────────────────┘
```

Two rules follow from this and are worth stating explicitly:

- **`internal/app` is the only place that knows the whole system.** It is a
  composition root: it constructs, wires, and registers. It contains no
  analysis logic. If you find yourself adding a `switch` over domains inside a
  provider, the wiring probably belongs in `app`.
- **Domain packages do not import `internal/lsp`.** They expose plain Go APIs
  over their indexes. The adapters that turn those into protocol responses live
  in `internal/lsp/<feature>` and are wired in `internal/app/providers*.go`.
  (Analyzers in `internal/lsp/diagnostics` are the deliberate exception: they
  sit in the LSP layer precisely because they produce `lsp.Problem` values.)

## Package inventory

Sizes are indicative of where the mass is, not a quality signal.

### Syntax layer

| Package | What it does |
| --- | --- |
| `parser/cst` | Language-neutral lossless tree, `Kind` registry, `TextRange`, `LineIndex`, `Builder`, slab pools |
| `parser/parsekit` | Language-neutral event/marker parser, trivia-skipping cursor, recovery, diagnostics, the sink |
| `parser/bytescan` | Allocation-free byte scans with optional SIMD |
| `parser/php`, `twig`, `yaml`, `xml`, `json`, `javascript`, `scss`, `vue`, `xpath` | One frontend each: `lexer` / `parser` / `syntax` / `query` |
| `language` | Extension → frontend registry (`ParsePath`) |

See [`parser-architecture.md`](parser-architecture.md).

### Infrastructure

| Package | What it does |
| --- | --- |
| `indexer` | `FileScanner`, coordinated `Mutation`s, `DataIndexer` repositories, cache versioning, workspace-symbol FTS, filesystem watchers, PHAR sources |
| `rewrite` | Validated immutable source edits, `ElementHandle`, `WorkspacePlan` |
| `projectconfig` | Committed and editor configuration, domains, diagnostic policy, JSON schema, structural fingerprint |
| `projectdetect` | Is this root a Shopware or Symfony project? |
| `runtimeconfig` | Process-wide GC / memory policy |
| `phar` | Reads `.phar` archives without invoking PHP |
| `uriutil`, `pathmatch`, `textutil` | URI conversion, glob matching, small text transforms |

See [`indexing.md`](indexing.md) and
[`refactoring-engine.md`](refactoring-engine.md).

### PHP engine

| Package | What it does |
| --- | --- |
| `php` | Project model, `PHPIndex`, name resolution, embedded-language detection |
| `php/binder` | Declarations, scopes, imports, references, PHPDoc — file-local and deterministic |
| `php/semantic` | Immutable document and workspace generations, persisted graph, open-file overlays |
| `php/resolver` | Names, inheritance, members, call signatures, generics |
| `php/inference` | Expression and control-flow evaluation, bounded fixed point, framework extensions |
| `php/types` | The immutable type algebra and relation rules |
| `php/stubs` | Version-aware runtime catalog generated from pinned phpstorm-stubs |
| `php/phpdoc`, `php/literal`, `php/languagelevel`, `php/suppression`, `php/rewrite`, `php/project`, `php/phpstormmeta` | Supporting stages |

See [`php-semantic-engine.md`](php-semantic-engine.md).

### Domain indexes

Each of these owns one or more namespaced repositories and exposes lookup APIs.
They are registered as `indexer.Indexer` implementations in
`internal/app/workspace.go`.

| Package | Domain |
| --- | --- |
| `symfony` | Service container, routes, route usage, DI aliases |
| `doctrine` | ORM metadata, DQL, mappings |
| `twig` | Templates, blocks, extends/include graph, versioning |
| `twigcomponent` | Twig component catalog |
| `admin` | Shopware Administration components, TypeScript declarations, Vue contracts |
| `snippet`, `translation` | Storefront/admin snippets and Symfony translations |
| `theme`, `style`, `asset` | Theme config and icons, SCSS classes, public assets and Encore entries |
| `feature`, `systemconfig`, `extension` | Feature flags, system config, extension metadata |
| `console`, `event`, `messenger`, `form`, `security`, `serializer`, `environment`, `symfonyconfig`, `stimulus`, `httpclient`, `validation` | Symfony subsystems |
| `shopware`, `shopware/dal`, `shopware/entityschema`, `appscript` | Shopware version resolution, DAL, entity schemas, app scripts |

### Protocol and features

| Package | What it does |
| --- | --- |
| `lsp` | JSON-RPC server, method dispatch, document manager, capabilities, inspection/quick-fix infrastructure, protocol conversion |
| `lsp/protocol` | Wire DTOs. No parser or domain state |
| `lsp/completion`, `definition`, `hover`, `reference`, `codelens`, `codeaction`, `symbol`, `semantic`, `signature`, `inlay`, `folding`, `selection`, `highlight`, `linkedediting`, `documentlink`, `color`, `callhierarchy`, `refactor`, `scaffold` | Provider implementations |
| `lsp/diagnostics` | Analyzers producing `lsp.Problem` values |
| `lsp/inspections` | Inspection declarations binding analyzers to codes and fixes |
| `lsp/phpsemantic`, `lsp/phpanalysis` | Adapters from the PHP semantic graph to LSP features |

See [`lsp-server.md`](lsp-server.md) and
[`diagnostics-pipeline.md`](diagnostics-pipeline.md).

## Lifecycle: from process start to a served request

### 1. Process start

`main.go` → `cli.Runner.Run` parses global flags (`-root`, `-json`,
`-allow-unsupported-project`, profiling flags) and dispatches a command. With no
command it runs the server on stdin/stdout.

### 2. `initialize`

`lsp.Server.initialize` (`internal/lsp/workspace.go`) does, in order:

1. Resolve the workspace root from the `initialize` params.
2. Configure client integration from `initializationOptions.shopwareClient` —
   protocol version, presentation profile, supported client commands.
3. **Project detection.** Unless explicitly allowed, `projectdetect.Detect`
   must find Shopware Composer metadata, an app manifest,
   `symfony/framework-bundle`, `config/bundles.php`, or a committed
   `.config/shopware/lsp.json`. An unsupported root makes the server return an
   *inactive* capability set rather than indexing a random directory. In CLI
   mode it is a hard error.
4. Load configuration (workspace file, nested extension scopes, editor
   overrides) and compute the effective configuration.
5. Record client capabilities that change behavior — notably
   `codeAction.resolveSupport` for lazy quick fixes.
6. **Build the workspace** through the injected `WorkspaceFactory`, which in
   production is `app.NewWorkspace`.
7. Return `serverCapabilities()`, computed from *what was actually registered*
   crossed with the effective feature and domain configuration. A domain that
   is off does not advertise its capability.

### 3. `app.NewWorkspace` — the composition root

`internal/app/workspace.go` is one long, explicit function. It:

1. Resolves the cache directory.
2. Runs two cache migrations, either of which forces a full reindex:
   - `CheckAndMigrateCache` compares the stored `index_version` against
     `indexer.IndexVersion`;
   - `CheckAndMigrateConfiguration` compares the stored fingerprint against
     `configuration.StructuralFingerprint()`. This matters because file hashes
     are shared by all indexers — re-enabling a disabled indexer must not skip
     files that changed while it was off.
3. Opens the single `indexer.Store` (`indexes.db`) and the workspace-symbol
   catalog.
4. Creates the `FileScanner` (`file_scanner.db`).
5. Constructs each domain index, in dependency order, injecting the
   dependencies each one needs (`serviceIndex.SetPHPIndex(phpIndex)`,
   `twigIndex.SetDependencies(phpIndex, serviceIndex)`, …) and registering PHP
   type extensions (`inference.FakerTypes`, Shopware, Symfony, Doctrine, event).
6. Resolves the Shopware target version.
7. Registers each index with the scanner **if its domain is enabled**
   (`domainForIndexer(idx.ID())` → domain name → configuration lookup).
8. Calls `registerFeatures(server, root, workspaceServices{…})`, which fans out
   to `providers_completion.go`, `providers_definition.go`, `providers_hover.go`,
   … and `inspections.go`.

`Close` tears down in reverse construction order: scanner, then indexers
back-to-front, then the store.

### 4. `initialized` — indexing starts

`handleInitialized` schedules a background job that runs `indexAll` and then
starts the filesystem watcher. Indexing failure is reported to the client via a
`shopware/indexingFailed` notification but does not kill the server; features
degrade to whatever is indexed.

### 5. Steady state

- Editor edits update the document manager and schedule debounced diagnostics.
- Filesystem changes reach `FileScanner` through the watcher, are debounced
  200 ms, and become `IndexFiles` / `RemoveFiles` calls.
- Requests are served from indexes plus the current open document.

## The three data sources

Every feature answers from some combination of exactly three sources. Knowing
which one you need is most of the design work.

| Source | Owner | Freshness | Use for |
| --- | --- | --- | --- |
| **Open document snapshot** | `lsp.DocumentManager` | Current unsaved editor buffer | Anything about the file the cursor is in: position context, ranges, local analysis |
| **Workspace index** | `indexer.Store` repositories | Last committed scan | Cross-file lookups: "which service has this id", "which template defines this block" |
| **On-disk source** | filesystem | Whatever is saved | Rarely, and only through an index or a deliberate one-off read |

The precedence rule is: **an open document always wins over the index.** An
unsaved edit must be visible to the feature operating on that file. Where a
domain index needs to see unsaved editor state — the Administration component
registry, for example — it does so through a narrow
`lsp.DocumentObserver` overlay (`RegisterDocumentObserver`), not by writing
editor buffers into the persistent generation:

```go
server.RegisterDocumentObserver(lsp.DocumentObserver{
    DidOpenOrChange: func(document *lsp.TextDocument) { /* overlay */ },
    DidClose:        func(uri string) { /* drop overlay */ },
})
```

Observers are replayed for already-open documents when they register, which
makes workspace initialization order irrelevant.

## Concurrency model

Four distinct concurrency regimes, each with different rules.

**1. Request handling is sequential.** `Server.Start` wraps the handler in
`jsonrpc2.HandlerWithError`, which processes requests in order. A slow provider
therefore blocks the next request — so providers must return fast and must
honor `ctx` cancellation. The single exception is `textDocument/diagnostic` in
CLI mode, which `cliDiagnosticHandler` moves to a background goroutine so
`check` can analyze several files concurrently.

**2. Diagnostics run in cancellable background jobs.** One job per URI,
debounced 150 ms, superseded (and cancelled) by the next edit. See
[`diagnostics-pipeline.md`](diagnostics-pipeline.md#scheduling-debouncing-and-caching).

**3. Indexing is a parallel fan-out with a serialized writer.** The scanner
runs `min(NumCPU-based default, len(files))` workers. Each worker reads,
hashes, parses, and lets every `PreparingIndexer` do read-only extraction
concurrently. Persistence funnels through a single-writer gate on the store, so
exactly one SQLite write transaction is open at a time. See
[`indexing.md`](indexing.md#the-indexing-run).

**4. Background lifecycle work** goes through `Server.startBackground`, which
refuses new work once closing has begun and tracks everything in a
`sync.WaitGroup` so shutdown is clean.

Shared mutable state to be careful with:

- Pooled parser and CST buffers (`parsekit`, `cst`) are process-global and
  mutex-guarded. Anything touching them needs `go test -race`.
- Domain indexes publish immutable generations and swap a pointer. Readers take
  the pointer once; they never see a half-updated catalog.
- `TextDocument` is an immutable snapshot. Never mutate one; publish a new one.

## Configuration

Configuration is layered, in increasing precedence:

1. built-in defaults (`projectconfig.Default()`);
2. workspace `.config/shopware/lsp.json`;
3. nested extension `.config/shopware/lsp.json` files, applying to files below
   that extension — **diagnostics-only**, with path patterns relative to the
   extension root;
4. editor-local overrides supplied by the client.

Three orthogonal switches gate behavior:

- **Features** (`completion`, `hover`, `codeActions`, …) gate LSP capabilities
  and method dispatch. A disabled feature's method returns a disabled result.
- **Domains** (`php`, `symfony.routes`, `shopware.snippets`, `administration`,
  …) gate whole subsystems: an index may not even be constructed, and its
  providers and inspections are not registered.
- **Diagnostic rules** set per-code severity or `off`.

Unknown settings and unknown diagnostic IDs **fail validation** rather than
being ignored — silent typos in configuration are worse than a startup error.

Structural settings (domains, indexing, PHP extensions, target versions) feed
`StructuralFingerprint()`. Changing one invalidates the whole workspace cache
on next start, because the set of things that were indexed changed.

## A worked example, end to end

Feature flag completion. It touches the parser, an index, and a provider, and
crosses four languages — a good map of the whole system.

**1. Parse.** `config/services/feature.yaml` is a YAML file, so
`language.Registry.ParsePath` picks the YAML frontend and produces a lossless
CST.

**2. Index.** `feature.FeatureIndexer` implements `indexer.Indexer`:

```go
// internal/feature/indexer.go
func (i *FeatureIndexer) Index(file *indexer.ParsedFile) error {
    if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
        return nil                                    // cheap rejection first
    }
    if !strings.Contains(strings.ToLower(path), "feature") {
        return nil
    }
    features, err := ParseFeatureTree(file.SyntaxTree(), file.LineIndex(), path)
    // ...
    return i.featureIndex.BatchSaveItemsIn(file.Mutation(), batchSave)
}
```

Note three habits: reject non-candidates before touching the tree; take the
tree from `ParsedFile` (already parsed once, shared by every indexer); persist
through the coordinated `file.Mutation()`.

**3. Extract.** `feature.ParseFeatureTree` uses the YAML query package rather
than walking children by index:

```go
// internal/feature/feature.go
for _, pair := range yamlquery.Nodes(tree.Root, yamlsyntax.YamlPair) {
    if yamlquery.ScalarValue(yamlquery.PairKey(pair)) != "name" {
        continue
    }
    value := yamlquery.PairValue(pair)
    if value == nil || value.Kind() != yamlsyntax.YamlScalar || yamlquery.IsNull(value) {
        continue
    }
    line, _ := lineIndex.Position(value.RangeTrimmedTrivia().Start)
    features = append(features, Feature{Name: yamlquery.ScalarValue(value), File: filePath, Line: int(line) + 1})
}
```

**4. Serve.** `completion.FeatureCompletionProvider` answers in four languages
by asking each language's query package the same question:

```go
// internal/lsp/completion/feature_completion.go
switch filepath.Ext(params.TextDocument.URI) {
case ".twig":
    matches = params.Node != nil && twigquery.StringInFunction(params.Node, "feature")
case ".scss":
    matches = params.Node != nil && scssquery.StringInFunction(params.Node, "feature")
case ".php":
    matches = params.Node != nil && phpquery.StringInCall(params.Node, 0, "Feature::isActive")
case ".js", ".ts":
    matches = params.Node != nil && jsquery.StringInCall(params.Node, 0,
        "Feature.isActive", "Shopware.Feature.isActive")
}
if matches {
    features, _ := p.featureIndex.GetAllFeatures()
    // → []protocol.CompletionItem
}
```

`params.Node` is the CST node under the cursor, already resolved by
`SyntaxContext`. The provider does no parsing and no scanning.

**5. Register.** In `internal/app/providers_completion.go`, constructed with
its index and registered on the server. That is the only place the wiring
exists.

The whole feature is ~110 lines of production code across three files, because
every layer it needs already exists.

## Where does my change go?

| I want to… | Go to | Also read |
| --- | --- | --- |
| Support new syntax in an existing language | `internal/parser/<lang>/{lexer,parser,syntax}` | [parser](parser-architecture.md) |
| Add a whole new language | `internal/parser/<lang>/…` + `internal/language/builtin.go` | [parser: adding a language](parser-architecture.md#adding-a-language) |
| Ask a new question about a tree | `internal/parser/<lang>/query` | [parser: querying](parser-architecture.md#querying-nodes) |
| Index a new kind of file or field | the domain package + `internal/app/workspace.go` | [indexing](indexing.md) |
| Add completion / definition / hover / … | `internal/lsp/<feature>` + `internal/app/providers_*.go` | [lsp server](lsp-server.md) |
| Add a diagnostic | `internal/lsp/diagnostics` + `internal/lsp/inspections` + `internal/app/inspections.go` | [diagnostics](diagnostics-pipeline.md) |
| Add a quick fix or refactoring | `internal/rewrite`, `internal/lsp/codeaction`, `internal/lsp/refactor` | [refactoring](refactoring-engine.md) |
| Change PHP type or inference behavior | `internal/php/{types,resolver,inference}` | [php engine](php-semantic-engine.md) |
| Add a setting, domain, or rule | `internal/projectconfig` (+ schema, + gating, + VS Code model) | [reference](reference.md#project-configuration) |
| Add a CLI command | `internal/cli` | — |
| Change the editor client | `vscode-extension/src` | [phpstorm integration](phpstorm-integration.md) |

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
| Parser | `internal/parser/<lang>/parser` | Losslessness (`Root.Text() == source`), recovery on incomplete input, `DebugTree` shape |
| Index | the domain package | Extraction, update, removal, persistence across restart |
| Query | `internal/parser/<lang>/query` | Structural questions, including negative cases |
| Provider / analyzer | `internal/lsp/<feature>` | Domain behavior from a source string |
| Server | `internal/lsp`, `internal/app` | Routing, UTF-16 positions, document lifecycle, lazy fixes |
| Adapter | `internal/cli`, `vscode-extension` | One-based positions, diff previews, MCP shapes |

Conventions:

```go
func TestFeature(t *testing.T) {
    index, err := NewIndex(filepath.Join(t.TempDir(), "cache"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, index.Close()) })
}
```

`require` for prerequisites, `assert` for independent comparisons, `t.TempDir()`
for cache directories, `t.Cleanup` for closing indexes. No network access. Set
`SHOPWARE_LSP_CACHE_DIR` rather than touching a real cache.

Commands:

```bash
go build ./...
go test ./...
go test -race ./internal/...             # required for pooling/lifecycle changes
golangci-lint run
mise run check                            # backend + VS Code validation
GOEXPERIMENT=simd go test ./internal/parser/...
```

Opt-in real-world tests run against a Shopware checkout behind the
`integration` build tag:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
  go test -tags=integration ./internal/app -run '^TestShopwareTrunkIndexing$' -count=1 -v
```

Real-world fixtures move independently of this repository. If an unrelated
assertion fails, confirm it on the baseline revision before changing production
code.

Two coarse regression guards, both intentionally loose:

- `internal/architecture/maintainability_test.go` caps production Go files at
  2,500 lines (generated files exempt).
- `.golangci.yml` rejects new functions with `gocognit` complexity above 65.

Treat both as ceilings that force a conversation about responsibilities, not as
metrics to optimize. Grammar dispatch is legitimately branch-heavy; see
[`maintainability.md`](maintainability.md).

## Performance guardrails

The target workload is a full Shopware monorepo — hundreds of thousands of
files. The costs that matter, and where they are controlled:

| Cost | Controlled by |
| --- | --- |
| Cold index wall time | Scanner worker fan-out, `PreparingIndexer` moving work out of the write transaction, cheap path/content rejection before parsing |
| Cold index peak RSS | Bounded parser/CST slab pools, `ReleaseTransientBuffers` at batch boundaries, `debug.FreeOSMemory()` after large batches |
| Warm start time | Persisted compact index values, `VisitAllValues` streaming instead of materializing catalogs, FTS symbol catalog read directly |
| Disk size | MessagePack-encoded values, per-path rows, index-version invalidation instead of migrations |
| Request latency | Indexes answer lookups; documents are parsed once per snapshot; `MemoizedAnalysis` shares derived analysis across features |

Measurement entry points:

```bash
go test -bench=BenchmarkNativeParsers ./internal/parser
go test ./internal/php -run '^$' -bench . -benchmem
SHOPWARE_LSP_TRACE_DIAGNOSTICS=1 shopware-lsp
shopware-lsp -root /path/to/project stats
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
