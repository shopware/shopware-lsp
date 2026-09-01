# Indexing and Persistence

This document explains how Shopware Language Server discovers files, decides
which ones changed, hands them to indexers, and persists what they extract —
plus how to write an indexer that behaves well on a Shopware monorepo.

Everything here lives in [`internal/indexer`](../internal/indexer) and the
domain packages that use it. For the shape of the whole system, see
[`architecture.md`](architecture.md).

## Table of contents

- [Why there is an index at all](#why-there-is-an-index-at-all)
- [Storage layout](#storage-layout)
- [The Indexer interface](#the-indexer-interface)
- [ParsedFile: the shared input](#parsedfile-the-shared-input)
- [Discovery](#discovery)
- [Change detection](#change-detection)
- [The indexing run](#the-indexing-run)
- [Transactions and the single-writer gate](#transactions-and-the-single-writer-gate)
- [Repositories: DataIndexer](#repositories-dataindexer)
- [Deletion and clearing](#deletion-and-clearing)
- [Batch publication](#batch-publication)
- [Workspace symbols](#workspace-symbols)
- [Cache versioning](#cache-versioning)
- [The filesystem watcher](#the-filesystem-watcher)
- [PHAR archives](#phar-archives)
- [Open-document overlays](#open-document-overlays)
- [Writing an indexer](#writing-an-indexer)
- [Performance rules](#performance-rules)
- [Debugging and testing](#debugging-and-testing)

## Why there is an index at all

A request must never scan the workspace. On a real Shopware checkout — hundreds
of thousands of files across the platform, vendor dependencies, plugins, and
Administration sources — walking the filesystem to answer "which service has id
`product.repository`" would take seconds. Every cross-file question is therefore
answered from a persistent index that is built once and kept current
incrementally.

The index is a **cache, not a source of truth**. It can be deleted at any time
and rebuilt from source. That is why cache invalidation is a version bump
(delete everything, reindex) rather than a schema migration: correctness is
cheap to restore, and migration code for 160-plus historical shapes would not
be.

## Storage layout

```
$SHOPWARE_LSP_CACHE_DIR (or ~/.cache)/shopware-lsp/<escaped workspace root>/
├── indexes.db                  the single namespaced store (SQLite, WAL)
├── file_scanner.db             file_hashes: path → size, mtime
├── index_version               integer; compared against indexer.IndexVersion
├── configuration_fingerprint   structural configuration hash
└── phar/                       extracted PHAR sources
```

The workspace root is escaped by replacing `/`, `:`, and `\` with `_`
(`internal/app/cache.go`), so each root gets its own directory.

`indexes.db` holds one wide table plus the workspace-symbol catalog:

```sql
CREATE TABLE data (
    namespace TEXT NOT NULL,
    file_path TEXT NOT NULL,
    key       TEXT NOT NULL,
    value     BLOB NOT NULL
);
CREATE INDEX idx_data_namespace_key  ON data(namespace, key);
CREATE INDEX idx_data_namespace_path ON data(namespace, file_path);
```

Every repository is a `(namespace, key) → value` map where each row remembers
the `file_path` it came from. That single design decision buys three
properties:

- **Lookup by key** is one indexed query, regardless of how many namespaces
  exist.
- **File replacement** is `DELETE WHERE namespace = ? AND file_path = ?`
  followed by inserts — a changed file cannot leave stale rows behind.
- **Adding a repository** needs no DDL. A new domain picks a namespace string
  and starts writing.

Values are MessagePack-encoded Go structs, with a pooled encoder
(`acquireMessagePackEncoder`) to keep cold-index allocation bounded.

Pragmas set at open time (`internal/indexer/store.go`):

```
journal_mode=WAL          concurrent readers during a write
busy_timeout=5000
synchronous=NORMAL
cache_size=-8192          8 MiB native page cache, bounded
foreign_keys=ON
auto_vacuum=INCREMENTAL
wal_autocheckpoint=1000
```

The connection pool is capped at 4. Only one *writer* ever runs (see the
[mutation gate](#transactions-and-the-single-writer-gate)); the extra
connections exist because index preparation may legitimately read an
already-committed repository — Symfony service types, say — while the same
goroutine holds the current write transaction.

`Close` runs `PRAGMA optimize`, `incremental_vacuum`, and
`wal_checkpoint(TRUNCATE)` so a clean shutdown leaves a compact database.

## The Indexer interface

```go
// internal/indexer/indexer.go
type Indexer interface {
    ID() string                          // stable, e.g. "feature.indexer"
    Index(file *ParsedFile) error
    RemovedFiles(paths []string) error
    Close() error
    Clear() error
}
```

`ID()` is a contract: it is used for error messages, for
`domainForIndexer` configuration routing, and in tests. Do not rename it
casually.

Six optional interfaces let an indexer participate in more of the lifecycle:

```go
// Move CPU- and allocation-heavy read-only work ahead of the write
// transaction. Prepare may run concurrently for different files.
type PreparingIndexer interface {
    Prepare(file *ParsedFile) (any, error)
    IndexPrepared(file *ParsedFile, prepared any) error
}

// Opt into files outside the syntax registry, and selectively reopen
// otherwise skipped directories (public assets, for example).
type SupplementalPathIndexer interface {
    ShouldEnterDirectory(path string) bool
    ShouldIndexPath(path string) bool
}

// A supplemental indexer that also wants the normal syntax preparation.
type SupplementalSyntaxIndexer interface {
    ShouldPreparsePath(path string) bool
}

// Participate in the coordinated cross-repository transaction.
type TransactionalRemover interface {
    RemovedFilesIn(paths []string, mutation *Mutation) error
}
type TransactionalClearer interface {
    ClearIn(mutation *Mutation) error
}

// Accumulate committed updates and publish one workspace generation
// when a coordinated run finishes.
type BatchIndexer interface {
    BeginIndexingBatch(candidateFiles []string)
    EndIndexingBatch() error
}
```

Plus `WorkspaceSymbolContributor` for declarations (see
[workspace symbols](#workspace-symbols)).

The `Prepare` / `IndexPrepared` split is the single most important performance
lever available to an indexer. Preparation runs concurrently across files and
outside the write transaction; `IndexPrepared` runs while the transaction is
open and one writer at a time. Anything expensive that happens in
`IndexPrepared` serializes the entire cold index.

## ParsedFile: the shared input

```go
// internal/indexer/parsed_file.go
type ParsedFile struct {
    Path    string
    Content []byte
    Source  string
    // ...
}

func (f *ParsedFile) Extension() string
func (f *ParsedFile) Language() language.ID
func (f *ParsedFile) SyntaxTree() *cst.Tree            // sync.Once
func (f *ParsedFile) LineIndex() *cst.LineIndex        // sync.Once
func (f *ParsedFile) Memoized(key any, compute func() any) any
func (f *ParsedFile) Mutation() *Mutation
func (f *ParsedFile) AddWorkspaceSymbols(symbols ...WorkspaceSymbol)
```

One `ParsedFile` is shared by every indexer handling that file, which is what
makes twenty indexers cost one parse. Details worth knowing:

- `Source` aliases the `Content` byte slice via `unsafe.String` rather than
  copying. `ParsedFile` owns immutable input for its lifetime, so this is safe —
  but it also means nothing may mutate `Content`.
- `SyntaxTree()` and `LineIndex()` are lazy and `sync.Once`-guarded. A file no
  indexer parses is never parsed. An unsupported extension returns `nil` — every
  indexer must handle that.
- `Memoized(key, compute)` extends the same sharing to derived analysis:
  concurrent indexers asking for the same key block on one computation. Keys
  must be comparable; a component pointer is the safest choice because it also
  isolates values across workspace instances.
- `Mutation()` is the coordinated write transaction assigned by the scanner. It
  is `nil` when an indexer is used standalone (unit tests), and every
  repository call has a `…In(mutation, …)` variant that accepts `nil` and falls
  back to standalone behavior.

Lifecycle hooks the scanner drives: `clearMemoized()` at the
preparation/persistence boundary, and `releaseSyntaxStorage()` once prepared
values are committed — which returns the tree's element slabs to the CST pools.

## Discovery

`FileScanner.discoverFiles` walks the root with `fastwalk` using the same worker
count as indexing. Two filters apply.

**Directories** — `shouldEnterDirectory` consults `defaultSkipDirs`:

```
node_modules  var  vendor-bin  bin  cache  dist  .tmp  .git  .github
.gitlab  .claude  .codex  .continue  .cursor  .delta  .windsurf  .run
.idea  .vscode  .fleet  .zed  tests  public  .ddev  .devbox  .devenv  .direnv
```

with one important refinement: inside a `vendor` tree, the entries `cache`,
`bin`, `dist`, `public`, and `var` are *not* skipped. Composer package names and
source trees legitimately contain directories like `symfony/cache`, and skipping
them would hide dependency source. Application-level cache directories stay
excluded.

A `SupplementalPathIndexer` can reopen a skipped directory — that is how public
assets get indexed without every indexer walking generated media. Explicit
workspace patterns from `indexing.exclude` are applied first and cannot be
reopened by a supplemental indexer.

**Files** — `IsScannedPath` accepts anything the language registry recognizes,
with two deliberate exclusions:

- `*.phar.php` — a PHAR stub, handled by the archive path instead.
- `*.html` — plain HTML uses the Twig frontend for open-document features, but
  large generated frontend trees contain thousands of HTML artifacts. They stay
  available on demand without joining the persistent scan.

`ShouldSkipRelativePath`, `PathExclusions`, and `IsScannedPath` are exported so
CLI commands can discover exactly the same file set as the scanner. Configured
patterns are compiled once, use workspace-relative slash-normalized paths, and
filter initial discovery, direct index requests, watcher events, and PHAR
archives consistently.

Discovered files are sorted. That is not cosmetic: sorted order lets
`scanFileStates` align the discovery result with the ordered SQLite state in one
pass, and it makes indexing order deterministic.

## Change detection

`file_scanner.db` stores `path → (size, mtime)`. `fileNeedsIndexing` compares
`os.Stat` against the stored pair:

```go
if exists && stored.size == info.Size() && stored.mtime == info.ModTime().UnixNano() {
    return false, nil, info, nil     // unchanged: never read the file
}
content, err := os.ReadFile(path)
return true, content, info, err
```

Size plus nanosecond mtime, not a content hash. Hashing every file on every
start would mean reading the entire workspace; stat is a metadata read. The
trade-off is that a modification preserving both size and mtime is missed —
acceptable, because the fallback is an explicit `index -force` or a version bump.

`IndexAll` additionally collects **stale** paths: entries in `file_hashes` that
discovery did not produce. Those files were deleted while the server was not
running, and are removed from every repository before indexing starts. Because
discovery is sorted and the state query is ordered, this costs one pass rather
than a second scan of `file_hashes`.

File states are written **only after** all indexers succeed for a file. A
failed index therefore leaves the file looking unindexed, so the next run
retries it — the index never silently skips a file it failed to process.

## The indexing run

```
IndexAll(ctx)
 ├─ symbols.SetReady(false)          mark the catalog as rebuilding
 ├─ discoverFiles                    fastwalk + filters + sort
 ├─ scanFileStates                   aligned single pass; collect stale paths
 ├─ RemoveFiles(stale)
 ├─ symbols.BeginBulkPopulation      defer FTS maintenance
 ├─ indexFiles(files, storedStates)  ── see below
 ├─ symbols.EndBulkPopulation        build the inverted index once
 └─ symbols.SetReady(true)

indexFiles
 ├─ operationMu                      one scanner operation at a time
 ├─ BeginIndexingBatch(files)        for every BatchIndexer
 ├─ loadStoredStates                 bulk lookup above 1024 files
 ├─ runWorkers
 │    producer: files → channel (cap 100)
 │    N workers, N = min(min(NumCPU,4) or override, len(files)):
 │      for each candidate:
 │        fileNeedsIndexing → skip or read content
 │        accumulate into a preparation batch
 │        flush at 50 files or 128 KiB
 │      processBatch:
 │        prepareBatch    → NewParsedFile, optional pre-parse,
 │                          every PreparingIndexer.Prepare (read-only)
 │        indexWithStore  → one Mutation per batch
 ├─ commitFileStates                 only for fully successful files
 └─ EndIndexingBatch + release pools
```

Some specifics that are easy to miss:

**Batch sizing.** Preparation batches flush at 50 files or 128 KiB of source,
whichever comes first, and a file that would overflow the byte budget flushes
the current batch first. Small batches keep peak memory bounded (all prepared
values for a batch are live at once); large-enough batches amortize the
transaction cost.

**Failure bisection.** If a batch fails to index, the mutation is rolled back
and the batch is **split in half and retried recursively**:

```go
if len(indexErrors) > 0 {
    mutation.Rollback()
    if len(batch) > 1 && run.ctx.Err() == nil {
        middle := len(batch) / 2
        indexBatch(batch[:middle])
        indexBatch(batch[middle:])
        return
    }
    run.recordError(errors.Join(indexErrors...))
}
```

So one pathological file does not cost 49 healthy files their index entries; the
recursion isolates it and everything else commits.

**Cancellation.** The producer, the workers, and `indexBatch` all check
`ctx.Err()`. A cancelled run deliberately skips file-state publication, so
nothing is recorded as indexed that was not fully committed.

**Memory reclamation.** `EndIndexingBatch` releases the parser, CST, and
MessagePack pools; after a run of 8,192 or more files it calls
`debug.FreeOSMemory()`, because cold indexing leaves large slabs dead at exactly
that lifecycle boundary.

**Worker count.** `defaultIndexWorkerCount` is `min(max(NumCPU, 1), 4)` —
capped deliberately, because the bottleneck is the serialized writer, not the
CPU. `SetWorkerCount(n)` overrides it (the CLI's `check -workers` uses the same
idea for diagnostics).

## Transactions and the single-writer gate

```go
type Mutation struct {
    store       *Store
    tx          *sql.Tx
    statements  map[string]*sql.Stmt
    touched     map[mutationCache]struct{}
    afterCommit []func()
    // ...
}
```

`Store.BeginMutation` takes a token from a buffered channel of size one:

```go
select {
case <-ctx.Done():
    return nil, ctx.Err()
case <-s.mutationGate:
}
tx, err := s.db.BeginTx(ctx, nil)
```

Both `Commit` and `Rollback` return the token. So exactly one write transaction
exists at a time, cancellation while waiting is clean, and a leaked mutation
deadlocks loudly instead of corrupting concurrently.

Three services the mutation provides:

- **`Prepare(query)`** returns a transaction-scoped cached statement. Repository
  replacement uses a handful of SQL shapes across every namespace and file in a
  batch; preparing each once removes most of the `database/sql` and SQLite
  statement-construction cost.
- **Cache invalidation.** Repositories register themselves via `addCache`.
  On successful commit — and only then — every touched repository's
  `invalidateMutationCache()` runs.
- **`AfterCommit(callback)`** registers in-memory publication. Callbacks run
  after the SQLite commit *and* after cache invalidation. Rollback discards
  them. This is how an index publishes a new immutable generation without ever
  exposing data that was rolled back.

## Repositories: DataIndexer

```go
type DataIndexer[T any] struct { /* ... */ }

func NewRepository[T any](dbPath, namespace string, stores ...*Store) (*DataIndexer[T], error)
```

`NewRepository` uses the shared store when one is passed and falls back to an
isolated database otherwise — that fallback is what lets unit tests construct a
domain index with nothing but a `t.TempDir()`.

### Writing

| Method | Use |
| --- | --- |
| `SaveItem(filePath, key, item)` | Single value, standalone |
| `BatchSaveItems(map[filePath]map[key]T)` | Replace all values for the given paths, standalone |
| `BatchSaveItemsIn(mutation, items)` | The same inside the coordinated transaction — **the normal indexer path** |

Batch save **deletes existing rows for the given file paths first**, so a
changed file cannot leave stale entries. Always pass the file's own path as a
key in the outer map, even when it produced no values — otherwise a file that
used to contribute rows and now contributes none keeps its old rows.

### Reading

| Method | Semantics |
| --- | --- |
| `GetValues(key)` | Cloned slice for one key |
| `GetValuesView(key)` | **Immutable cached view** — no clone. Callers must not modify it |
| `GetAllValues()` / `GetAllValuesView()` | Whole namespace, cloned / viewed |
| `VisitAllValues(visitor)` | Streams decoded values without retaining them in the repository cache |
| `VisitAllEncodedValues(visitor)` | Streams raw MessagePack, borrowed from `sql.Rows` — no decode, no row copy |
| `CountAllValues()` | Row count, for reserving a final in-memory representation |
| `GetAllKeys()`, `GetAllKeysByPath(path)`, `GetAllFilePaths()` | Key/path enumeration |
| `HasAnyKeyExceptFold(exclusions…)` | Existence check without materializing the key catalog |

The `View` and `Visit` variants exist for a specific reason: cloning a large
catalog for every request, or materializing it during startup restoration, is a
measurable cost on a real workspace. Use `GetValues` when you need an owned
copy; use `GetValuesView` on read-heavy request paths and honor the immutability
contract. Use `VisitAllValues` in startup loaders that immediately project into
a compact representation.

`GetAllFilePaths()` deserves a mention: loading it once lets an indexer skip a
SQLite lookup for every file rejected by a cheap content check.

## Deletion and clearing

Three cases, each with a coordinated variant:

| Situation | Method |
| --- | --- |
| Files deleted from disk | `RemovedFiles(paths)` / `RemovedFilesIn(paths, mutation)` |
| Forced reindex, index-version bump | `Clear()` / `ClearIn(mutation)` |
| A file changed | implicit — `BatchSaveItems*` deletes the path's rows first |

Implement the `…In` variants (`TransactionalRemover`, `TransactionalClearer`)
so removal and clearing are atomic across every namespace. Without them a
crash between two indexers' deletions leaves the store internally inconsistent.

`FileScanner.ClearHashes()` clears the file-state table, which forces every file
to be reindexed on the next run.

## Batch publication

Some indexes cannot publish per file. A component registry or a semantic graph
has to be rebuilt as a whole, and doing that once per file during a cold index
would be quadratic. `BatchIndexer` solves it:

```go
type BatchIndexer interface {
    BeginIndexingBatch(candidateFiles []string)
    EndIndexingBatch() error
}
```

- `BeginIndexingBatch` receives the filtered, sorted candidate list. Inspect it
  for allocation hints only; **do not retain or mutate it.**
- `EndIndexingBatch` is called for **every** successful begin — including when
  the run is cancelled or a file fails. Treat it as a `defer`, not a
  success callback.

The pattern is: accumulate committed file updates during the run, then publish
one new immutable generation in `EndIndexingBatch`.

## Workspace symbols

`workspace/symbol` is served from a dedicated SQLite FTS catalog rather than by
asking every domain index. Declarations are contributed during the same pass
that persists domain data:

```go
type WorkspaceSymbolContributor interface {
    WorkspaceSymbols(file *ParsedFile, prepared any) ([]WorkspaceSymbol, error)
}
```

An indexer that already computed a prepared value returns its declarations from
it — no second parse. An indexer can also push symbols mid-pass with
`file.AddWorkspaceSymbols(...)`. Both sets are persisted in the same file
transaction as the domain data.

Two mechanics matter:

- **Bulk population.** `BeginBulkPopulation` defers FTS maintenance while a cold
  index writes thousands of small transactions; `EndBulkPopulation` builds the
  inverted index once from the completed content table.
- **The ready marker.** `IndexAll` sets ready to `false` before reconciling and
  to `true` only after a fully successful run. Request-time consumers check it
  to refuse destructive previews against a partial or stale index. A warm cache
  must not advertise a complete generation while reconciliation is still
  running.

Ranking is textual relevance first, then symbol-kind priority, so an exact
member match stays useful while equally good type matches sort above members and
framework aliases. Aliases are searchable but are not returned to LSP clients.

`workspace-symbol` in the CLI reads this catalog directly without restoring any
language-specific object graph; `--fresh` forces a full production session
first.

## Cache versioning

```go
// internal/indexer/version.go
const IndexVersion = 167
```

Two independent invalidation checks run at workspace construction:

**`CheckAndMigrateCache(cacheDir)`** compares the stored `index_version` file
against the constant. A mismatch, a missing file, or a corrupt file wipes the
cache directory and rewrites the version. **Bump `IndexVersion` whenever
persisted structure or extraction semantics changes**, and update its test in
the same change. There is no migration path — restoring the cache from source
is cheap, and 167 migrations would not be.

**`CheckAndMigrateConfiguration(cacheDir, fingerprint)`** compares
`configuration.StructuralFingerprint()`. This exists because file hashes are
shared by every indexer: if a domain is disabled, files change, and the domain
is re-enabled, per-file hashes would say "unchanged" and the re-enabled indexer
would never see them. Changing structural configuration therefore invalidates
the whole cache.

`CacheVersionCurrent` is the read-only half, so lightweight CLI commands can
reject a stale catalog without constructing a workspace.

## The filesystem watcher

`FileScanner` is the only source of filesystem index events. Feature packages
must never add their own watcher.

- **Linux / Windows** — `fsnotify` (`watcher.go`).
- **macOS** — native FSEvents (`watcher_darwin.go`). kqueue's
  one-descriptor-per-file cost is prohibitive on a Shopware workspace.

Events are accumulated into pending add/remove sets and processed after a
200 ms debounce (`watcherDebounce`), which coalesces the burst an editor save or
a `git checkout` produces:

```go
if fullScan {
    fs.IndexAll(fs.watcherCtx)     // a changed PHAR invalidates its extraction
    return
}
if len(adds) > 0    { fs.IndexFiles(fs.watcherCtx, adds) }
if len(removes) > 0 { fs.RemoveFiles(fs.watcherCtx, removes) }
```

After a successful run the scanner's `onUpdate` callback fires, which the server
uses to republish diagnostics for all open documents — cross-file diagnostics can
change when an unrelated file is indexed.

The watcher starts only after the initial `IndexAll` completes, and never in CLI
mode.

## PHAR archives

Shopware plugins and tools ship as `.phar` archives. `internal/phar` reads them
without invoking PHP; `internal/indexer/phar_sources.go` extracts their PHP
sources into `<cacheDir>/phar/` and adds them to the discovered file set, so
classes inside an archive participate in normal indexing and navigation.

Because extraction is a derived artifact, a changed archive triggers a **full**
rescan rather than an incremental update — hence the `pendingFullScan` branch in
the watcher.

## Open-document overlays

The index reflects what is on disk. Unsaved editor state reaches an index only
through an explicit, narrow overlay:

```go
server.RegisterDocumentObserver(lsp.DocumentObserver{
    DidOpenOrChange: func(document *lsp.TextDocument) {
        adminIndex.UpdateLiveDocument(path, root, document.Source, document.LineIndex)
    },
    DidClose: func(uri string) {
        adminIndex.RemoveLiveDocument(path)
    },
})
```

Rules for overlays:

- The overlay is separate from the persisted generation. Editor buffers are
  never written into the workspace index.
- Observers are replayed for already-open documents at registration, so
  initialization order does not matter.
- Skip overlay work in CLI mode. `check` runs after the scanner is current;
  publishing an identical overlay per checked file would invalidate caches and
  copy whole registries repeatedly.
- Overlay changes usually need a diagnostics refresh for affected open
  documents — use `Server.RefreshOpenDocumentDiagnostics(match)`, which is
  debounced.

## Writing an indexer

A complete, minimal indexer — feature flags from YAML:

```go
// internal/feature/indexer.go
type FeatureIndexer struct {
    featureIndex *indexer.DataIndexer[Feature]
}

func NewFeatureIndexer(configDir string, stores ...*indexer.Store) (*FeatureIndexer, error) {
    featureIndex, err := indexer.NewRepository[Feature](
        filepath.Join(configDir, "feature_flags.db"), "feature.flags", stores...,
    )
    if err != nil {
        return nil, err
    }
    return &FeatureIndexer{featureIndex: featureIndex}, nil
}

func (i *FeatureIndexer) ID() string { return "feature.indexer" }

func (i *FeatureIndexer) Index(file *indexer.ParsedFile) error {
    path := file.Path
    if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
        return nil
    }
    if !strings.Contains(strings.ToLower(path), "feature") {
        return nil
    }
    features, err := ParseFeatureTree(file.SyntaxTree(), file.LineIndex(), path)
    if err != nil {
        return fmt.Errorf("parsing feature file: %w", err)
    }
    batchSave := map[string]map[string]Feature{path: {}}
    for _, feature := range features {
        if _, ok := batchSave[feature.File]; !ok {
            batchSave[feature.File] = make(map[string]Feature)
        }
        batchSave[feature.File][feature.Name] = feature
    }
    if err := i.featureIndex.BatchSaveItemsIn(file.Mutation(), batchSave); err != nil {
        return fmt.Errorf("saving features: %w", err)
    }
    return nil
}

func (i *FeatureIndexer) RemovedFiles(paths []string) error {
    return i.featureIndex.BatchDeleteByFilePaths(paths)
}
func (i *FeatureIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
    return i.featureIndex.BatchDeleteByFilePathsIn(mutation, paths)
}
func (i *FeatureIndexer) Close() error                             { return i.featureIndex.Close() }
func (i *FeatureIndexer) Clear() error                             { return i.featureIndex.Clear() }
func (i *FeatureIndexer) ClearIn(m *indexer.Mutation) error        { return i.featureIndex.ClearIn(m) }

func (i *FeatureIndexer) GetFeatureByName(name string) ([]Feature, error) {
    return i.featureIndex.GetValues(name)
}
func (i *FeatureIndexer) GetAllFeatures() ([]Feature, error) {
    return i.featureIndex.GetAllValues()
}
```

The checklist this follows, in order:

1. **Reject cheaply, before touching the tree.** Extension, then path
   heuristic, then content check if needed. Most files in a workspace are not
   yours; every one you parse unnecessarily is paid on every cold index.
2. **Take the tree from `ParsedFile`.** Never call a parser directly in an
   indexer — that would parse the file a second time.
3. **Persist through `file.Mutation()`.** Use the `…In` variants everywhere.
4. **Include the file's own path in the batch map**, even with no values, so
   removals take effect.
5. **Wrap errors with context** and `%w`.
6. **Expose plain Go lookups** for providers. No LSP types in a domain package.
7. Implement `PreparingIndexer` when extraction is substantial, and keep
   `IndexPrepared` to persistence plus `AfterCommit` publication.
8. **Sort anything a client can observe.** Repository iteration and Go map
   ordering are nondeterministic; leaking that into LSP responses produces
   flaky tests and jumpy UI.
9. **Register it** in `internal/app/workspace.go`, and map `ID()` to a domain in
   `domainForIndexer` so it can be switched off.
10. **Bump `indexer.IndexVersion`** if persisted structure or extraction
    semantics changed.

## Performance rules

Every new indexed field has four costs. Consider all of them.

| Cost | Question to ask |
| --- | --- |
| Cold index time | Does this parse or analyze files it does not need to? Is the work in `Prepare`, or is it serializing the writer? |
| Warm start time | Is startup restoration streaming (`VisitAllValues`), or materializing a catalog? |
| Disk size | Is the persisted value compact, or a whole AST-shaped struct? |
| Request latency | Is there a targeted key lookup, or does the request load everything and filter? |

Specific rules that come up repeatedly:

- **Do not load a whole catalog into memory when a targeted SQLite lookup
  suffices.** `GetValues(key)` beats `GetAllValues()` plus a filter.
- **Prefer `GetValuesView` on request paths** and honor the no-mutation
  contract.
- **Do not retain CST nodes across document versions.** Persist stable
  identifiers (names, symbol IDs, byte ranges), never pointers.
- **Protect published mutable state** with the existing generation-swap pattern
  and run `go test -race ./internal/...` after any lifecycle or concurrency
  change.

## Debugging and testing

### Inspecting a real workspace

```bash
shopware-lsp -root /path/to/project index          # build or refresh
shopware-lsp -root /path/to/project index -force   # clear hashes and rebuild
shopware-lsp -root /path/to/project stats          # tracked files, cache size, memory
shopware-lsp -root /path/to/project -json stats
```

Persistent indexing skips source files larger than `indexing.maxFileSizeMiB`
(8 MiB by default). Set it to `0` to disable the configurable ceiling. Files
outside the parser's 32-bit source range are always rejected. `stats` reports
the ten largest indexed files plus the count, total size, and largest examples
of skipped oversized files.

The cache is a plain SQLite file, so it is directly inspectable:

```bash
sqlite3 ~/.cache/shopware-lsp/<escaped-root>/indexes.db \
  "SELECT namespace, COUNT(*) FROM data GROUP BY namespace ORDER BY 2 DESC LIMIT 20;"
```

### Unit tests

Domain index tests construct the index against a temp directory, with no store:

```go
func TestFeatureIndexer(t *testing.T) {
    index, err := NewFeatureIndexer(t.TempDir())
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, index.Close()) })

    file := indexer.NewParsedFile("/project/config/feature.yaml", []byte(source))
    require.NoError(t, index.Index(file))

    features, err := index.GetFeatureByName("FEATURE_NEXT_1234")
    require.NoError(t, err)
    require.Len(t, features, 1)
}
```

Cover four behaviors, not just extraction: **extraction**, **update** (index the
same path twice with different content and assert the old values are gone),
**removal** (`RemovedFiles`), and **persistence** (close, reopen, still there).

Always set `SHOPWARE_LSP_CACHE_DIR` or use `t.TempDir()`; never touch a
developer's real workspace cache.

### Scanner and integration tests

`internal/indexer/filescanner_test.go` covers discovery, change detection,
batching, and failure bisection. `internal/app/index_profile_test.go` and the
`integration`-tagged real-world suite measure cold and warm indexing against an
actual Shopware checkout:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
  go test -tags=integration ./internal/app -run '^TestShopwareTrunkIndexing$' -count=1 -v

go test -c -tags=integration ./internal/app -o /tmp/index-profile.test
/usr/bin/time -l /tmp/index-profile.test -test.run '^TestShopwareTrunkIndexingProfile$' -test.count=1 -test.v
```

Use isolated cache directories and reverse the run order when comparing
revisions.

## Further reading

- [`architecture.md`](architecture.md) — the system this fits into
- [`parser-architecture.md`](parser-architecture.md) — the trees indexers read
- [`lsp-server.md`](lsp-server.md) — how providers consume indexes
- [`php-semantic-engine.md`](php-semantic-engine.md) — the largest index, with
  its own persisted graph
- [`AGENTS.md`](../AGENTS.md#indexing-and-persistence) — the contributor
  checklist
