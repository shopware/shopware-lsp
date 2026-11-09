//! The workspace file scanner: discovery, change detection, coordinated
//! indexing, removal, and the tracked-file state database.
//!
//! The scanner is the only source of filesystem index events (see
//! [`crate::watcher`]). Tracked file states live in their own SQLite database
//! next to the workspace store; index contents are written through the
//! shared [`Store`] mutation.

use std::collections::HashMap;
use std::fs;
use std::sync::atomic::{AtomicBool, AtomicI64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex, RwLock};
use std::time::{Instant, UNIX_EPOCH};

use rusqlite::Connection;
use serde::Serialize;

use crate::dataindex::in_clause_sql;
use crate::error::{CancelToken, Error, Result, ResultExt};
use crate::filetypes::is_scanned_path;
use crate::indexer::Indexer;
use crate::path_exclusions::PathExclusions;
use crate::paths::{self, SEPARATOR, path_within, relative_path_within};
use crate::phar_sources::is_phar_archive_path;
use crate::store::{apply_pragmas, lock, read_lock, write_lock};
use crate::vendor_packages::VendorPackages;
use crate::watcher::WatcherHandle;
use crate::workspace_symbols::WorkspaceSymbolCatalog;
use crate::{Mutation, Store};

const DEFAULT_SKIP_DIRS: &[&str] = &[
    "node_modules",
    "var",
    "vendor-bin",
    "bin",
    "cache",
    "dist",
    ".tmp",
    ".git",
    ".github",
    ".gitlab",
    ".claude",
    ".codex",
    ".continue",
    ".cursor",
    ".delta",
    ".windsurf",
    ".run",
    ".idea",
    ".vscode",
    ".fleet",
    ".zed",
    "tests",
    "public",
    ".ddev",
    ".devbox",
    ".devenv",
    ".direnv",
];

/// The default configurable source-file indexing limit.
pub const DEFAULT_MAX_FILE_SIZE_BYTES: i64 = 8 << 20;
/// The parser's hard 32-bit source range limit.
pub const MAXIMUM_SOURCE_FILE_BYTES: i64 = u32::MAX as i64;
pub(crate) const FILE_STATS_LIMIT: usize = 10;

pub(crate) const SKIPPED_CONFIGURED_LIMIT: &str = "configured file size limit";
pub(crate) const SKIPPED_PARSER_RANGE: &str = "32-bit parser source range";

const FILE_STATE_PRAGMAS: &[&str] = &[
    "PRAGMA journal_mode=WAL",
    "PRAGMA busy_timeout=5000",
    "PRAGMA synchronous=NORMAL",
    // File hash scans are sequential and do not benefit from a large cache.
    "PRAGMA cache_size=-8192",
    "PRAGMA auto_vacuum=INCREMENTAL",
    "PRAGMA wal_autocheckpoint=1000",
];

