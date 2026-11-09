//! The single namespaced SQLite store shared by every repository of a
//! workspace, and the coordinated write transaction ([`Mutation`]).
//!
//! Exactly one writer connection exists. [`Store::begin_mutation`] takes it
//! out of the store (waiting while another mutation holds it) and the
//! mutation hands it back on commit, rollback or drop. Reads use a small pool
//! of separate WAL reader connections, so a read never waits for the writer
//! and always observes committed data only.

use std::collections::{HashMap, HashSet};
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex, MutexGuard, RwLock, RwLockReadGuard, RwLockWriteGuard};
use std::thread::ThreadId;
use std::time::Duration;

use rusqlite::{Connection, OpenFlags};

use crate::error::{CancelToken, Error, Result, ResultExt};

const STORE_PRAGMAS: &[&str] = &[
    "PRAGMA journal_mode=WAL",
    "PRAGMA busy_timeout=5000",
    "PRAGMA synchronous=NORMAL",
    // A negative cache size is expressed in KiB. Eight MiB keeps the resident
    // native SQLite cache bounded.
    "PRAGMA cache_size=-8192",
    "PRAGMA foreign_keys=ON",
    "PRAGMA auto_vacuum=INCREMENTAL",
    "PRAGMA wal_autocheckpoint=1000",
];

/// Paths and namespaces are stored once and referenced by integer ids, the
/// way IntelliJ's VFS and rust-analyzer's `FileId` intern paths: every row
/// and both secondary indexes would otherwise repeat a ~100 byte absolute
/// path, which made up a fifth of the database and of the pages each commit
/// rewrites.
const STORE_SCHEMA: &str = "
    DROP TABLE IF EXISTS files;
    CREATE TABLE IF NOT EXISTS paths (
        id INTEGER PRIMARY KEY,
        path TEXT NOT NULL UNIQUE
    );
    CREATE TABLE IF NOT EXISTS namespaces (
        id INTEGER PRIMARY KEY,
        name TEXT NOT NULL
    );
    CREATE TABLE IF NOT EXISTS data (
        namespace INTEGER NOT NULL,
        path_id INTEGER NOT NULL,
        key TEXT NOT NULL,
        value BLOB NOT NULL
    );
    CREATE INDEX IF NOT EXISTS idx_data_namespace_key ON data(namespace, key);
    CREATE INDEX IF NOT EXISTS idx_data_namespace_path ON data(namespace, path_id);
";

/// The integer id of a repository namespace: a stable hash, so repositories
/// need no lookup before reading. `namespaces` maps ids back to names for
/// inspection.
pub(crate) fn namespace_id(namespace: &str) -> i64 {
    // SQLite integers are signed; the bit pattern is what matters.
    xxhash_rust::xxh3::xxh3_64(namespace.as_bytes()) as i64
}

/// The id of `path` in `paths`, inserting it when new.
pub(crate) fn intern_path(connection: &Connection, path: &str) -> Result<i64> {
    let existing = connection
        .prepare_cached("SELECT id FROM paths WHERE path = ?")?
        .query_row([path], |row| row.get(0));
    match existing {
        Ok(id) => Ok(id),
        Err(rusqlite::Error::QueryReturnedNoRows) => {
            connection
                .prepare_cached("INSERT INTO paths(path) VALUES (?)")?
                .execute([path])
                .context("intern path")?;
            Ok(connection.last_insert_rowid())
        }
        Err(error) => Err(Error::from(error).context("look up path id")),
    }
}

const READER_PRAGMAS: &[&str] = &[
    "PRAGMA busy_timeout=5000",
    "PRAGMA cache_size=-2048",
    "PRAGMA query_only=1",
];

/// Idle reader connections kept for reuse.
const MAX_IDLE_READERS: usize = 4;
/// Statements cached per connection. Repository replacement uses a handful of
/// SQL shapes across every namespace, so they stay prepared across batches.
const STATEMENT_CACHE_CAPACITY: usize = 64;

