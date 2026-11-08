# Indexing and Persistence

This document explains how Shopware Language Server discovers files, decides
which ones changed, hands them to indexers, and persists what they extract —
plus how to write an indexer that behaves well on a Shopware monorepo.

Everything here lives in [`crates/indexer`](../crates/indexer) (`swlsp-indexer`)
and the domain crates that use it. For the shape of the whole system, see
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
cheap to restore, and migration code for 180 historical shapes would not
be.

## Storage layout

```
$SHOPWARE_LSP_CACHE_DIR (or the user cache dir)/shopware-lsp/<sha256 of root>/
├── indexes.db                  the single namespaced store (SQLite, WAL)
├── file_scanner.db             file_hashes: path → size, mtime
├── index_version               "<INDEX_VERSION>-rs"; compared against swlsp_indexer::INDEX_VERSION
├── configuration_fingerprint   structural configuration hash
└── phar/                       extracted PHAR sources
```

The directory name is the SHA-256 of the normalized absolute workspace root
(`crates/app/src/cache.rs`), so each root gets its own directory without
aliasing or path-length problems.

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

Values are serde types encoded as MessagePack with field names
(`rmp_serde::encode::write_named`, so `#[serde(default)]` fields stay
compatible), through a per-thread reusable buffer to keep cold-index
allocation bounded.

Pragmas set at open time (`crates/indexer/src/store.rs`):

```
journal_mode=WAL          concurrent readers during a write
busy_timeout=5000
synchronous=NORMAL
cache_size=-8192          8 MiB native page cache, bounded
foreign_keys=ON
auto_vacuum=INCREMENTAL
wal_autocheckpoint=1000
```