pub(crate) const STATE_BATCH_SIZE: usize = 128;

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FileScannerStats {
    pub tracked_files: usize,
    pub tracked_bytes: i64,
    pub indexers: usize,
    pub workers: usize,
    pub max_file_size_bytes: i64,
    pub skipped_oversized_count: usize,
    pub skipped_oversized_bytes: i64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub largest_indexed_files: Vec<FileSizeStats>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub largest_skipped_files: Vec<SkippedFileStats>,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct FileSizeStats {
    pub path: String,
    pub bytes: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SkippedFileStats {
    pub path: String,
    pub bytes: i64,
    pub limit_bytes: i64,
    pub reason: String,
}

/// Size and modification time of a file, as tracked in `file_hashes`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub(crate) struct FileMeta {
    pub size: i64,
    pub mtime: i64,
}

impl FileMeta {
    pub(crate) fn of(metadata: &fs::Metadata) -> Self {
        let mtime = metadata
            .modified()
            .ok()
            .and_then(|time| time.duration_since(UNIX_EPOCH).ok())
            .map(|duration| i64::try_from(duration.as_nanos()).unwrap_or(i64::MAX))
            .unwrap_or_default();
        Self {
            size: i64::try_from(metadata.len()).unwrap_or(i64::MAX),
            mtime,
        }
    }
}

/// A fast, stable content digest. When a file's size or modification time
/// changed but its digest did not (a `git checkout` round trip, `touch`, a
/// re-extracted dependency), only the tracked state is refreshed and no
/// indexer runs, like clangd's shard digests and rust-analyzer's VFS.
pub(crate) fn content_hash(content: &[u8]) -> i64 {
    // SQLite integers are signed; the bit pattern is what matters.
    xxhash_rust::xxh3::xxh3_64(content) as i64
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub(crate) struct StoredFileState {
    pub meta: FileMeta,
    pub hash: i64,
    pub found: bool,
}

#[derive(Debug, Clone)]
pub(crate) struct FileState {
    pub path: String,
    pub meta: FileMeta,
    pub hash: i64,
}

type UpdateCallback = Arc<dyn Fn() + Send + Sync>;

pub(crate) struct Inner {
    pub(crate) project_root: String,
    pub(crate) phar_cache: String,
    db: Mutex<Option<Connection>>,
    pub(crate) store: Option<Store>,
    pub(crate) symbols: Option<Arc<WorkspaceSymbolCatalog>>,
    pub(crate) indexers: Vec<Arc<dyn Indexer>>,
    pub(crate) on_update: RwLock<Option<UpdateCallback>>,
    pub(crate) worker_count: AtomicUsize,
    exclusions: PathExclusions,
    pub(crate) vendors: VendorPackages,
    max_file_size: AtomicI64,
    skipped: RwLock<HashMap<String, SkippedFileStats>>,
    pub(crate) operation: Mutex<()>,
    pub(crate) watcher: Mutex<Option<WatcherHandle>>,
    closed: AtomicBool,
}

/// Scans the project for files, tracks changes and drives the indexers.
///
/// Configure it (indexers, exclusions, callbacks) before sharing it or
/// starting the watcher; the configuration methods panic once a watcher
/// thread holds a reference.
pub struct FileScanner {
    pub(crate) inner: Arc<Inner>,
}

impl std::fmt::Debug for FileScanner {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("FileScanner")
            .field("project_root", &self.inner.project_root)
            .finish()
    }
}

impl FileScanner {
    /// Opens the tracked-state database at `db_path`. With a workspace
    /// `store`, every indexing batch runs in one coordinated mutation.
    pub fn new(
        project_root: impl Into<String>,
        db_path: impl AsRef<std::path::Path>,
        store: Option<Store>,
    ) -> Result<Self> {
        let db_path = db_path.as_ref();
        if let Some(parent) = db_path.parent() {
            fs::create_dir_all(parent).context("failed to create db directory")?;
        }
        let connection = Connection::open(db_path).context("failed to open database")?;
        connection.set_prepared_statement_cache_capacity(32);
        apply_pragmas(&connection, FILE_STATE_PRAGMAS)?;
        connection
            .execute_batch(
                "CREATE TABLE IF NOT EXISTS file_hashes (
                    path TEXT PRIMARY KEY,
                    size INTEGER NOT NULL,
                    mtime INTEGER NOT NULL,
                    hash INTEGER NOT NULL DEFAULT 0
                )",
            )
            .context("failed to initialize tables")?;
        let project_root = project_root.into();
        let cache_dir = db_path
            .parent()
            .map(|parent| parent.to_string_lossy().into_owned())
            .unwrap_or_default();
        Ok(Self {
            inner: Arc::new(Inner {
                vendors: VendorPackages::new(&project_root),
                phar_cache: paths::join(&[&cache_dir, "phar-sources"]),
                project_root,
                db: Mutex::new(Some(connection)),
                store,
                symbols: None,
                indexers: Vec::new(),
                on_update: RwLock::new(None),
                worker_count: AtomicUsize::new(0),
                exclusions: PathExclusions::default(),
                max_file_size: AtomicI64::new(DEFAULT_MAX_FILE_SIZE_BYTES),
                skipped: RwLock::new(HashMap::new()),
                operation: Mutex::new(()),
                watcher: Mutex::new(None),
                closed: AtomicBool::new(false),
            }),
        })
    }

    fn configure(&mut self) -> &mut Inner {
        Arc::get_mut(&mut self.inner)
            .expect("configure the file scanner before starting its watcher")
    }

    pub fn project_root(&self) -> &str {
        &self.inner.project_root
    }

    pub fn add_indexer(&mut self, indexer: Arc<dyn Indexer>) {
        self.configure().indexers.push(indexer);
    }

    /// Called after indexed or removed files were committed and batch
    /// indexers published their generations. Unlike the other configuration
    /// methods this works on a shared scanner, so a server can install its
    /// callback after a workspace factory created the scanner.
    pub fn set_on_update(&self, on_update: impl Fn() + Send + Sync + 'static) {
        *self
            .inner
            .on_update
            .write()
            .unwrap_or_else(|poisoned| poisoned.into_inner()) = Some(Arc::new(on_update));
    }

    pub fn set_workspace_symbol_catalog(&mut self, catalog: Arc<WorkspaceSymbolCatalog>) {
        self.configure().symbols = Some(catalog);
    }

    /// Installs the immutable workspace-relative exclusion policy.
    pub fn set_excluded_paths<S: AsRef<str>>(&mut self, patterns: &[S]) -> Result<()> {
        let exclusions = PathExclusions::new(patterns).context("configure excluded paths")?;
        self.configure().exclusions = exclusions;
        Ok(())
    }

    /// Sets the configurable source-file limit. Zero disables it; the
    /// parser's 32-bit range remains a hard limit.
    pub fn set_max_file_size_bytes(&self, size: i64) {
        self.inner
            .max_file_size
            .store(size.max(0), Ordering::Release);
    }

    /// Overrides the automatic indexing concurrency; zero restores the
    /// default. Takes effect on the next operation.
    pub fn set_worker_count(&self, count: usize) {
        self.inner.worker_count.store(count, Ordering::Release);
    }

    pub fn stats(&self) -> Result<FileScannerStats> {
        self.inner.stats()
    }

    /// Discovers every workspace file, removes stale tracked files and
    /// indexes new or changed ones.
    pub fn index_all(&self, cancel: &CancelToken) -> Result<()> {
        self.inner.index_all(cancel)
    }

    /// Indexes the given paths (filtered, deduplicated and sorted).
    pub fn index_files<S: AsRef<str>>(&self, cancel: &CancelToken, files: &[S]) -> Result<()> {
        let files: Vec<String> = files.iter().map(|file| file.as_ref().to_owned()).collect();
        self.inner.index_files(cancel, files, true, None)
    }

    pub fn remove_files<S: AsRef<str>>(&self, cancel: &CancelToken, paths: &[S]) -> Result<()> {
        let paths: Vec<String> = paths.iter().map(|path| path.as_ref().to_owned()).collect();
        self.inner.remove_files(cancel, &paths)
    }

    /// Clears every index and all tracked states, forcing a full reindex.
    pub fn clear_hashes(&self) -> Result<()> {
        self.inner.clear_hashes()
    }

    /// Tracked paths below `directory` from the persisted path index, in
    /// lexical order. No source files or directories are read.
    pub fn resource_paths(&self, directory: &str, limit: usize) -> Result<Vec<String>> {
        self.inner.resource_paths(directory, limit)
    }

    /// Whether `path` would be indexed by a workspace scan.
    /// Whether the index already reflects `content` for `path`: the last scan
    /// recorded the file's current size and modification time, and the file
    /// on disk holds exactly `content`. Editor overlays of such documents
    /// would duplicate indexed data.
    pub fn indexed_content_matches(&self, path: &str, content: &[u8]) -> bool {
        let Ok(metadata) = fs::metadata(path) else {
            return false;
        };
        if metadata.len() != content.len() as u64 {
            return false;
        }
        if !matches!(self.inner.query_file_state(path), Ok(Some(state)) if state.meta == FileMeta::of(&metadata))
        {
            return false;
        }
        fs::read(path).is_ok_and(|disk| disk == content)
    }

    pub fn should_index_path(&self, path: &str) -> bool {
        self.inner.should_index_path(path)
    }

    pub fn start_watcher(&self) -> Result<()> {
        crate::watcher::start(&self.inner)
    }

    pub fn stop_watcher(&self) {
        crate::watcher::stop(&self.inner);
    }

    /// Stops the watcher and closes the tracked-state database. Indexers are
    /// not closed: the scanner does not own them. Idempotent.
    pub fn close(&self) -> Result<()> {
        self.stop_watcher();
        let _operation = lock(&self.inner.operation);
        self.inner.closed.store(true, Ordering::Release);
        if let Some(connection) = lock(&self.inner.db).take() {
            let _ = connection.execute_batch("PRAGMA optimize");
            let _ = connection.execute_batch("PRAGMA incremental_vacuum");
            let _ = connection.execute_batch("PRAGMA wal_checkpoint(TRUNCATE)");
            connection
                .close()
                .map_err(|(_, error)| Error::from(error))?;
        }
        Ok(())
    }

    #[cfg(test)]
    pub(crate) fn with_db<R>(&self, operation: impl FnOnce(&Connection) -> Result<R>) -> Result<R> {
        self.inner.with_db(operation)
    }
}