/// Applies `pragmas` in order, wrapping failures with the pragma text.
pub(crate) fn apply_pragmas(connection: &Connection, pragmas: &[&str]) -> Result<()> {
    for pragma in pragmas {
        // journal_mode returns a row; execute_batch ignores result rows.
        connection
            .execute_batch(pragma)
            .with_context(|| format!("set pragma {pragma}"))?;
    }
    Ok(())
}

/// Implemented by repositories so a committed mutation can invalidate their
/// in-memory read caches.
pub trait MutationCache: Send + Sync {
    fn invalidate_mutation_cache(&self);
}

struct WriterSlot {
    connection: Option<Connection>,
    busy: bool,
    closed: bool,
    /// The thread that began the active mutation. A second mutation from the
    /// same thread could never acquire the writer, so it fails instead of
    /// deadlocking (Go hits SQLite's busy timeout in the same situation).
    owner: Option<ThreadId>,
    /// Grouped mutations whose outer transaction is still open.
    group: Group,
}

/// Group commit: indexing batches run as savepoints of one longer
/// transaction that commits every [`GROUP_COMMIT_MUTATIONS`] batches and when
/// the run ends ([`Store::commit_grouped`]). Each commit rewrites the B-tree pages it
/// touched, and consecutive batches touch the same index pages, so a cold
/// index wrote several times the final database size. Cache invalidation
/// and after-commit callbacks of grouped mutations wait for the real commit.
#[derive(Default)]
struct Group {
    /// The idle writer connection holds an open transaction.
    open: bool,
    mutations: usize,
    touched: Vec<Arc<dyn MutationCache>>,
    after_commit: Vec<AfterCommit>,
}

const GROUP_COMMIT_MUTATIONS: usize = 8;
/// Undoes one grouped mutation and keeps the group's earlier ones.
const ROLLBACK_SAVEPOINT: &str = "ROLLBACK TO SAVEPOINT grouped; RELEASE SAVEPOINT grouped";

pub(crate) struct StoreInner {
    path: PathBuf,
    writer: Mutex<WriterSlot>,
    writer_released: Condvar,
    readers: Mutex<Vec<Connection>>,
    closed: AtomicBool,
    /// Rows were deleted by path or namespace, so `paths` may hold ids no
    /// row refers to; [`Store::close`] then removes them.
    paths_released: AtomicBool,
    checkpointer: Checkpointer,
}

/// How long the checkpointer waits after a commit so that a burst of commits
/// (cold indexing commits a batch every few milliseconds) shares one
/// checkpoint.
const CHECKPOINT_COALESCE: Duration = Duration::from_millis(200);

/// Moves committed WAL frames into the database file on a background thread.
///
/// SQLite's automatic checkpoint runs inside the committing writer: with
/// `synchronous=NORMAL` every ~4 MiB of WAL a commit stalls on `fsync` while
/// all other indexing workers wait for the single writer. The writer disables
/// automatic checkpoints and this thread runs `PASSIVE` checkpoints on its
/// own connection, which never block writers or readers.
struct Checkpointer {
    state: Arc<(Mutex<CheckpointState>, Condvar)>,
    thread: Mutex<Option<std::thread::JoinHandle<()>>>,
}

#[derive(Default)]
struct CheckpointState {
    pending: bool,
    stop: bool,
}

impl Checkpointer {
    fn start(path: &Path) -> Self {
        let state: Arc<(Mutex<CheckpointState>, Condvar)> = Arc::default();
        let thread_state = state.clone();
        let path = path.to_path_buf();
        let thread = std::thread::Builder::new()
            .name("index-checkpoint".into())
            .spawn(move || checkpoint_loop(&path, &thread_state))
            .ok();
        Self {
            state,
            thread: Mutex::new(thread),
        }
    }

    fn request(&self) {
        let (state, wake) = &*self.state;
        lock(state).pending = true;
        wake.notify_one();
    }

    fn stop(&self) {
        {
            let (state, wake) = &*self.state;
            lock(state).stop = true;
            wake.notify_one();
        }
        if let Some(thread) = lock(&self.thread).take() {
            let _ = thread.join();
        }
    }
}