Exactly one writer connection exists (see the
[mutation gate](#transactions-and-the-single-writer-gate)). Reads use a small
pool of separate WAL reader connections (`query_only`, up to four kept idle),
so a read never waits for the writer and always observes committed data —
index preparation may legitimately read an already-committed repository
(Symfony service types, say) while another thread holds the write
transaction.

The writer disables SQLite's inline auto-checkpoint; a background
`Checkpointer` thread runs `PASSIVE` checkpoints shortly after commits, so a
commit never stalls every indexing worker on `fsync`.

`Store::close` runs `PRAGMA optimize`, `incremental_vacuum`, and
`wal_checkpoint(TRUNCATE)` so a clean shutdown leaves a compact database.

## The Indexer interface

```rust
// crates/indexer/src/indexer.rs
pub trait Indexer: Send + Sync {
    fn id(&self) -> &str;                                  // stable, e.g. "feature.indexer"
    fn index(&self, file: &ParsedFile) -> Result<()>;
    fn removed_files(&self, paths: &[String]) -> Result<()>;
    fn close(&self) -> Result<()> { Ok(()) }
    fn clear(&self) -> Result<()>;

    // Optional capabilities: override to return Some(self).
    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> { None }
    fn as_supplemental_path(&self) -> Option<&dyn SupplementalPathIndexer> { None }
    fn as_supplemental_syntax(&self) -> Option<&dyn SupplementalSyntaxIndexer> { None }
    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> { None }
    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> { None }
    fn as_batch(&self) -> Option<&dyn BatchIndexer> { None }
    fn as_workspace_symbol_contributor(&self) -> Option<&dyn WorkspaceSymbolContributor> { None }
}
```

`id()` is a contract: it is used for error messages, for
`domain_for_indexer` configuration routing, and in tests. Do not rename it
casually.

The optional capability traits let an indexer participate in more of the
lifecycle:

```rust
/// Move CPU- and allocation-heavy read-only work ahead of the write
/// transaction. `prepare` may run concurrently for different files.
pub trait PreparingIndexer: Send + Sync {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>>;
    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()>;
}

/// Opt into files outside the syntax registry, and selectively reopen
/// otherwise skipped directories (public assets, for example).
pub trait SupplementalPathIndexer: Send + Sync {
    fn should_enter_directory(&self, path: &str) -> bool;
    fn should_index_path(&self, path: &str) -> bool;
}

/// A supplemental indexer that also wants the normal syntax preparation.
pub trait SupplementalSyntaxIndexer: Send + Sync {
    fn should_preparse_path(&self, path: &str) -> bool;
}

/// Participate in the coordinated cross-repository transaction.
pub trait TransactionalRemover: Send + Sync {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()>;
}
pub trait TransactionalClearer: Send + Sync {
    fn clear_in(&self, mutation: &Mutation) -> Result<()>;
}

/// Accumulate committed updates and publish one workspace generation
/// when a coordinated run finishes.
pub trait BatchIndexer: Send + Sync {
    fn begin_indexing_batch(&self, candidate_files: &[String]);
    fn end_indexing_batch(&self) -> Result<()>;
}
```

`Prepared` is `Box<dyn Any + Send + Sync>`. `WorkspaceSymbolContributor`
publishes declarations (see [workspace symbols](#workspace-symbols)).

The `prepare` / `index_prepared` split is the single most important
performance lever available to an indexer. Preparation runs concurrently
across files and outside the write transaction; `index_prepared` runs while the
transaction is open and one writer at a time. Anything expensive that happens
in `index_prepared` serializes the entire cold index.

## ParsedFile: the shared input

```rust
// crates/indexer/src/parsed_file.rs
impl ParsedFile {
    pub fn new(path: impl Into<String>, content: Vec<u8>) -> Self;
    pub fn path(&self) -> &str;
    pub fn source(&self) -> &str;
    pub fn content(&self) -> &[u8];
    pub fn extension(&self) -> &str;
    pub fn language(&self) -> Option<LanguageId>;
    pub fn syntax_tree(&self) -> Option<&Tree>;        // lazy, at most once
    pub fn line_index(&self) -> &LineIndex;            // lazy, at most once
    pub fn memoized<T: Any + Send + Sync>(&self, key: usize, compute: impl FnOnce() -> T) -> Arc<T>;
    pub fn mutation(&self) -> Option<&Mutation>;
    pub fn add_workspace_symbols(&self, symbols: impl IntoIterator<Item = WorkspaceSymbol>);
}
```

One `ParsedFile` is shared by every indexer handling that file, which is what
makes twenty indexers cost one parse. Details worth knowing:

- `new` takes ownership of the content and reuses the allocation as the source
  when it is valid UTF-8; other files are decoded lossily, and byte offsets
  refer to the decoded text.
- `syntax_tree()` and `line_index()` are lazy and built at most once. A file no
  indexer parses is never parsed. An unsupported extension returns `None` —
  every indexer must handle that.
- `memoized(key, compute)` extends the same sharing to derived analysis:
  concurrent indexers asking for the same `(type, key)` share one computation.
  A component's address is the safest key because it also isolates values
  across workspace instances.
- `mutation()` is the coordinated write transaction assigned by the scanner. It
  is `None` when an indexer is used standalone (unit tests), and every
  repository write has a `…_in(mutation, …)` variant that accepts `None` and
  falls back to a standalone transaction.

The scanner clears memoized values at the preparation/persistence boundary;
the file, its tree, and its prepared values are dropped once the batch is
committed.

## Discovery

`discover_files` walks the root with the `ignore` crate's parallel walker (no
gitignore filtering, no symlink following) using the same worker count as
indexing. Two filters apply.

**Directories** — `should_enter_directory` consults `DEFAULT_SKIP_DIRS`:

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

**Files** — `is_scanned_path` accepts anything the language registry recognizes,
with two deliberate exclusions:

- `*.phar.php` — a PHAR stub, handled by the archive path instead.
- `*.html` — plain HTML uses the Twig frontend for open-document features, but
  large generated frontend trees contain thousands of HTML artifacts. They stay
  available on demand without joining the persistent scan.

`should_skip_relative_path`, `PathExclusions`, and `is_scanned_path` are exported so
CLI commands can discover exactly the same file set as the scanner. Configured
patterns are compiled once, use workspace-relative slash-normalized paths, and
filter initial discovery, direct index requests, watcher events, and PHAR
archives consistently.

Discovered files are sorted. That is not cosmetic: sorted order lets
`scan_file_states` align the discovery result with the ordered SQLite state in one
pass, and it makes indexing order deterministic.

## Change detection

`file_scanner.db` stores `path → (size, mtime)`. The scanner's `file_change`
compares the file's metadata against the stored pair:

```rust
// crates/indexer/src/scanner_index.rs
let metadata = std::fs::metadata(path)?;
let meta = FileMeta::of(&metadata);
// ... size-limit checks ...
if stored == Some(meta) {
    return Ok(Change::Unchanged); // never read the file
}
let content = std::fs::read(path)?;
```

Size plus nanosecond mtime, not a content hash. Hashing every file on every
start would mean reading the entire workspace; stat is a metadata read. The
trade-off is that a modification preserving both size and mtime is missed —
acceptable, because the fallback is an explicit `index --force` or a version bump.

`index_all` additionally collects **stale** paths: entries in `file_hashes` that
discovery did not produce. Those files were deleted while the server was not
running, and are removed from every repository before indexing starts. Because
discovery is sorted and the state query is ordered, this costs one pass rather
than a second scan of `file_hashes`.

File states are written **only after** all indexers succeed for a file and its
batch has committed. A failed index therefore leaves the file looking unindexed, so the next run
retries it — the index never silently skips a file it failed to process.

## The indexing run

```
index_all(cancel)
 ├─ symbols.set_ready(false)           mark the catalog as rebuilding
 ├─ discover_files                     parallel walk + filters + sort
 ├─ scan_file_states                   aligned single pass; collect stale paths
 ├─ remove_files(stale)
 ├─ symbols.begin_bulk_population      defer FTS maintenance
 ├─ index_files(files, stored_states)  ── see below
 ├─ symbols.end_bulk_population        build the inverted index once
 └─ symbols.set_ready(true)

index_files
 ├─ operation lock                     one scanner operation at a time
 ├─ begin_indexing_batch(files)        for every BatchIndexer
 ├─ load_file_states                   bulk lookup at 1024 files or more
 ├─ run_workers (std::thread::scope)
 │    producer: files → crossbeam channel (cap 100)
 │    N workers, N = min(clamp(CPUs, 1, 4) or override, len(files)):
 │      for each candidate:
 │        file_change → skip or read content
 │        accumulate into a preparation batch
 │        flush at 50 files or 128 KiB
 │      process_batch:
 │        prepare_batch    → ParsedFile::new, optional pre-parse,
 │                           every PreparingIndexer::prepare (read-only)
 │        index_with_store → one Mutation per batch
 ├─ publish file states                only for committed, successful files
 └─ end_indexing_batch (reverse order) + release encode buffers
```

Some specifics that are easy to miss:

**Batch sizing.** Preparation batches flush at 50 files or 128 KiB of source,
whichever comes first, and a file that would overflow the byte budget flushes
the current batch first. Small batches keep peak memory bounded (all prepared
values for a batch are live at once); large-enough batches amortize the
transaction cost.

**Failure bisection.** If a batch fails to index, the mutation is rolled back
and the batch is **split in half and retried recursively**:

```rust
// crates/indexer/src/scanner_index.rs
if !errors.is_empty() {
    if let Err(error) = mutation.rollback() {
        errors.push(error);
    }
    if batch.len() > 1 && !self.cancel.is_cancelled() {
        let middle = batch.len() / 2;
        let (left, right) = batch.split_at_mut(middle);
        self.index_with_store(left, successful);
        self.index_with_store(right, successful);
        return;
    }
    self.record_error(Error::join(errors).expect("errors are not empty"));
    return;
}
```

So one pathological file does not cost 49 healthy files their index entries; the
recursion isolates it and everything else commits.

**Cancellation.** The producer, the workers, and `index_with_store` all check
the `CancelToken`. A cancelled batch is rolled back. File states are still
published on the cancelled path, but only for batches whose mutation already
committed, so nothing is recorded as indexed that was not fully committed.

**Memory reclamation.** Prepared values and parse trees live only for their
batch. After the run, `index_files` drops the per-thread MessagePack encode
buffers.

**Worker count.** `default_index_worker_count` is `clamp(CPUs, 1, 4)` —
capped deliberately, because the bottleneck is the serialized writer, not the
CPU. `set_worker_count(n)` overrides it (the CLI's `check --workers` uses the
same idea for diagnostics).

## Transactions and the single-writer gate

`Store::begin_mutation(cancel)` takes the single writer connection out of the
store, waiting (and re-checking cancellation) while another mutation holds it,
then runs `BEGIN IMMEDIATE`:

```rust
// crates/indexer/src/store.rs
let mut slot = lock(&self.inner.writer);
loop {
    cancel.check()?;
    if !slot.busy {
        break;
    }
    if slot.owner == Some(std::thread::current().id()) {
        return Err(Error::msg("index mutation is already active on this thread"));
    }
    slot = self.inner.writer_released.wait_timeout(slot, Duration::from_millis(10))/* ... */;
}
```

`commit`, `rollback`, and dropping the `Mutation` hand the connection back. So
exactly one write transaction exists at a time, cancellation while waiting is
clean, and a nested mutation on the same thread fails instead of deadlocking.

Three services the mutation provides:

- **Cached statements.** `MutationConnection::prepare_cached` reuses prepared
  statements (64 per connection). Repository replacement uses a handful of SQL
  shapes across every namespace and file in a batch, so they stay prepared
  across batches.
- **Cache invalidation.** Repositories touched by the mutation implement
  `MutationCache`. On successful commit — and only then — every touched
  repository's `invalidate_mutation_cache()` runs.
- **`after_commit(callback)`** registers in-memory publication. Callbacks run
  after the SQLite commit *and* after cache invalidation. Rollback discards
  them. This is how an index publishes a new immutable generation without ever
  exposing data that was rolled back.

## Repositories: DataIndexer

```rust
// crates/indexer/src/dataindex.rs
pub struct DataIndexer<T> { /* ... */ }

impl<T: Serialize + DeserializeOwned + ...> DataIndexer<T> {
    pub fn repository(db_path: impl AsRef<Path>, namespace: &str, store: Option<&Store>) -> Result<Self>;
}
```

`DataIndexer::repository` uses the shared store when one is passed. Without
one, every repository under the same directory shares one standalone store —
that fallback is what lets unit tests construct a domain index with nothing but
a `tempfile::tempdir()`.

### Writing

| Method | Use |
| --- | --- |
| `save_item(file_path, key, &item)` | Single value, standalone |
| `batch_save_items(items)` | Replace all values for the given paths, standalone |
| `batch_save_items_in(mutation, items)` | The same inside the coordinated transaction — **the normal indexer path** |

`items` is an iterator of `(file_path, iterator of (key, value))`. Batch save
**deletes existing rows for the given file paths first**, so a changed file
cannot leave stale entries. Always pass the file's own path, even when it
produced no values — otherwise a file that used to contribute rows and now
contributes none keeps its old rows. A path must appear at most once.

### Reading

| Method | Semantics |
| --- | --- |
| `get_values(key)` | Owned `Vec<T>` for one key |
| `get_values_view(key)` | **Shared cached view** (`Arc<[T]>`) — no clone |
| `get_all_values()` / `get_all_values_view()` | Whole namespace, owned / viewed |
| `get_values_by_path(path)` / `get_values_by_path_view(path)` | Everything one file contributed |
| `visit_all_values(visitor)` | Streams decoded values without retaining them in the repository cache |
| `visit_all_encoded_values(visitor)` | Streams raw MessagePack — no decode, no row copy |
| `count_all_values()` | Row count, for reserving a final in-memory representation |
| `get_all_keys()`, `get_all_keys_by_path(path)`, `get_all_file_paths()` | Key/path enumeration |
| `has_any_key_except_fold(exclusions)` | Existence check without materializing the key catalog |

The `_view` and `visit_` variants exist for a specific reason: cloning a large
catalog for every request, or materializing it during startup restoration, is a
measurable cost on a real workspace. Use `get_values` when you need an owned
copy; use `get_values_view` on read-heavy request paths. Use
`visit_all_values` in startup loaders that immediately project into a compact
representation.

`get_all_file_paths()` deserves a mention: loading it once lets an indexer skip a
SQLite lookup for every file rejected by a cheap content check.

## Deletion and clearing

Three cases, each with a coordinated variant:

| Situation | Method |
| --- | --- |
| Files deleted from disk | `removed_files(paths)` / `removed_files_in(paths, mutation)` |
| Forced reindex, index-version bump | `clear()` / `clear_in(mutation)` |
| A file changed | implicit — `batch_save_items*` deletes the path's rows first |

Implement the `…_in` variants (`TransactionalRemover`, `TransactionalClearer`)
so removal and clearing are atomic across every namespace. Without them a
crash between two indexers' deletions leaves the store internally inconsistent.

`FileScanner::clear_hashes()` clears the file-state table, which forces every file
to be reindexed on the next run.

## Batch publication

Some indexes cannot publish per file. A component registry or a semantic graph
has to be rebuilt as a whole, and doing that once per file during a cold index
would be quadratic. `BatchIndexer` solves it:

```rust
pub trait BatchIndexer: Send + Sync {
    fn begin_indexing_batch(&self, candidate_files: &[String]);
    fn end_indexing_batch(&self) -> Result<()>;
}
```

- `begin_indexing_batch` receives the filtered, sorted candidate list. Inspect
  it for allocation hints only.
- `end_indexing_batch` is called for **every** begin — including when the run
  is cancelled or a file fails. Treat it as a guard, not a success callback.

The pattern is: accumulate committed file updates during the run, then publish
one new immutable generation in `end_indexing_batch`.

## Workspace symbols

`workspace/symbol` is served from a dedicated SQLite FTS catalog rather than by
asking every domain index. Declarations are contributed during the same pass
that persists domain data:

```rust
pub trait WorkspaceSymbolContributor: Send + Sync {
    fn workspace_symbols(&self, file: &ParsedFile, prepared: Option<&Prepared>)
        -> Result<Vec<WorkspaceSymbol>>;
}
```

An indexer that already computed a prepared value returns its declarations from
it — no second parse. An indexer can also push symbols mid-pass with
`file.add_workspace_symbols(...)`. Both sets are persisted in the same file
transaction as the domain data.

Two mechanics matter:

- **Bulk population.** `begin_bulk_population` defers FTS maintenance while a
  cold index writes thousands of small transactions; `end_bulk_population`
  builds the inverted index once from the completed content table.
- **The ready marker.** `index_all` sets ready to `false` before reconciling and
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

```rust
// crates/indexer/src/version.rs
pub const INDEX_VERSION: u32 = 180;
```

The version file stores `<INDEX_VERSION>-rs` (`index_version_marker()`); the
suffix marks the Rust value encoding, so a cache written by the earlier Go
implementation is rebuilt rather than misread.

Two independent invalidation checks run at workspace construction:

**`check_and_migrate_cache(cache_dir)`** compares the stored `index_version`
file against the marker. A mismatch, a missing file, or a corrupt file wipes
the cache directory and rewrites the version. **Bump `INDEX_VERSION` whenever
persisted structure or extraction semantics changes**, and update its test in
the same change. There is no migration path — restoring the cache from source
is cheap, and 180 migrations would not be.

**`check_and_migrate_configuration(cache_dir, fingerprint)`** compares the
configuration's `structural_fingerprint()`. This exists because file hashes are
shared by every indexer: if a domain is disabled, files change, and the domain
is re-enabled, per-file hashes would say "unchanged" and the re-enabled indexer
would never see them. Changing structural configuration therefore invalidates
the whole cache.

`cache_version_current` is the read-only half, so lightweight CLI commands can
reject a stale catalog without constructing a workspace.

## The filesystem watcher

`FileScanner` is the only source of filesystem index events. Feature crates
must never add their own watcher. The watcher (`crates/indexer/src/watcher.rs`)
uses the `notify` crate:

- **Linux and other inotify/kqueue platforms** watch each entered directory
  non-recursively, so skipped trees such as `node_modules` never consume watch
  descriptors.
- **macOS (FSEvents) and Windows** watch the root recursively and filter events
  by path. The watcher resolves the physical root once and maps canonical event
  paths back to the configured workspace path, so symlinked roots and `/var`
  aliases update the same indexed records, including on deletion.

Events are accumulated into pending add/remove sets and processed after a
200 ms debounce (`WATCHER_DEBOUNCE`), which coalesces the burst an editor save
or a `git checkout` produces. A changed PHAR archive or a dropped-event
notification triggers a full `index_all` reconciliation; otherwise the pending
sets become one `index_files` and one `remove_files` call.

After a successful run the scanner's `on_update` callback fires, which the server
uses to republish diagnostics for all open documents — cross-file diagnostics can
change when an unrelated file is indexed.

The watcher starts only after the initial `index_all` completes. CLI mode leaves it
off unless the client sets `initializationOptions.watchFiles`, which the
long-lived `shopware-lsp mcp` server does and one-shot commands do not.

## PHAR archives

Shopware plugins and tools ship as `.phar` archives. `swlsp-phar` reads them
without invoking PHP; `crates/indexer/src/phar_sources.rs` extracts their PHP
sources into `<cacheDir>/phar/` and adds them to the discovered file set, so
classes inside an archive participate in normal indexing and navigation.

Because extraction is a derived artifact, a changed archive triggers a **full**
rescan rather than an incremental update — hence the full-scan branch in the
watcher.

## Open-document overlays

The index reflects what is on disk. Unsaved editor state reaches an index only
through an explicit, narrow overlay:

```rust
server.register_document_observer(DocumentObserver {
    did_open_or_change: Some(Arc::new(move |document: &Arc<TextDocument>| {
        // e.g. admin index: update_live_document(path, root, source, line index)
    })),
    did_close: Some(Arc::new(move |uri: &str| {
        // e.g. admin index: remove_live_document(path)
    })),
});
```

Rules for overlays:

- The overlay is separate from the persisted generation. Editor buffers are
  never written into the workspace index.
- Observers are replayed for already-open documents at registration, so
  initialization order does not matter.
- Skip overlay work in CLI mode. `check` runs after the scanner is current;
  publishing an identical overlay per checked file would invalidate caches and
  copy whole registries repeatedly.
- Skip overlay work when the open text equals the file the index was built
  from (`FileScanner::indexed_content_matches`); the Administration observer
  does this.
- Overlay changes usually need a diagnostics refresh for affected open
  documents — use `Server::refresh_open_document_diagnostics(matches)`, which
  is debounced.

## Writing an indexer

A complete, minimal indexer — feature flags from YAML
(`crates/feature/src/indexer.rs`):

```rust
pub struct FeatureIndexer {
    features: DataIndexer<Feature>,
}

impl FeatureIndexer {
    /// Uses the shared workspace `store`, or an isolated database in
    /// `config_dir` without one.
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let features = DataIndexer::repository(
            config_dir.as_ref().join("feature_flags.db"),
            "feature.flags",
            store,
        )?;
        Ok(Self { features })
    }

    pub fn feature_by_name(&self, name: &str) -> Result<Arc<[Feature]>> {
        self.features.get_values_view(name)
    }

    pub fn all_features(&self) -> Result<Arc<[Feature]>> {
        self.features.get_all_values_view()
    }
}

impl Indexer for FeatureIndexer {
    fn id(&self) -> &str {
        "feature.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let path = file.path();
        if !path.ends_with(".yaml") && !path.ends_with(".yml") {
            return Ok(());
        }
        if !path.to_lowercase().contains("feature") {
            return Ok(());
        }
        let features = match file.syntax_tree() {
            Some(tree) => parse_feature_tree(tree, file.line_index(), path),
            None => Err(MissingFeatures { file: path.to_owned() }),
        }
        .map_err(|error| Error::other(error).context("parsing feature file"))?;
        // Later duplicates replace earlier ones.
        let mut items: Vec<(&str, &Feature)> = Vec::with_capacity(features.len());
        for feature in &features {
            match items.iter_mut().find(|(name, _)| *name == feature.name) {
                Some(existing) => existing.1 = feature,
                None => items.push((&feature.name, feature)),
            }
        }
        self.features
            .batch_save_items_in(file.mutation(), [(path, items)])
            .context("saving features")
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.features.batch_delete_by_file_paths(paths).context("removing features")
    }

    fn close(&self) -> Result<()> {
        self.features.close()
    }

    fn clear(&self) -> Result<()> {
        self.features.clear()
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl TransactionalRemover for FeatureIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.features.batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for FeatureIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.features.clear_in(Some(mutation))
    }
}
```

The checklist this follows, in order:

1. **Reject cheaply, before touching the tree.** Extension, then path
   heuristic, then content check if needed. Most files in a workspace are not
   yours; every one you parse unnecessarily is paid on every cold index.
2. **Take the tree from `ParsedFile`.** Never call a parser directly in an
   indexer — that would parse the file a second time.
3. **Persist through `file.mutation()`.** Use the `…_in` variants everywhere.
4. **Include the file's own path in the batch**, even with no values, so
   removals take effect.
5. **Add context to errors** (`ResultExt::context`).
6. **Expose plain Rust lookups** for providers. No LSP types in a domain crate.
7. Implement `PreparingIndexer` when extraction is substantial, and keep
   `index_prepared` to persistence plus `after_commit` publication.
8. **Sort anything a client can observe.** Repository iteration and `HashMap`
   ordering are not stable; leaking that into LSP responses produces flaky
   tests and jumpy UI.
9. **Register it** in `crates/app/src/workspace.rs`, and map `id()` to a domain
   in `domain_for_indexer` so it can be switched off.
10. **Bump `swlsp_indexer::INDEX_VERSION`** if persisted structure or
    extraction semantics changed.

## Performance rules

Every new indexed field has four costs. Consider all of them.

| Cost | Question to ask |
| --- | --- |
| Cold index time | Does this parse or analyze files it does not need to? Is the work in `Prepare`, or is it serializing the writer? |
| Warm start time | Is startup restoration streaming (`visit_all_values`), or materializing a catalog? |
| Disk size | Is the persisted value compact, or a whole AST-shaped struct? |
| Request latency | Is there a targeted key lookup, or does the request load everything and filter? |

Specific rules that come up repeatedly:

- **Do not load a whole catalog into memory when a targeted SQLite lookup
  suffices.** `get_values(key)` beats `get_all_values()` plus a filter.
- **Prefer `get_values_view` on request paths.**
- **Do not retain CST nodes across document versions.** Persist stable
  identifiers (names, symbol IDs, byte ranges), never syntax nodes.
- **Protect published mutable state** with the existing generation-swap pattern
  (publish a new `Arc` snapshot; readers take it once), and add concurrency
  tests for lifecycle changes.

## Debugging and testing

### Inspecting a real workspace

```bash
shopware-lsp --root /path/to/project index          # build or refresh
shopware-lsp --root /path/to/project index --force   # clear hashes and rebuild
shopware-lsp --root /path/to/project stats          # tracked files, cache size, memory
shopware-lsp --root /path/to/project --json stats
```

Persistent indexing skips source files larger than `indexing.maxFileSizeMiB`
(8 MiB by default). Set it to `0` to disable the configurable ceiling. Files
outside the parser's 32-bit source range are always rejected. `stats` reports
the ten largest indexed files plus the count, total size, and largest examples
of skipped oversized files.

The cache is a plain SQLite file, so it is directly inspectable:

```bash
sqlite3 ~/.cache/shopware-lsp/<root-hash>/indexes.db \
  "SELECT namespace, COUNT(*) FROM data GROUP BY namespace ORDER BY 2 DESC LIMIT 20;"
```

### Unit tests

Domain index tests construct the index against a temp directory, with no store
(`crates/feature/src/indexer.rs`):

```rust
#[test]
fn indexes_features() {
    let dir = tempfile::tempdir().unwrap();
    let indexer = FeatureIndexer::new(dir.path(), None).unwrap();
    let (path, content) = fixture();
    indexer.index(&ParsedFile::new(path, content)).unwrap();

    assert_eq!(indexer.all_features().unwrap().len(), 8);
    let v650 = indexer.feature_by_name("v6.5.0.0").unwrap();
    assert_eq!(v650.len(), 1);
    assert_eq!(v650[0].line, 4);
}
```

Cover four behaviors, not just extraction: **extraction**, **update** (index the
same path twice with different content and assert the old values are gone),
**removal** (`removed_files`), and **persistence** (close, reopen, still there).

Always use a `tempfile::tempdir()` (or an explicit cache base such as
`Workspace::new_in`); never touch a developer's real workspace cache.

### Scanner and integration tests

`crates/indexer/src/tests/scanner.rs` covers discovery, change detection,
batching, and failure bisection. The opt-in real-world suite behind the
`integration` feature of `swlsp-app` (`crates/app/src/tests/real_world`)
measures cold and warm indexing against an actual Shopware checkout:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
  cargo test --release -p swlsp-app --features integration --lib -- \
  --exact tests::real_world::indexing_main::shopware_trunk_indexing

SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
SHOPWARE_LSP_PROFILE_CACHE_DIR=/path/to/isolated-cache \
  cargo test --release -p swlsp-app --features integration --lib \
  shopware_trunk_indexing_profile -- --nocapture
```

The profile test reports construction, cold-index and warm-scan time, RSS, and
on-disk cache size; `SHOPWARE_LSP_PROFILE_WORKERS` and the other
`SHOPWARE_LSP_PROFILE_*` knobs in
`crates/app/src/tests/real_world/index_profile.rs` tune it. Use isolated cache
directories and reverse the run order when comparing revisions.

## Further reading

- [`architecture.md`](architecture.md) — the system this fits into
- [`parser-architecture.md`](parser-architecture.md) — the trees indexers read
- [`lsp-server.md`](lsp-server.md) — how providers consume indexes
- [`php-semantic-engine.md`](php-semantic-engine.md) — the largest index, with
  its own persisted graph
- [`AGENTS.md`](../AGENTS.md#indexing-and-persistence) — the contributor
  checklist