impl Drop for FileScanner {
    fn drop(&mut self) {
        self.stop_watcher();
    }
}

/// Reports whether a path relative to a scan root is in a generated, cache,
/// tooling, or otherwise excluded directory. Exposed for commands that need
/// to discover the same source-file set as the scanner.
pub fn should_skip_relative_path(relative_path: &str) -> bool {
    if relative_path.is_empty() || relative_path == "." {
        return false;
    }
    let mut inside_vendor = false;
    for part in relative_path.split(SEPARATOR) {
        if part == "vendor" {
            inside_vendor = true;
        }
        // Composer package names and source trees can legitimately contain
        // directories such as symfony/cache. Root/application cache-like
        // directories remain excluded, while dependency source stays visible.
        let skip_inside_vendor = matches!(part, "cache" | "bin" | "dist" | "public" | "var");
        if DEFAULT_SKIP_DIRS.contains(&part) && (!inside_vendor || !skip_inside_vendor) {
            return true;
        }
    }
    false
}

pub(crate) fn default_index_worker_count(cpu_count: usize) -> usize {
    cpu_count.clamp(1, 4)
}

pub(crate) fn available_cpus() -> usize {
    std::thread::available_parallelism()
        .map(usize::from)
        .unwrap_or(1)
}

impl Inner {
    pub(crate) fn with_db<R>(&self, operation: impl FnOnce(&Connection) -> Result<R>) -> Result<R> {
        let connection = lock(&self.db);
        let connection = connection
            .as_ref()
            .ok_or_else(|| Error::msg("file scanner is closed"))?;
        operation(connection)
    }

