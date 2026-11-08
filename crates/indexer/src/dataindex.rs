//! Generic namespaced repository over the shared workspace store
//! (Go `DataIndexer[T]`).
//!
//! Values are MessagePack-encoded with field names (so `#[serde(default)]`
//! and `skip_serializing_if` keep working) and stored per `(namespace,
//! file_path, key)`. Every write replaces or removes complete source paths so
//! file changes and deletions cannot leave stale rows. Reads go through the
//! store's WAL reader pool and observe committed data only; decoded results
//! are cached until a committed mutation touches the repository.

use std::cell::RefCell;
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, OnceLock, Weak};

use rusqlite::types::ValueRef;
use serde::Serialize;
use serde::de::DeserializeOwned;

use crate::error::{CancelToken, Error, Result, ResultExt};
use crate::store::{MutationCache, lock};
use crate::{Mutation, Store};

const MAX_POOLED_MESSAGE_PACK_BUFFER: usize = 1 << 20;
const DELETE_BATCH_SIZE: usize = 128;

thread_local! {
    static ENCODE_BUFFER: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
}

/// Encodes `value` exactly as repositories persist it.
pub fn encode_value<T: Serialize + ?Sized>(value: &T) -> Result<Vec<u8>> {
    let mut buffer = Vec::new();
    rmp_serde::encode::write_named(&mut buffer, value)?;
    Ok(buffer)
}

/// Decodes a persisted repository value, for example one visited through
/// [`DataIndexer::visit_all_encoded_values`].
pub fn decode_value<T: DeserializeOwned>(encoded: &[u8]) -> Result<T> {
    Ok(rmp_serde::from_slice(encoded)?)
}

/// Encodes into a reusable per-thread buffer, bounded so one huge value does
/// not pin its allocation for the lifetime of a worker thread.
fn with_encoded<T: Serialize + ?Sized, R>(
    value: &T,
    use_bytes: impl FnOnce(&[u8]) -> Result<R>,
) -> Result<R> {
    ENCODE_BUFFER.with(|cell| {
        let mut owned;
        let mut borrowed = cell.try_borrow_mut();
        let buffer: &mut Vec<u8> = match borrowed.as_deref_mut() {
            Ok(buffer) => buffer,
            Err(_) => {
                owned = Vec::new();
                &mut owned
            }
        };
        buffer.clear();
        let result = rmp_serde::encode::write_named(buffer, value)
            .map_err(Error::from)
            .and_then(|()| use_bytes(buffer));
        if buffer.capacity() > MAX_POOLED_MESSAGE_PACK_BUFFER {
            *buffer = Vec::new();
        }
        result
    })
}

/// Releases this thread's pooled encoding buffer.
pub(crate) fn clear_message_pack_buffers() {
    ENCODE_BUFFER.with(|cell| {
        if let Ok(mut buffer) = cell.try_borrow_mut() {
            *buffer = Vec::new();
        }
    });
}

struct CacheState<T> {
    values: HashMap<String, Arc<[T]>>,
    all: Option<Arc<[T]>>,
    keys: Option<Arc<[String]>>,
    path_keys: HashMap<String, Arc<[String]>>,
    path_values: HashMap<String, Arc<[T]>>,
}

impl<T> Default for CacheState<T> {
    fn default() -> Self {
        Self {
            values: HashMap::new(),
            all: None,
            keys: None,
            path_keys: HashMap::new(),
            path_values: HashMap::new(),
        }
    }
}

struct RepositoryCache<T> {
    state: Mutex<CacheState<T>>,
}

impl<T: Send + Sync> MutationCache for RepositoryCache<T> {
    fn invalidate_mutation_cache(&self) {
        *lock(&self.state) = CacheState::default();
    }
}

/// File name of the store shared by repositories that are opened without a
/// workspace store in the same directory.
pub const SHARED_REPOSITORY_STORE: &str = "repositories.db";

/// A store shared by every standalone repository of one directory; closed
/// (optimized and checkpointed) when the last of them closes.
struct SharedStore(Store);

impl Drop for SharedStore {
    fn drop(&mut self) {
        let _ = self.0.close();
    }
}