fn checkpoint_loop(path: &Path, state: &(Mutex<CheckpointState>, Condvar)) {
    let Ok(connection) = Connection::open(path) else {
        return;
    };
    let _ = connection.execute_batch("PRAGMA busy_timeout=5000");
    let (state, wake) = state;
    loop {
        {
            let mut guard = lock(state);
            while !guard.pending && !guard.stop {
                guard = wake
                    .wait(guard)
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
            }
            if guard.stop {
                return;
            }
        }
        // Coalesce a burst of commits, then checkpoint what they wrote.
        let stopping = {
            let guard = lock(state);
            let (guard, _) = wake
                .wait_timeout_while(guard, CHECKPOINT_COALESCE, |state| !state.stop)
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            guard.stop
        };
        if stopping {
            return;
        }
        lock(state).pending = false;
        let _ = connection.execute_batch("PRAGMA wal_checkpoint(PASSIVE)");
    }
}

/// The single SQLite database used by all repositories in a workspace.
/// Repository data is isolated by namespace. Cloning is cheap and shares the
/// same connections.
#[derive(Clone)]
pub struct Store {
    inner: Arc<StoreInner>,
}

impl fmt::Debug for Store {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Store")
            .field("path", &self.inner.path)
            .finish()
    }
}

impl Store {
    /// Opens (creating when needed) the store at `db_path`.
    pub fn open(db_path: impl AsRef<Path>) -> Result<Self> {
        let path = db_path.as_ref().to_path_buf();
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent).context("create database directory")?;
        }
        let connection = Connection::open(&path).context("open index database")?;
        connection.set_prepared_statement_cache_capacity(STATEMENT_CACHE_CAPACITY);
        apply_pragmas(&connection, STORE_PRAGMAS)?;
        // Checkpoints run on the background checkpointer instead of inside
        // commits (see `Checkpointer`).
        apply_pragmas(&connection, &["PRAGMA wal_autocheckpoint=0"])?;
        if store_uses_legacy_schema(&connection).context("inspect index database schema")? {
            connection
                .execute_batch("DROP TABLE IF EXISTS files; DROP TABLE IF EXISTS data;")
                .context("reset legacy index database schema")?;
        }
        connection
            .execute_batch(STORE_SCHEMA)
            .context("initialize index database")?;
        let checkpointer = Checkpointer::start(&path);
        Ok(Self {
            inner: Arc::new(StoreInner {
                path,
                writer: Mutex::new(WriterSlot {
                    connection: Some(connection),
                    busy: false,
                    closed: false,
                    owner: None,
                    group: Group::default(),
                }),
                writer_released: Condvar::new(),
                readers: Mutex::new(Vec::new()),
                closed: AtomicBool::new(false),
                paths_released: AtomicBool::new(false),
                checkpointer,
            }),
        })
    }

    pub fn path(&self) -> &Path {
        &self.inner.path
    }

    /// Whether both handles refer to the same open store.
    #[cfg(test)]
    pub(crate) fn same(&self, other: &Store) -> bool {
        Arc::ptr_eq(&self.inner, &other.inner)
    }

    pub(crate) fn same_store(&self, inner: &Arc<StoreInner>) -> bool {
        Arc::ptr_eq(&self.inner, inner)
    }

    /// Starts the single coordinated write transaction, waiting while another
    /// mutation is active. Cancellation while waiting returns
    /// [`Error::Cancelled`]. Grouped mutations still pending commit are
    /// committed first, so this mutation's commit is durable when it returns.
    pub fn begin_mutation(&self, cancel: &CancelToken) -> Result<Mutation> {
        self.begin(cancel, false)
    }

    /// Starts a mutation whose commit may be deferred and grouped with the
    /// following grouped mutations (see [`Group`]). Rolling it back discards
    /// only its own writes. [`Store::commit_grouped`] makes pending grouped
    /// commits durable.
    pub fn begin_grouped_mutation(&self, cancel: &CancelToken) -> Result<Mutation> {
        self.begin(cancel, true)
    }

    fn begin(&self, cancel: &CancelToken, grouped: bool) -> Result<Mutation> {
        let (connection, pending) = self.acquire_writer(cancel, !grouped)?;
        if let Some(pending) = pending
            && let Err(error) = self.inner.commit_group(&connection, pending)
        {
            self.inner.release_writer(connection);
            return Err(error);
        }
        let group_open = lock(&self.inner.writer).group.open;
        let begin = match (grouped, group_open) {
            (true, true) => "SAVEPOINT grouped",
            (true, false) => "BEGIN IMMEDIATE; SAVEPOINT grouped",
            (false, _) => "BEGIN IMMEDIATE",
        };
        if let Err(error) = connection.execute_batch(begin) {
            if !group_open && !connection.is_autocommit() {
                let _ = connection.execute_batch("ROLLBACK");
            }
            self.inner.release_writer(connection);
            return Err(Error::from(error).context("begin index mutation"));
        }
        if grouped {
            lock(&self.inner.writer).group.open = true;
        }
        Ok(Mutation {
            store: self.inner.clone(),
            grouped,
            state: Mutex::new(MutationState {
                connection: Some(connection),
                touched: Vec::new(),
                after_commit: Vec::new(),
                prepared: HashSet::new(),
                path_ids: HashMap::new(),
                done: false,
            }),
        })
    }

    /// Takes the writer connection, waiting while a mutation holds it. With
    /// `take_group`, an open group is handed over for committing.
    fn acquire_writer(
        &self,
        cancel: &CancelToken,
        take_group: bool,
    ) -> Result<(Connection, Option<Group>)> {
        let mut slot = lock(&self.inner.writer);
        loop {
            cancel.check()?;
            if slot.closed {
                return Err(Error::msg("index store is closed"));
            }
            if !slot.busy {
                break;
            }
            if slot.owner == Some(std::thread::current().id()) {
                return Err(Error::msg(
                    "index mutation is already active on this thread",
                ));
            }
            slot = self
                .inner
                .writer_released
                .wait_timeout(slot, Duration::from_millis(10))
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .0;
        }
        let connection = slot
            .connection
            .take()
            .ok_or_else(|| Error::msg("index store is closed"))?;
        slot.busy = true;
        slot.owner = Some(std::thread::current().id());
        let group = (take_group && slot.group.open).then(|| std::mem::take(&mut slot.group));
        Ok((connection, group))
    }

    /// Commits grouped mutations that are still pending. Idempotent.
    pub fn commit_grouped(&self) -> Result<()> {
        if !lock(&self.inner.writer).group.open {
            return Ok(());
        }
        let (connection, pending) = self.acquire_writer(&CancelToken::new(), true)?;
        let result = match pending {
            Some(pending) => self.inner.commit_group(&connection, pending),
            None => Ok(()),
        };
        self.inner.release_writer(connection);
        result
    }

    /// Runs `operation` in its own mutation and commits it. This is the
    /// standalone (non-coordinated) write path.
    pub fn write<R>(&self, operation: impl FnOnce(&Mutation) -> Result<R>) -> Result<R> {
        let mutation = self.begin_mutation(&CancelToken::new())?;
        match operation(&mutation) {
            Ok(value) => {
                mutation.commit()?;
                Ok(value)
            }
            Err(error) => {
                let rollback = mutation.rollback();
                Err(
                    Error::join([Some(error), rollback.err()].into_iter().flatten())
                        .expect("at least one error"),
                )
            }
        }
    }

    /// Runs `operation` on a pooled reader connection. Readers observe the
    /// latest committed state and never wait for an active mutation.
    pub fn read<R>(&self, operation: impl FnOnce(&Connection) -> Result<R>) -> Result<R> {
        if self.inner.closed.load(Ordering::Acquire) {
            return Err(Error::msg("index store is closed"));
        }
        let connection = match lock(&self.inner.readers).pop() {
            Some(connection) => connection,
            None => self.open_reader()?,
        };
        let result = operation(&connection);
        let mut readers = lock(&self.inner.readers);
        if readers.len() < MAX_IDLE_READERS && !self.inner.closed.load(Ordering::Acquire) {
            readers.push(connection);
        }
        result
    }

    fn open_reader(&self) -> Result<Connection> {
        let connection = Connection::open_with_flags(
            &self.inner.path,
            OpenFlags::SQLITE_OPEN_READ_WRITE
                | OpenFlags::SQLITE_OPEN_NO_MUTEX
                | OpenFlags::SQLITE_OPEN_URI,
        )
        .context("open index reader")?;
        connection.set_prepared_statement_cache_capacity(STATEMENT_CACHE_CAPACITY);
        apply_pragmas(&connection, READER_PRAGMAS)?;
        Ok(connection)
    }

    /// Optimizes, vacuums and checkpoints the database, then closes every
    /// connection. Waits for an active mutation. Idempotent.
    pub fn close(&self) -> Result<()> {
        // Commit a pending group while the store is still open, so its
        // callbacks can read; one begun concurrently is committed below.
        let grouped = self.commit_grouped();
        let mut slot = lock(&self.inner.writer);
        while slot.busy {
            slot = self
                .inner
                .writer_released
                .wait(slot)
                .unwrap_or_else(|poisoned| poisoned.into_inner());
        }
        if slot.closed {
            return Ok(());
        }
        slot.closed = true;
        let pending = std::mem::take(&mut slot.group);
        self.inner.closed.store(true, Ordering::Release);
        self.inner.checkpointer.stop();
        lock(&self.inner.readers).clear();
        if let Some(connection) = slot.connection.take() {
            let committed = if pending.open {
                self.inner.commit_group(&connection, pending)
            } else {
                Ok(())
            };
            if self.inner.paths_released.load(Ordering::Acquire) {
                let _ = remove_unreferenced_paths(&connection);
            }
            let _ = connection.execute_batch("PRAGMA optimize");
            let _ = connection.execute_batch("PRAGMA incremental_vacuum");
            let _ = connection.execute_batch("PRAGMA wal_checkpoint(TRUNCATE)");
            connection
                .close()
                .map_err(|(_, error)| Error::from(error))?;
            committed?;
        }
        grouped
    }
}