    pub(crate) fn configured_workers(&self) -> usize {
        match self.worker_count.load(Ordering::Acquire) {
            0 => default_index_worker_count(available_cpus()),
            count => count,
        }
    }

    fn stats(&self) -> Result<FileScannerStats> {
        let mut stats = self.with_db(|connection| {
            let mut stats = FileScannerStats::default();
            let (files, bytes): (i64, i64) = connection
                .query_row(
                    "SELECT COUNT(*), COALESCE(SUM(size), 0) FROM file_hashes",
                    [],
                    |row| Ok((row.get(0)?, row.get(1)?)),
                )
                .context("query tracked file statistics")?;
            stats.tracked_files = usize::try_from(files).unwrap_or_default();
            stats.tracked_bytes = bytes;
            let mut statement = connection
                .prepare_cached(
                    "SELECT path, size FROM file_hashes ORDER BY size DESC, path LIMIT ?",
                )
                .context("query largest tracked files")?;
            let rows = statement
                .query_map([FILE_STATS_LIMIT as i64], |row| {
                    Ok(FileSizeStats {
                        path: row.get(0)?,
                        bytes: row.get(1)?,
                    })
                })
                .context("query largest tracked files")?;
            stats.largest_indexed_files = rows
                .collect::<rusqlite::Result<Vec<_>>>()
                .context("scan largest tracked file")?;
            Ok(stats)
        })?;
        stats.max_file_size_bytes = self.max_file_size.load(Ordering::Acquire);
        let (count, bytes, largest) = self.skipped_file_stats();
        stats.skipped_oversized_count = count;
        stats.skipped_oversized_bytes = bytes;
        stats.largest_skipped_files = largest;
        stats.indexers = self.indexers.len();
        stats.workers = self.configured_workers();
        Ok(stats)
    }

    pub(crate) fn source_size_rejection(&self, size: i64) -> Option<(&'static str, i64)> {
        if size > MAXIMUM_SOURCE_FILE_BYTES {
            return Some((SKIPPED_PARSER_RANGE, MAXIMUM_SOURCE_FILE_BYTES));
        }
        let limit = self.max_file_size.load(Ordering::Acquire);
        if limit > 0 && size > limit {
            return Some((SKIPPED_CONFIGURED_LIMIT, limit));
        }
        None
    }

    fn skipped_file_stats(&self) -> (usize, i64, Vec<SkippedFileStats>) {
        let mut files: Vec<SkippedFileStats> = read_lock(&self.skipped).values().cloned().collect();
        let bytes = files.iter().map(|file| file.bytes).sum();
        files.sort_by(|left, right| {
            right
                .bytes
                .cmp(&left.bytes)
                .then_with(|| left.path.cmp(&right.path))
        });
        let count = files.len();
        files.truncate(FILE_STATS_LIMIT);
        (count, bytes, files)
    }

    fn replace_skipped_files(&self, files: HashMap<String, SkippedFileStats>) {
        *write_lock(&self.skipped) = files;
    }