/// The open shared store of `directory`, or a new one.
fn shared_store(directory: &Path) -> Result<Arc<SharedStore>> {
    static STORES: OnceLock<Mutex<HashMap<PathBuf, Weak<SharedStore>>>> = OnceLock::new();
    let mut stores = lock(STORES.get_or_init(Mutex::default));
    stores.retain(|_, store| store.strong_count() > 0);
    if let Some(store) = stores.get(directory).and_then(Weak::upgrade) {
        return Ok(store);
    }
    let store = Arc::new(SharedStore(Store::open(
        directory.join(SHARED_REPOSITORY_STORE),
    )?));
    stores.insert(directory.to_path_buf(), Arc::downgrade(&store));
    Ok(store)
}

/// A namespaced repository storing values of type `T` per source path and key.
pub struct DataIndexer<T> {
    store: Store,
    namespace: String,
    owns_store: bool,
    /// The directory store this standalone repository shares, released on
    /// close.
    shared: Mutex<Option<Arc<SharedStore>>>,
    closed: AtomicBool,
    cache: Arc<RepositoryCache<T>>,
}

impl<T> std::fmt::Debug for DataIndexer<T> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("DataIndexer")
            .field("namespace", &self.namespace)
            .field("store", &self.store)
            .finish()
    }
}