impl StoreInner {
    /// Commits a group's outer transaction, then invalidates the touched
    /// caches and runs the deferred callbacks. On failure the group's
    /// mutations are rolled back together and their callbacks dropped.
    fn commit_group(&self, connection: &Connection, group: Group) -> Result<()> {
        let result = connection.execute_batch("COMMIT");
        if result.is_err() {
            if !connection.is_autocommit() {
                let _ = connection.execute_batch("ROLLBACK");
            }
            return result.context("commit grouped index mutations");
        }
        self.checkpointer.request();
        for cache in group.touched {
            cache.invalidate_mutation_cache();
        }
        for callback in group.after_commit {
            callback();
        }
        Ok(())
    }

    fn release_writer(&self, connection: Connection) {
        let mut slot = lock(&self.writer);
        slot.connection = Some(connection);
        slot.busy = false;
        slot.owner = None;
        drop(slot);
        self.writer_released.notify_all();
    }
}

/// Drops interned paths no row refers to any more (deleted or renamed
/// files), keeping `paths` proportional to the indexed workspace.
pub(crate) fn remove_unreferenced_paths(connection: &Connection) -> Result<usize> {
    let has_symbols: bool = connection.query_row(
        "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'workspace_symbols')",
        [],
        |row| row.get(0),
    )?;
    let sql = if has_symbols {
        "DELETE FROM paths WHERE id NOT IN
         (SELECT path_id FROM data UNION SELECT owner_id FROM workspace_symbols)"
    } else {
        "DELETE FROM paths WHERE id NOT IN (SELECT path_id FROM data)"
    };
    connection
        .execute(sql, [])
        .context("remove unreferenced paths")
}