    pub(crate) fn record_skipped_files(&self, files: &[SkippedFileStats]) {
        if files.is_empty() {
            return;
        }
        let mut skipped = write_lock(&self.skipped);
        for file in files {
            skipped.insert(file.path.clone(), file.clone());
        }
    }

    pub(crate) fn clear_skipped_files(&self, paths: &[String]) {
        let mut skipped = write_lock(&self.skipped);
        for path in paths {
            skipped.remove(path);
        }
    }

    fn refresh_skipped_files(&self, paths: &[String]) {
        let mut skipped = write_lock(&self.skipped);
        for path in paths {
            let metadata = match fs::metadata(path) {
                Ok(metadata) if !metadata.is_dir() && self.should_index_path(path) => metadata,
                _ => {
                    skipped.remove(path);
                    continue;
                }
            };
            let size = FileMeta::of(&metadata).size;
            match self.source_size_rejection(size) {
                Some((reason, limit)) => {
                    skipped.insert(
                        path.clone(),
                        SkippedFileStats {
                            path: path.clone(),
                            bytes: size,
                            limit_bytes: limit,
                            reason: reason.to_owned(),
                        },
                    );
                }
                None => {
                    skipped.remove(path);
                }
            }
        }
    }

    pub(crate) fn index_all(&self, cancel: &CancelToken) -> Result<()> {
        // A warm cache must not advertise a complete generation while the
        // reconciliation that may invalidate it is still running.
        let mut resuming = false;
        if let Some(symbols) = &self.symbols {
            resuming = !symbols.ready().context("read workspace index readiness")?;
            symbols
                .set_ready(cancel, false)
                .context("mark workspace indexes rebuilding")?;
        }
        let files = self.discover_files(cancel)?;
        let (stored_states, stale) = self
            .scan_file_states(&files, true)
            .context("load tracked file states")?;
        if !stale.is_empty() {
            self.remove_files(cancel, &stale)
                .context("remove stale index entries")?;
        }
        log::info!("Found {} files to index", files.len());
        let started = Instant::now();
        if let Some(symbols) = &self.symbols {
            symbols.begin_bulk_population();
            if resuming {
                symbols.require_bulk_rebuild();
            }
        }
        let index_result = self.index_files(cancel, files, false, Some(stored_states));
        let symbol_result = match &self.symbols {
            Some(symbols) => symbols.end_bulk_population(cancel),
            None => Ok(()),
        };
        if let Some(error) = Error::join(
            [index_result.err(), symbol_result.err()]
                .into_iter()
                .flatten(),
        ) {
            return Err(error.context("failed to index files"));
        }
        if let Some(symbols) = &self.symbols {
            symbols
                .set_ready(cancel, true)
                .context("mark workspace symbol catalog ready")?;
        }
        log::info!("Indexing took {:?}", started.elapsed());
        Ok(())
    }

    pub(crate) fn discover_files(&self, cancel: &CancelToken) -> Result<Vec<String>> {
        let capacity: i64 = self
            .with_db(|connection| {
                Ok(connection
                    .query_row("SELECT COUNT(*) FROM file_hashes", [], |row| row.get(0))?)
            })
            .context("count tracked files")?;
        let files = Mutex::new(Vec::with_capacity(
            usize::try_from(capacity).unwrap_or_default(),
        ));
        let archives = Mutex::new(Vec::new());
        let failure: Mutex<Option<Error>> = Mutex::new(None);
        let walker = ignore::WalkBuilder::new(&self.project_root)
            .standard_filters(false)
            .follow_links(false)
            .threads(default_index_worker_count(available_cpus()))
            .build_parallel();
        walker.run(|| {
            Box::new(|entry| {
                if cancel.is_cancelled() {
                    lock(&failure).get_or_insert(Error::Cancelled);
                    return ignore::WalkState::Quit;
                }
                let entry = match entry {
                    Ok(entry) => entry,
                    Err(error) => {
                        lock(&failure)
                            .get_or_insert(Error::other(error).context("access workspace path"));
                        return ignore::WalkState::Quit;
                    }
                };
                let Some(path) = entry.path().to_str() else {
                    log::warn!("Skipping non-UTF-8 path {}", entry.path().display());
                    return ignore::WalkState::Skip;
                };
                if entry
                    .file_type()
                    .is_some_and(|file_type| file_type.is_dir())
                {
                    return if self.should_enter_directory(path) {
                        ignore::WalkState::Continue
                    } else {
                        ignore::WalkState::Skip
                    };
                }
                if is_phar_archive_path(path) && !self.is_explicitly_excluded(path, false) {
                    lock(&archives).push(path.to_owned());
                } else if self.should_index_path(path) {
                    lock(&files).push(path.to_owned());
                }
                ignore::WalkState::Continue
            })
        });
        if let Some(error) = failure
            .into_inner()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
        {
            return Err(error.context("failed to walk project directory"));
        }
        let mut files = files
            .into_inner()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        let archives = archives
            .into_inner()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        files.extend(self.discover_phar_sources(cancel, archives)?);
        // A complete scan rebuilds skipped-file telemetry from the same
        // metadata reads workers already need.
        self.replace_skipped_files(HashMap::new());
        files.sort();
        Ok(files)
    }