impl<T> DataIndexer<T>
where
    T: Serialize + DeserializeOwned + Send + Sync + 'static,
{
    /// Opens an isolated store at `db_path` with the `default` namespace.
    pub fn open(db_path: impl AsRef<std::path::Path>) -> Result<Self> {
        Self::open_namespace(db_path, "default")
    }

    /// Opens an isolated store at `db_path` owned by the repository.
    pub fn open_namespace(db_path: impl AsRef<std::path::Path>, namespace: &str) -> Result<Self> {
        let store = Store::open(db_path)?;
        match Self::in_store(&store, namespace) {
            Ok(mut index) => {
                index.owns_store = true;
                Ok(index)
            }
            Err(error) => {
                let _ = store.close();
                Err(error)
            }
        }
    }

    /// A namespaced repository in a shared workspace store.
    pub fn in_store(store: &Store, namespace: &str) -> Result<Self> {
        if namespace.is_empty() {
            return Err(Error::msg("index namespace is required"));
        }
        Ok(Self {
            store: store.clone(),
            namespace: namespace.to_owned(),
            owns_store: false,
            shared: Mutex::new(None),
            closed: AtomicBool::new(false),
            cache: Arc::new(RepositoryCache {
                state: Mutex::new(CacheState::default()),
            }),
        })
    }

    /// A repository in `store` when provided. Without one (standalone use,
    /// mostly tests), every repository of `db_path`'s directory shares one
    /// store ([`SHARED_REPOSITORY_STORE`]) instead of opening a database per
    /// repository; namespaces keep their data apart.
    pub fn repository(
        db_path: impl AsRef<std::path::Path>,
        namespace: &str,
        store: Option<&Store>,
    ) -> Result<Self> {
        if let Some(store) = store {
            return Self::in_store(store, namespace);
        }
        let Some(directory) = db_path.as_ref().parent() else {
            return Self::open_namespace(db_path, namespace);
        };
        let shared = shared_store(directory)?;
        let index = Self::in_store(&shared.0, namespace)?;
        *lock(&index.shared) = Some(shared);
        Ok(index)
    }

    pub fn namespace(&self) -> &str {
        &self.namespace
    }

    pub fn store(&self) -> &Store {
        &self.store
    }

    fn check_mutation(&self, mutation: &Mutation) -> Result<()> {
        if self.store.same_store(mutation.store_inner()) {
            Ok(())
        } else {
            Err(Error::msg("index mutation belongs to a different store"))
        }
    }

    fn touch(&self, mutation: &Mutation) -> Result<()> {
        mutation.touch(self.cache.clone())
    }

    /// Runs a standalone write in its own mutation.
    fn write_standalone(&self, operation: impl FnOnce(&Mutation) -> Result<()>) -> Result<()> {
        self.store.write(|mutation| {
            operation(mutation)?;
            self.touch(mutation)
        })
    }

    /// Adds one item without removing the file's existing items.
    pub fn save_item(&self, file_path: &str, key: &str, item: &T) -> Result<()> {
        self.write_standalone(|mutation| {
            mutation.with_connection(|connection| {
                let mut statement = connection.prepare_cached(
                    "INSERT INTO data (namespace, file_path, key, value) VALUES (?, ?, ?, ?)",
                )?;
                with_encoded(item, |data| {
                    statement
                        .execute(rusqlite::params![self.namespace, file_path, key, data])
                        .context("failed to save item")?;
                    Ok(())
                })
            })
        })
    }

    /// Replaces every listed source path with its items in a standalone
    /// transaction. An empty item list is a deliberate delete-only update.
    pub fn batch_save_items<P, I, K, V>(
        &self,
        items: impl IntoIterator<Item = (P, I)>,
    ) -> Result<()>
    where
        P: AsRef<str>,
        I: IntoIterator<Item = (K, V)>,
        K: AsRef<str>,
        V: std::borrow::Borrow<T>,
    {
        self.batch_save_items_in(None, items)
    }

    /// Replaces every listed source path inside `mutation`, or in a standalone
    /// transaction for `None`. Each path's existing rows are deleted before
    /// its items are inserted, so a path must appear at most once.
    pub fn batch_save_items_in<P, I, K, V>(
        &self,
        mutation: Option<&Mutation>,
        items: impl IntoIterator<Item = (P, I)>,
    ) -> Result<()>
    where
        P: AsRef<str>,
        I: IntoIterator<Item = (K, V)>,
        K: AsRef<str>,
        V: std::borrow::Borrow<T>,
    {
        let Some(mutation) = mutation else {
            return self.write_standalone(|mutation| self.save_in(mutation, items));
        };
        self.check_mutation(mutation)?;
        self.save_in(mutation, items)?;
        self.touch(mutation)
    }

    fn save_in<P, I, K, V>(
        &self,
        mutation: &Mutation,
        items: impl IntoIterator<Item = (P, I)>,
    ) -> Result<()>
    where
        P: AsRef<str>,
        I: IntoIterator<Item = (K, V)>,
        K: AsRef<str>,
        V: std::borrow::Borrow<T>,
    {
        mutation.with_connection(|connection| {
            let mut delete = connection
                .prepare_cached("DELETE FROM data WHERE namespace = ? AND file_path = ?")
                .context("failed to prepare delete data statement")?;
            // Deferred until an item actually needs encoding.
            let mut insert = None;
            for (file_path, file_items) in items {
                let file_path = file_path.as_ref();
                delete.execute([&self.namespace, file_path]).context("failed to delete existing data")?;
                for (key, item) in file_items {
                    if insert.is_none() {
                        insert = Some(
                            connection
                                .prepare_cached("INSERT INTO data (namespace, file_path, key, value) VALUES (?, ?, ?, ?)")
                                .context("failed to prepare data statement")?,
                        );
                    }
                    let statement = insert.as_mut().expect("insert statement prepared above");
                    with_encoded(item.borrow(), |data| {
                        statement
                            .execute(rusqlite::params![self.namespace, file_path, key.as_ref(), data])
                            .context("failed to save item")?;
                        Ok(())
                    })?;
                }
            }
            Ok(())
        })
    }

    /// Deletes every item of `file_path`.
    pub fn delete_by_file_path(&self, file_path: &str) -> Result<()> {
        self.batch_delete_by_file_paths(&[file_path])
    }

    /// Deletes every item of the given paths in one standalone transaction.
    pub fn batch_delete_by_file_paths<S: AsRef<str>>(&self, file_paths: &[S]) -> Result<()> {
        self.batch_delete_by_file_paths_in(None, file_paths)
    }

    pub fn batch_delete_by_file_paths_in<S: AsRef<str>>(
        &self,
        mutation: Option<&Mutation>,
        file_paths: &[S],
    ) -> Result<()> {
        if file_paths.is_empty() {
            return Ok(());
        }
        let Some(mutation) = mutation else {
            return self.write_standalone(|mutation| self.delete_in(mutation, file_paths));
        };
        self.check_mutation(mutation)?;
        self.delete_in(mutation, file_paths)?;
        self.touch(mutation)
    }

    fn delete_in<S: AsRef<str>>(&self, mutation: &Mutation, file_paths: &[S]) -> Result<()> {
        mutation.with_connection(|connection| {
            for batch in file_paths.chunks(DELETE_BATCH_SIZE) {
                let sql = in_clause_sql(
                    "DELETE FROM data WHERE namespace = ? AND file_path IN (",
                    batch.len(),
                );
                let mut statement = connection
                    .prepare_cached(&sql)
                    .context("failed to prepare data deletion")?;
                let mut arguments: Vec<&str> = Vec::with_capacity(batch.len() + 1);
                arguments.push(&self.namespace);
                arguments.extend(batch.iter().map(AsRef::as_ref));
                statement
                    .execute(rusqlite::params_from_iter(arguments))
                    .context("failed to delete data")?;
            }
            Ok(())
        })
    }

    /// Removes the whole namespace in a standalone transaction and reclaims
    /// free pages.
    pub fn clear(&self) -> Result<()> {
        self.write_standalone(|mutation| {
            self.clear_rows(mutation)?;
            mutation.with_connection(|connection| {
                connection
                    .connection()
                    .execute_batch("PRAGMA incremental_vacuum")?;
                Ok(())
            })
        })
    }

    pub fn clear_in(&self, mutation: Option<&Mutation>) -> Result<()> {
        let Some(mutation) = mutation else {
            return self.clear();
        };
        self.check_mutation(mutation)?;
        self.clear_rows(mutation)?;
        self.touch(mutation)
    }

    fn clear_rows(&self, mutation: &Mutation) -> Result<()> {
        mutation.with_connection(|connection| {
            connection
                .connection()
                .execute("DELETE FROM data WHERE namespace = ?", [&self.namespace])?;
            Ok(())
        })
    }

    /// Reports whether any item is associated with `file_path`. Inside an
    /// active mutation the lookup uses that transaction and sees its
    /// preceding writes.
    pub fn has_file_path_in(&self, mutation: Option<&Mutation>, file_path: &str) -> Result<bool> {
        const QUERY: &str = "SELECT 1 FROM data WHERE namespace = ? AND file_path = ? LIMIT 1";
        let run = |connection: &rusqlite::Connection| -> Result<bool> {
            let mut statement = connection.prepare_cached(QUERY)?;
            let exists = statement
                .exists([&self.namespace, file_path])
                .context("failed to query file association")?;
            Ok(exists)
        };
        match mutation {
            None => self.store.read(run),
            Some(mutation) => {
                self.check_mutation(mutation)?;
                mutation.with_connection(|connection| run(connection.connection()))
            }
        }
    }

    /// The immutable cached values for `key`. Read-heavy services use this
    /// view to avoid copying large catalogs for every request.
    pub fn get_values_view(&self, key: &str) -> Result<Arc<[T]>> {
        let mut cache = lock(&self.cache.state);
        if let Some(values) = cache.values.get(key) {
            return Ok(values.clone());
        }
        let values: Arc<[T]> = self
            .query_values(
                "SELECT value FROM data INDEXED BY idx_data_namespace_key WHERE namespace = ? AND key = ? ORDER BY file_path, rowid",
                &[&self.namespace, key],
                false,
            )?
            .into();
        cache.values.insert(key.to_owned(), values.clone());
        Ok(values)
    }

    /// The immutable cached repository catalog.
    pub fn get_all_values_view(&self) -> Result<Arc<[T]>> {
        let mut cache = lock(&self.cache.state);
        if let Some(values) = &cache.all {
            return Ok(values.clone());
        }
        let values: Arc<[T]> = self
            .query_values(
                "SELECT value FROM data WHERE namespace = ? ORDER BY file_path, rowid",
                &[&self.namespace],
                true,
            )?
            .into();
        cache.all = Some(values.clone());
        Ok(values)
    }

    /// The immutable snapshot of one source file's values.
    pub fn get_values_by_path_view(&self, file_path: &str) -> Result<Arc<[T]>> {
        let mut cache = lock(&self.cache.state);
        if let Some(values) = cache.path_values.get(file_path) {
            return Ok(values.clone());
        }
        let values: Arc<[T]> = self
            .query_values(
                "SELECT value FROM data WHERE namespace = ? AND file_path = ?",
                &[&self.namespace, file_path],
                false,
            )?
            .into();
        cache
            .path_values
            .insert(file_path.to_owned(), values.clone());
        Ok(values)
    }

    fn query_values(&self, sql: &str, arguments: &[&str], skip_empty: bool) -> Result<Vec<T>> {
        self.store.read(|connection| {
            let mut statement = connection
                .prepare_cached(sql)
                .context("failed to query data")?;
            let mut rows = statement.query(rusqlite::params_from_iter(arguments))?;
            let mut values = Vec::new();
            while let Some(row) = rows.next()? {
                let data = blob(row.get_ref(0)?);
                if skip_empty && data.is_empty() {
                    continue;
                }
                values.push(decode_value(data)?);
            }
            Ok(values)
        })
    }

    /// Decodes every value without retaining the result in the repository
    /// cache, for startup restoration into a compact representation.
    pub fn visit_all_values(&self, mut visitor: impl FnMut(T) -> Result<()>) -> Result<()> {
        self.store.read(|connection| {
            let mut statement =
                connection.prepare_cached("SELECT value FROM data WHERE namespace = ?")?;
            let mut rows = statement
                .query([&self.namespace])
                .context("failed to query data")?;
            while let Some(row) = rows.next()? {
                let data = blob(row.get_ref(0)?);
                if data.is_empty() {
                    continue;
                }
                visitor(decode_value(data)?)?;
            }
            Ok(())
        })
    }

    /// Visits each encoded value without decoding or copying it. The slice is
    /// borrowed from the SQLite row and valid only during the call.
    pub fn visit_all_encoded_values(
        &self,
        mut visitor: impl FnMut(&[u8]) -> Result<()>,
    ) -> Result<()> {
        self.store.read(|connection| {
            let mut statement = connection
                .prepare_cached("SELECT file_path, key, value FROM data WHERE namespace = ?")?;
            let mut rows = statement
                .query([&self.namespace])
                .context("failed to query data")?;
            while let Some(row) = rows.next()? {
                let data = blob(row.get_ref(2)?);
                if data.is_empty() {
                    continue;
                }
                if let Err(error) = visitor(data) {
                    let file_path: String = row.get(0)?;
                    let key: String = row.get(1)?;
                    return Err(error.context(format!("visit encoded value {file_path} ({key})")));
                }
            }
            Ok(())
        })
    }

    /// Visits the encoded values of one source path without populating the
    /// path cache.
    pub fn visit_encoded_values_by_path(
        &self,
        file_path: &str,
        mut visitor: impl FnMut(&[u8]) -> Result<()>,
    ) -> Result<()> {
        self.store.read(|connection| {
            let mut statement = connection.prepare_cached(
                "SELECT key, value FROM data WHERE namespace = ? AND file_path = ?",
            )?;
            let mut rows = statement
                .query([&self.namespace, file_path])
                .context("failed to query encoded values by path")?;
            while let Some(row) = rows.next()? {
                let data = blob(row.get_ref(1)?);
                if data.is_empty() {
                    continue;
                }
                if let Err(error) = visitor(data) {
                    let key: String = row.get(0)?;
                    return Err(error.context(format!("visit encoded value {file_path} ({key})")));
                }
            }
            Ok(())
        })
    }

    /// The number of values in this namespace.
    pub fn count_all_values(&self) -> Result<usize> {
        self.store.read(|connection| {
            let count: i64 = connection
                .prepare_cached("SELECT COUNT(*) FROM data WHERE namespace = ?")?
                .query_row([&self.namespace], |row| row.get(0))
                .context("failed to count data")?;
            Ok(usize::try_from(count).unwrap_or_default())
        })
    }

    /// Every distinct key (cached).
    pub fn get_all_keys(&self) -> Result<Vec<String>> {
        self.get_all_keys_view().map(|keys| keys.to_vec())
    }

    pub fn get_all_keys_view(&self) -> Result<Arc<[String]>> {
        let mut cache = lock(&self.cache.state);
        if let Some(keys) = &cache.keys {
            return Ok(keys.clone());
        }
        let keys: Arc<[String]> = self
            .query_strings(
                "SELECT DISTINCT key FROM data WHERE namespace = ?",
                &[&self.namespace],
            )?
            .into();
        cache.keys = Some(keys.clone());
        Ok(keys)
    }

    /// Every distinct key of one source path (cached).
    pub fn get_all_keys_by_path(&self, file_path: &str) -> Result<Vec<String>> {
        let mut cache = lock(&self.cache.state);
        if let Some(keys) = cache.path_keys.get(file_path) {
            return Ok(keys.to_vec());
        }
        let keys = self.query_strings(
            "SELECT DISTINCT key FROM data WHERE namespace = ? AND file_path = ?",
            &[&self.namespace, file_path],
        )?;
        cache
            .path_keys
            .insert(file_path.to_owned(), keys.clone().into());
        Ok(keys)
    }

    /// Every source path currently represented in the repository.
    pub fn get_all_file_paths(&self) -> Result<Vec<String>> {
        self.query_strings(
            "SELECT DISTINCT file_path FROM data WHERE namespace = ?",
            &[&self.namespace],
        )
    }

    fn query_strings(&self, sql: &str, arguments: &[&str]) -> Result<Vec<String>> {
        self.store.read(|connection| {
            let mut statement = connection
                .prepare_cached(sql)
                .context("failed to query keys")?;
            let rows = statement.query_map(rusqlite::params_from_iter(arguments), |row| {
                row.get::<_, String>(0)
            })?;
            rows.collect::<rusqlite::Result<Vec<_>>>()
                .map_err(Error::from)
        })
    }

    /// Reports whether the repository contains a key other than the supplied
    /// case-insensitive exclusions, without materializing the key catalog.
    pub fn has_any_key_except_fold(&self, excluded: &[&str]) -> Result<bool> {
        let mut normalized: Vec<String> = excluded
            .iter()
            .map(|key| key.trim().to_lowercase())
            .filter(|key| !key.is_empty())
            .collect();
        normalized.sort();
        normalized.dedup();
        let cache = lock(&self.cache.state);
        if let Some(keys) = &cache.keys {
            return Ok(keys
                .iter()
                .any(|key| !normalized.contains(&key.to_lowercase())));
        }
        let mut sql = String::from("SELECT 1 FROM data WHERE namespace = ?");
        if !normalized.is_empty() {
            sql = in_clause_sql(&format!("{sql} AND LOWER(key) NOT IN ("), normalized.len());
        }
        sql.push_str(" LIMIT 1");
        let mut arguments: Vec<&str> = vec![&self.namespace];
        arguments.extend(normalized.iter().map(String::as_str));
        self.store.read(|connection| {
            let mut statement = connection.prepare_cached(&sql)?;
            statement
                .exists(rusqlite::params_from_iter(arguments))
                .context("failed to query key existence")
        })
    }

    /// Invalidates caches and closes an owned store. Idempotent.
    pub fn close(&self) -> Result<()> {
        if self.closed.swap(true, Ordering::AcqRel) {
            return Ok(());
        }
        self.cache.invalidate_mutation_cache();
        if self.owns_store {
            return self.store.close();
        }
        // The last repository of a shared directory store closes it and
        // reports its error.
        let shared = lock(&self.shared).take();
        match shared.map(Arc::try_unwrap) {
            Some(Ok(shared)) => shared.0.close(),
            _ => Ok(()),
        }
    }

    /// Starts a standalone mutation on this repository's store.
    pub fn begin_mutation(&self, cancel: &CancelToken) -> Result<Mutation> {
        self.store.begin_mutation(cancel)
    }
}