/// Whether `data` predates interned path ids (a file-association table or
/// inline path text). Such stores are rebuilt.
fn store_uses_legacy_schema(connection: &Connection) -> rusqlite::Result<bool> {
    let mut statement = connection.prepare("PRAGMA table_info(data)")?;
    let names = statement.query_map([], |row| row.get::<_, String>(1))?;
    let mut has_columns = false;
    let mut has_path_id = false;
    for name in names {
        has_columns = true;
        if name? == "path_id" {
            has_path_id = true;
        }
    }
    Ok(has_columns && !has_path_id)
}

pub(crate) fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

pub(crate) fn read_lock<T>(lock: &RwLock<T>) -> RwLockReadGuard<'_, T> {
    lock.read().unwrap_or_else(|poisoned| poisoned.into_inner())
}

pub(crate) fn write_lock<T>(lock: &RwLock<T>) -> RwLockWriteGuard<'_, T> {
    lock.write()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

type AfterCommit = Box<dyn FnOnce() + Send>;

struct MutationState {
    connection: Option<Connection>,
    touched: Vec<Arc<dyn MutationCache>>,
    after_commit: Vec<AfterCommit>,
    prepared: HashSet<String>,
    /// Path ids resolved by this transaction; every repository of a batch
    /// writes the same paths.
    path_ids: HashMap<String, i64>,
    done: bool,
}