    pub(crate) fn load_file_states(&self, files: &[String]) -> Result<Vec<StoredFileState>> {
        self.scan_file_states(files, false)
            .map(|(states, _)| states)
    }

    /// Aligns the ordered SQLite state with the ordered discovery result in
    /// one pass, optionally collecting tracked paths absent from discovery.
    pub(crate) fn scan_file_states(
        &self,
        files: &[String],
        collect_stale: bool,
    ) -> Result<(Vec<StoredFileState>, Vec<String>)> {
        self.with_db(|connection| {
            let mut statement = connection
                .prepare_cached("SELECT path, size, mtime, hash FROM file_hashes ORDER BY path")?;
            let mut rows = statement.query([])?;
            let mut states = vec![StoredFileState::default(); files.len()];
            let mut stale = Vec::new();
            let mut file_index = 0;
            while let Some(row) = rows.next()? {
                let path = match row.get_ref(0)? {
                    rusqlite::types::ValueRef::Text(bytes)
                    | rusqlite::types::ValueRef::Blob(bytes) => bytes,
                    _ => &[],
                };
                let meta = FileMeta {
                    size: row.get(1)?,
                    mtime: row.get(2)?,
                };
                let hash = row.get(3)?;
                let mut matched = false;
                while file_index < files.len() {
                    match path.cmp(files[file_index].as_bytes()) {
                        std::cmp::Ordering::Less => break,
                        std::cmp::Ordering::Equal => {
                            states[file_index] = StoredFileState {
                                meta,
                                hash,
                                found: true,
                            };
                            matched = true;
                            break;
                        }
                        std::cmp::Ordering::Greater => file_index += 1,
                    }
                }
                if collect_stale && !matched {
                    stale.push(String::from_utf8_lossy(path).into_owned());
                }
            }
            Ok((states, stale))
        })
    }

    pub(crate) fn query_file_state(&self, path: &str) -> Result<Option<StoredFileState>> {
        self.with_db(|connection| {
            let mut statement = connection
                .prepare_cached("SELECT size, mtime, hash FROM file_hashes WHERE path = ?")?;
            let mut rows = statement.query([path])?;
            match rows.next()? {
                Some(row) => Ok(Some(StoredFileState {
                    meta: FileMeta {
                        size: row.get(0)?,
                        mtime: row.get(1)?,
                    },
                    hash: row.get(2)?,
                    found: true,
                })),
                None => Ok(None),
            }
        })
        .context("query file state")
    }

    pub(crate) fn remove_files(&self, cancel: &CancelToken, paths: &[String]) -> Result<()> {
        let _operation = lock(&self.operation);
        self.remove_files_locked(cancel, paths)?;
        self.refresh_skipped_files(paths);
        Ok(())
    }

    pub(crate) fn remove_files_locked(&self, cancel: &CancelToken, paths: &[String]) -> Result<()> {
        cancel.check()?;
        if paths.is_empty() {
            return Ok(());
        }
        let mutation = match &self.store {
            Some(store)
                if self
                    .indexers
                    .iter()
                    .all(|indexer| indexer.as_transactional_remover().is_some()) =>
            {
                Some(store.begin_mutation(cancel)?)
            }
            _ => None,
        };
        let mut errors = Vec::new();
        for indexer in &self.indexers {
            if cancel.is_cancelled() {
                errors.push(Error::Cancelled);
                break;
            }
            let result = match (&mutation, indexer.as_transactional_remover()) {
                (Some(mutation), Some(remover)) => remover.removed_files_in(paths, mutation),
                _ => indexer.removed_files(paths),
            };
            if let Err(error) = result {
                errors.push(error.context(indexer.id()));
            }
        }
        if let Some(error) = Error::join(errors) {
            let rollback = mutation
                .as_ref()
                .and_then(|mutation| mutation.rollback().err());
            let error = Error::join([Some(error), rollback].into_iter().flatten())
                .expect("at least one error");
            return Err(error.context("remove index entries"));
        }
        match &mutation {
            Some(mutation) => {
                if let Some(symbols) = &self.symbols
                    && let Err(error) = symbols.delete_files_in(mutation, paths)
                {
                    return Err(rollback_with(error, mutation));
                }
                if cancel.is_cancelled() {
                    return Err(rollback_with(Error::Cancelled, mutation));
                }
                mutation.commit()?;
            }
            None => {
                if let Some(symbols) = &self.symbols {
                    symbols.delete_files(cancel, paths)?;
                }
            }
        }
        self.delete_file_states(paths)?;
        self.notify_update();
        Ok(())
    }