impl<T> DataIndexer<T>
where
    T: Serialize + DeserializeOwned + Clone + Send + Sync + 'static,
{
    /// All items with `key` (copied from the cached view).
    pub fn get_values(&self, key: &str) -> Result<Vec<T>> {
        self.get_values_view(key).map(|values| values.to_vec())
    }

    /// All items of the namespace (copied from the cached view).
    pub fn get_all_values(&self) -> Result<Vec<T>> {
        self.get_all_values_view().map(|values| values.to_vec())
    }

    /// The items of one source path (copied from the cached view).
    pub fn get_values_by_path(&self, file_path: &str) -> Result<Vec<T>> {
        self.get_values_by_path_view(file_path)
            .map(|values| values.to_vec())
    }
}

fn blob(value: ValueRef<'_>) -> &[u8] {
    match value {
        ValueRef::Blob(bytes) | ValueRef::Text(bytes) => bytes,
        _ => &[],
    }
}

pub(crate) fn in_clause_sql(prefix: &str, count: usize) -> String {
    let mut sql = String::with_capacity(prefix.len() + count * 2 + 1);
    sql.push_str(prefix);
    for index in 0..count {
        if index != 0 {
            sql.push(',');
        }
        sql.push('?');
    }
    sql.push(')');
    sql
}