/// A transaction shared by every namespaced repository of one store for a
/// batch of files. Exactly one mutation exists per store at a time.
///
/// Dropping an active mutation rolls it back.
pub struct Mutation {
    store: Arc<StoreInner>,
    /// A savepoint of the open group transaction (see [`Group`]).
    grouped: bool,
    state: Mutex<MutationState>,
}

impl fmt::Debug for Mutation {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Mutation")
            .field("store", &self.store.path)
            .field("active", &self.is_active())
            .finish()
    }
}

/// Access to the mutation's connection inside one repository operation.
pub struct MutationConnection<'a> {
    connection: &'a Connection,
    prepared: &'a mut HashSet<String>,
    path_ids: &'a mut HashMap<String, i64>,
}

impl<'a> MutationConnection<'a> {
    /// A transaction-scoped cached statement. Repository replacement shares a
    /// handful of SQL shapes across every namespace and file in a batch.
    pub fn prepare_cached(&mut self, sql: &str) -> Result<rusqlite::CachedStatement<'a>> {
        if !self.prepared.contains(sql) {
            self.prepared.insert(sql.to_owned());
        }
        Ok(self.connection.prepare_cached(sql)?)
    }

    /// The raw transaction connection for ad-hoc statements.
    pub fn connection(&self) -> &'a Connection {
        self.connection
    }

    /// The interned id of `path`, created inside this transaction when new.
    pub fn path_id(&mut self, path: &str) -> Result<i64> {
        if let Some(&id) = self.path_ids.get(path) {
            return Ok(id);
        }
        let id = intern_path(self.connection, path)?;
        self.path_ids.insert(path.to_owned(), id);
        Ok(id)
    }
}

impl Mutation {
    pub(crate) fn store_inner(&self) -> &Arc<StoreInner> {
        &self.store
    }

    /// Records that this mutation deletes rows by path or namespace, so
    /// interned paths may become unreferenced.
    pub(crate) fn release_paths(&self) {
        self.store.paths_released.store(true, Ordering::Release);
    }

    pub fn is_active(&self) -> bool {
        !lock(&self.state).done
    }