    fn delete_file_states(&self, paths: &[String]) -> Result<()> {
        self.with_db(|connection| {
            let transaction = connection.unchecked_transaction()?;
            for batch in paths.chunks(STATE_BATCH_SIZE) {
                transaction
                    .prepare_cached(&in_clause_sql(
                        "DELETE FROM file_hashes WHERE path IN (",
                        batch.len(),
                    ))?
                    .execute(rusqlite::params_from_iter(batch))?;
            }
            transaction.commit()?;
            Ok(())
        })
    }

    /// Records the tracked state of files whose index mutation committed.
    pub(crate) fn update_file_states(&self, files: &[FileState]) -> Result<()> {
        if files.is_empty() {
            return Ok(());
        }
        self.with_db(|connection| {
            let transaction = connection.unchecked_transaction()?;
            for batch in files.chunks(STATE_BATCH_SIZE) {
                let mut statement =
                    transaction.prepare_cached(&file_state_upsert_sql(batch.len()))?;
                let mut arguments: Vec<rusqlite::types::Value> =
                    Vec::with_capacity(batch.len() * 4);
                for file in batch {
                    arguments.push(file.path.clone().into());
                    arguments.push(file.meta.size.into());
                    arguments.push(file.meta.mtime.into());
                    arguments.push(file.hash.into());
                }
                statement.execute(rusqlite::params_from_iter(arguments))?;
            }
            transaction.commit()?;
            Ok(())
        })
    }

    pub(crate) fn should_enter_directory(&self, path: &str) -> bool {
        if path_within(&self.phar_cache, path) {
            return false;
        }
        let relative = relative_path_within(&self.project_root, path);
        if let Some(relative) = &relative {
            if self.exclusions.excludes_directory(relative)
                || self.vendors.duplicates_root_package(relative)
            {
                return false;
            }
            if !should_skip_relative_path(relative) {
                return true;
            }
        } else {
            return true;
        }
        self.indexers
            .iter()
            .filter_map(|indexer| indexer.as_supplemental_path())
            .any(|selector| selector.should_enter_directory(path))
    }

    pub(crate) fn should_index_path(&self, path: &str) -> bool {
        if path_within(&self.phar_cache, path) && is_scanned_path(path) {
            return true;
        }
        if let Some(relative) = relative_path_within(&self.project_root, path) {
            if self.exclusions.excludes(&relative)
                || self.vendors.duplicates_root_package(&relative)
            {
                return false;
            }
            if !should_skip_relative_path(&relative) && is_scanned_path(path) {
                return true;
            }
        }
        self.indexers
            .iter()
            .filter_map(|indexer| indexer.as_supplemental_path())
            .any(|selector| selector.should_index_path(path))
    }

    pub(crate) fn should_preparse_path(&self, path: &str) -> bool {
        if path_within(&self.phar_cache, path) && is_scanned_path(path) {
            return true;
        }
        if let Some(relative) = relative_path_within(&self.project_root, path) {
            if self.exclusions.excludes(&relative)
                || self.vendors.duplicates_root_package(&relative)
            {
                return false;
            }
            if !should_skip_relative_path(&relative) && is_scanned_path(path) {
                return true;
            }
        }
        self.indexers.iter().any(|indexer| {
            indexer
                .as_supplemental_path()
                .is_some_and(|selector| selector.should_index_path(path))
                && indexer
                    .as_supplemental_syntax()
                    .is_some_and(|syntax| syntax.should_preparse_path(path))
        })
    }