    /// Runs `operation` against the transaction connection.
    pub fn with_connection<R>(
        &self,
        operation: impl FnOnce(&mut MutationConnection<'_>) -> Result<R>,
    ) -> Result<R> {
        let mut state = lock(&self.state);
        let state = &mut *state;
        if state.done {
            return Err(Error::msg("index mutation is not active"));
        }
        let connection = state
            .connection
            .as_ref()
            .ok_or_else(|| Error::msg("index mutation is not active"))?;
        operation(&mut MutationConnection {
            connection,
            prepared: &mut state.prepared,
            path_ids: &mut state.path_ids,
        })
    }

    /// Number of distinct statements prepared through this mutation.
    pub fn prepared_statement_count(&self) -> usize {
        lock(&self.state).prepared.len()
    }

    /// Registers a repository cache to invalidate after a successful commit.
    pub fn touch(&self, cache: Arc<dyn MutationCache>) -> Result<()> {
        let mut state = lock(&self.state);
        if state.done {
            return Err(Error::msg("index mutation is not active"));
        }
        if !state
            .touched
            .iter()
            .any(|existing| Arc::ptr_eq(existing, &cache))
        {
            state.touched.push(cache);
        }
        Ok(())
    }

    /// Registers an in-memory publication callback. It runs only after the
    /// SQLite transaction and all repository cache invalidations succeed.
    /// Rollback discards callbacks.
    pub fn after_commit(&self, callback: impl FnOnce() + Send + 'static) -> Result<()> {
        let mut state = lock(&self.state);
        if state.done {
            return Err(Error::msg("index mutation is not active"));
        }
        state.after_commit.push(Box::new(callback));
        Ok(())
    }

    /// Commits the transaction, invalidates touched caches, runs after-commit
    /// callbacks, then releases the writer.
    pub fn commit(&self) -> Result<()> {
        let mut state = lock(&self.state);
        if state.done {
            return Err(Error::msg("index mutation is not active"));
        }
        state.done = true;
        state.prepared.clear();
        state.path_ids.clear();
        let connection = state
            .connection
            .take()
            .expect("active mutation owns the writer");
        let touched = std::mem::take(&mut state.touched);
        let callbacks = std::mem::take(&mut state.after_commit);
        drop(state);
        if self.grouped {
            return self.commit_grouped(connection, touched, callbacks);
        }
        let result = connection.execute_batch("COMMIT");
        if result.is_ok() {
            self.store.checkpointer.request();
            for cache in touched {
                cache.invalidate_mutation_cache();
            }
            for callback in callbacks {
                callback();
            }
        } else if !connection.is_autocommit() {
            let _ = connection.execute_batch("ROLLBACK");
        }
        self.store.release_writer(connection);
        result.context("commit index mutation")
    }

    /// Folds a grouped mutation into its group and commits the group when
    /// it is due.
    fn commit_grouped(
        &self,
        connection: Connection,
        touched: Vec<Arc<dyn MutationCache>>,
        callbacks: Vec<AfterCommit>,
    ) -> Result<()> {
        if let Err(error) = connection.execute_batch("RELEASE SAVEPOINT grouped") {
            let _ = connection.execute_batch(ROLLBACK_SAVEPOINT);
            self.store.release_writer(connection);
            return Err(Error::from(error).context("commit index mutation"));
        }
        let due = {
            let mut slot = lock(&self.store.writer);
            let group = &mut slot.group;
            group.mutations += 1;
            group.touched.extend(touched);
            group.after_commit.extend(callbacks);
            (group.mutations >= GROUP_COMMIT_MUTATIONS).then(|| std::mem::take(group))
        };
        let result = match due {
            Some(group) => self.store.commit_group(&connection, group),
            None => Ok(()),
        };
        self.store.release_writer(connection);
        result
    }

    /// Rolls back the transaction. A finished mutation is a no-op.
    pub fn rollback(&self) -> Result<()> {
        let mut state = lock(&self.state);
        if state.done {
            return Ok(());
        }
        state.done = true;
        state.prepared.clear();
        state.path_ids.clear();
        state.touched.clear();
        state.after_commit.clear();
        let connection = state
            .connection
            .take()
            .expect("active mutation owns the writer");
        drop(state);
        let result = if connection.is_autocommit() {
            if self.grouped {
                // The group transaction is gone; so are its deferred effects.
                lock(&self.store.writer).group = Group::default();
            }
            Ok(())
        } else if self.grouped {
            connection.execute_batch(ROLLBACK_SAVEPOINT)
        } else {
            connection.execute_batch("ROLLBACK")
        };
        self.store.release_writer(connection);
        result.context("rollback index mutation")
    }
}

impl Drop for StoreInner {
    fn drop(&mut self) {
        self.checkpointer.stop();
    }
}

impl Drop for Mutation {
    fn drop(&mut self) {
        let _ = self.rollback();
    }
}