    pub(crate) fn is_explicitly_excluded(&self, path: &str, directory: bool) -> bool {
        let Some(relative) = relative_path_within(&self.project_root, path) else {
            return false;
        };
        if directory {
            self.exclusions.excludes_directory(&relative)
        } else {
            self.exclusions.excludes(&relative)
        }
    }

    fn clear_hashes(&self) -> Result<()> {
        let _operation = lock(&self.operation);
        let cancel = CancelToken::new();
        let mutation = match &self.store {
            Some(store)
                if self
                    .indexers
                    .iter()
                    .all(|indexer| indexer.as_transactional_clearer().is_some()) =>
            {
                Some(store.begin_mutation(&cancel)?)
            }
            _ => None,
        };
        let mut errors = Vec::new();
        for indexer in &self.indexers {
            let result = match (&mutation, indexer.as_transactional_clearer()) {
                (Some(mutation), Some(clearer)) => clearer.clear_in(mutation),
                _ => indexer.clear(),
            };
            if let Err(error) = result {
                errors.push(error.context(indexer.id()));
            }
        }
        if let Some(error) = Error::join(errors) {
            let rollback = mutation
                .as_ref()
                .and_then(|mutation| mutation.rollback().err());
            let error = Error::join([Some(error), rollback].into_iter().flatten())
                .expect("at least one error");
            return Err(error.context("clear indexes"));
        }
        match &mutation {
            Some(mutation) => {
                if let Some(symbols) = &self.symbols
                    && let Err(error) = symbols.clear_in(mutation)
                {
                    return Err(rollback_with(error, mutation));
                }
                mutation.commit()?;
            }
            None => {
                if let Some(symbols) = &self.symbols {
                    symbols.clear(&cancel)?;
                }
            }
        }
        self.with_db(|connection| {
            connection.execute("DELETE FROM file_hashes", [])?;
            connection.execute_batch("PRAGMA incremental_vacuum")?;
            Ok(())
        })
    }

    fn resource_paths(&self, directory: &str, limit: usize) -> Result<Vec<String>> {
        if limit == 0 {
            return Ok(Vec::new());
        }
        let prefix = format!("{}{SEPARATOR}", paths::clean(directory));
        // The separator's successor bounds the primary-key range scan.
        let upper = format!(
            "{}{}",
            &prefix[..prefix.len() - SEPARATOR.len_utf8()],
            char::from(SEPARATOR as u8 + 1)
        );
        self.with_db(|connection| {
            let mut statement = connection.prepare_cached(
                "SELECT path FROM file_hashes WHERE path >= ? AND path < ? ORDER BY path LIMIT ?",
            )?;
            let rows = statement
                .query_map(rusqlite::params![prefix, upper, limit as i64], |row| {
                    row.get(0)
                })?;
            rows.collect::<rusqlite::Result<Vec<String>>>()
                .map_err(Error::from)
        })
    }

    /// Tracked paths equal to or below `path`.
    pub(crate) fn tracked_paths_under(&self, path: &str) -> Result<Vec<String>> {
        let cleaned = paths::clean(path);
        let pattern = format!("{cleaned}{SEPARATOR}%");
        self.with_db(|connection| {
            let mut statement = connection
                .prepare_cached("SELECT path FROM file_hashes WHERE path = ? OR path LIKE ?")?;
            let rows = statement.query_map([&cleaned, &pattern], |row| row.get(0))?;
            rows.collect::<rusqlite::Result<Vec<String>>>()
                .map_err(Error::from)
        })
    }
}

fn rollback_with(error: Error, mutation: &Mutation) -> Error {
    let rollback = mutation.rollback().err();
    Error::join([Some(error), rollback].into_iter().flatten()).expect("at least one error")
}

fn file_state_upsert_sql(count: usize) -> String {
    format!(
        "INSERT OR REPLACE INTO file_hashes (path, size, mtime, hash) VALUES {}",
        vec!["(?, ?, ?, ?)"; count].join(",")
    )
}

pub(crate) fn log_skipped_file(file: &SkippedFileStats) {
    log::warn!(
        "Skipping oversized source file {} ({} bytes): exceeds {} ({} bytes)",
        file.path,
        file.bytes,
        file.reason,
        file.limit_bytes
    );
}

pub(crate) fn log_skipped_file_count(count: usize) {
    log::warn!("Skipped {count} oversized source files; use stats for the largest files");
}

impl Inner {
    /// Runs the update callback outside the lock so it may re-enter the scanner.
    pub(crate) fn notify_update(&self) {
        let callback = self
            .on_update
            .read()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .clone();
        if let Some(callback) = callback {
            callback();
        }
    }
}
