//! Compact, SQLite FTS-backed workspace symbol catalog. Interactive queries
//! do not need to restore any language-specific object graph.

use std::path::Path;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};

use rusqlite::{Connection, OpenFlags};
use serde::{Deserialize, Serialize};
use swlsp_syntax::TextRange;

use crate::error::{CancelToken, Error, Result, ResultExt};
use crate::store::lock;
use crate::{Mutation, ParsedFile, Store};

/// The numeric LSP `SymbolKind` values, without coupling persistence to the
/// protocol crate.
#[derive(
    Debug, Clone, Copy, Default, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize,
)]
#[serde(transparent)]
pub struct WorkspaceSymbolKind(pub i32);

impl WorkspaceSymbolKind {
    pub const FILE: Self = Self(1);
    pub const MODULE: Self = Self(2);
    pub const NAMESPACE: Self = Self(3);
    pub const PACKAGE: Self = Self(4);
    pub const CLASS: Self = Self(5);
    pub const METHOD: Self = Self(6);
    pub const PROPERTY: Self = Self(7);
    pub const FIELD: Self = Self(8);
    pub const CONSTRUCTOR: Self = Self(9);
    pub const ENUM: Self = Self(10);
    pub const INTERFACE: Self = Self(11);
    pub const FUNCTION: Self = Self(12);
    pub const VARIABLE: Self = Self(13);
    pub const CONSTANT: Self = Self(14);
    pub const STRING: Self = Self(15);
    pub const NUMBER: Self = Self(16);
    pub const BOOLEAN: Self = Self(17);
    pub const ARRAY: Self = Self(18);
    pub const OBJECT: Self = Self(19);
    pub const KEY: Self = Self(20);
    pub const NULL: Self = Self(21);
    pub const ENUM_MEMBER: Self = Self(22);
    pub const STRUCT: Self = Self(23);
    pub const EVENT: Self = Self(24);
    pub const OPERATOR: Self = Self(25);
    pub const TYPE_PARAMETER: Self = Self(26);
}

// Priorities apply after textual relevance: equally good type matches sort
// above members and framework aliases.
pub const WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER: i32 = 50;
pub const WORKSPACE_SYMBOL_PRIORITY_PHP_MEMBER: i32 = 60;
pub const WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK: i32 = 70;
pub const WORKSPACE_SYMBOL_PRIORITY_PHP_GLOBAL: i32 = 80;
pub const WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE: i32 = 100;

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct WorkspaceSymbolPosition {
    pub line: i32,
    pub character: i32,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct WorkspaceSymbolRange {
    pub start: WorkspaceSymbolPosition,
    pub end: WorkspaceSymbolPosition,
}

impl WorkspaceSymbolRange {
    /// Converts a byte range of `file` to zero-based UTF-16 positions. An
    /// empty range at offset zero is the "unknown" range.
    pub fn from_text(file: &ParsedFile, range: TextRange) -> Self {
        if range == TextRange::default() {
            return Self::default();
        }
        let index = file.line_index();
        let (start_line, start_character) = index.position_utf16(range.start().into());
        let (end_line, end_character) = index.position_utf16(range.end().into());
        Self {
            start: WorkspaceSymbolPosition {
                line: start_line as i32,
                character: start_character as i32,
            },
            end: WorkspaceSymbolPosition {
                line: end_line as i32,
                character: end_character as i32,
            },
        }
    }

    /// Converts the one-based lines of legacy framework records into
    /// zero-based LSP positions.
    pub fn at_line(line: i32) -> Self {
        let position = WorkspaceSymbolPosition {
            line: line.max(1) - 1,
            character: 0,
        };
        Self {
            start: position,
            end: position,
        }
    }
}

/// The language-neutral persisted workspace symbol. Aliases are searchable
/// but not returned to LSP clients.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct WorkspaceSymbol {
    pub name: String,
    pub container_name: String,
    pub aliases: Vec<String>,
    /// The declaration's location when it differs from the owning document.
    pub path: String,
    pub domain: String,
    pub kind: WorkspaceSymbolKind,
    pub priority: i32,
    pub range: WorkspaceSymbolRange,
}

/// All symbols owned by one source path.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct WorkspaceSymbolDocument {
    pub path: String,
    pub symbols: Vec<WorkspaceSymbol>,
}

/// A symbol with its derived catalog columns. Deriving them splits and
/// lowercases every identifier, so the scanner does it during preparation
/// instead of inside the write transaction.
#[derive(Debug, Clone)]
pub(crate) struct WorkspaceSymbolRow {
    symbol: WorkspaceSymbol,
    aliases: String,
    search_text: String,
}

impl WorkspaceSymbolRow {
    /// The rows of `symbols`; unnamed symbols are not catalogued.
    pub(crate) fn of_all(symbols: impl IntoIterator<Item = WorkspaceSymbol>) -> Vec<Self> {
        symbols
            .into_iter()
            .filter(|symbol| !symbol.name.is_empty())
            .map(|symbol| Self {
                aliases: workspace_symbol_aliases(&symbol.aliases),
                search_text: workspace_symbol_index_text(&symbol),
                symbol,
            })
            .collect()
    }
}

/// The rows owned by one source path, in two runs so the scanner can append
/// prepared rows to the ones an indexer published while writing.
pub(crate) struct WorkspaceSymbolRows<'a> {
    pub path: &'a str,
    pub rows: [&'a [WorkspaceSymbolRow]; 2],
}

#[derive(Debug, Clone, Default)]
pub struct WorkspaceSymbolQueryOptions {
    pub excluded_domains: Vec<String>,
}

const WORKSPACE_SYMBOL_SCHEMA: &str = "
    CREATE TABLE IF NOT EXISTS workspace_symbols (
        id INTEGER PRIMARY KEY,
        owner_id INTEGER NOT NULL,
        file_path TEXT NOT NULL DEFAULT '',
        name TEXT NOT NULL,
        container_name TEXT NOT NULL DEFAULT '',
        aliases TEXT NOT NULL DEFAULT '',
        search_text TEXT NOT NULL,
        domain TEXT NOT NULL DEFAULT '',
        kind INTEGER NOT NULL,
        priority INTEGER NOT NULL,
        start_line INTEGER NOT NULL,
        start_character INTEGER NOT NULL,
        end_line INTEGER NOT NULL,
        end_character INTEGER NOT NULL
    );
    CREATE INDEX IF NOT EXISTS idx_workspace_symbols_owner
        ON workspace_symbols(owner_id);
    CREATE VIRTUAL TABLE IF NOT EXISTS workspace_symbols_fts USING fts4(
        search_text,
        content='workspace_symbols',
        tokenize=unicode61
    );
    CREATE TABLE IF NOT EXISTS workspace_symbol_meta (
        key TEXT PRIMARY KEY,
        value INTEGER NOT NULL
    );
";

/// The candidate order of queries. Its keys arrive in random order, so a
/// cold index builds it once at the end instead of maintaining it per row
/// (see [`WorkspaceSymbolCatalog::begin_bulk_population`]).
const PRIORITY_INDEX: &str = "CREATE INDEX IF NOT EXISTS idx_workspace_symbols_priority
    ON workspace_symbols(priority DESC, name)";
const DELETE_BATCH_SIZE: usize = 128;
const ALIAS_SEPARATOR: char = '\u{1f}';

enum Backend {
    Store(Store),
    ReadOnly(Mutex<Option<Connection>>),
}

/// The workspace symbol catalog stored in the shared workspace store.
pub struct WorkspaceSymbolCatalog {
    backend: Backend,
    bulk: AtomicBool,
    bulk_dirty: AtomicBool,
    /// The priority index was dropped for the current bulk population.
    index_deferred: AtomicBool,
}

impl std::fmt::Debug for WorkspaceSymbolCatalog {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("WorkspaceSymbolCatalog")
            .field("bulk", &self.bulk.load(Ordering::Relaxed))
            .finish()
    }
}

impl WorkspaceSymbolCatalog {
    /// Creates (or migrates) the catalog in a writable store.
    pub fn new(store: &Store) -> Result<Self> {
        store
            .write(|mutation| {
                mutation.with_connection(|connection| {
                    let connection = connection.connection();
                    if catalog_uses_path_text(connection)? {
                        connection.execute_batch(
                            "DROP TABLE IF EXISTS workspace_symbols_fts;
                             DROP TABLE IF EXISTS workspace_symbols;
                             DROP TABLE IF EXISTS workspace_symbol_meta;",
                        )?;
                    }
                    connection.execute_batch(WORKSPACE_SYMBOL_SCHEMA)?;
                    // Also restores the index an interrupted cold run dropped.
                    connection.execute_batch(PRIORITY_INDEX)?;
                    Ok(())
                })
            })
            .context("initialize workspace symbol catalog")?;
        Ok(Self {
            backend: Backend::Store(store.clone()),
            bulk: AtomicBool::new(false),
            bulk_dirty: AtomicBool::new(false),
            index_deferred: AtomicBool::new(false),
        })
    }

    /// Opens an existing catalog read-only without creating or migrating it,
    /// for lightweight CLI reads before a workspace exists.
    pub fn open_read_only(db_path: impl AsRef<Path>) -> Result<Self> {
        let connection = Connection::open_with_flags(
            db_path.as_ref(),
            OpenFlags::SQLITE_OPEN_READ_ONLY
                | OpenFlags::SQLITE_OPEN_NO_MUTEX
                | OpenFlags::SQLITE_OPEN_URI,
        )
        .context("open workspace symbol catalog")?;
        connection
            .execute_batch("PRAGMA query_only=1")
            .context("open workspace symbol catalog")?;
        let exists: i64 = connection
            .query_row(
                "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'workspace_symbols_fts'",
                [],
                |row| row.get(0),
            )
            .context("inspect workspace symbol catalog")?;
        if exists == 0 {
            return Err(Error::msg("workspace symbol catalog is not initialized"));
        }
        Ok(Self {
            backend: Backend::ReadOnly(Mutex::new(Some(connection))),
            bulk: AtomicBool::new(false),
            bulk_dirty: AtomicBool::new(false),
            index_deferred: AtomicBool::new(false),
        })
    }

    /// Closes a read-only catalog's own connection. A store-backed catalog
    /// does not own its store.
    pub fn close(&self) -> Result<()> {
        if let Backend::ReadOnly(connection) = &self.backend
            && let Some(connection) = lock(connection).take()
        {
            connection
                .close()
                .map_err(|(_, error)| Error::from(error))?;
        }
        Ok(())
    }

    fn read<R>(&self, operation: impl FnOnce(&Connection) -> Result<R>) -> Result<R> {
        match &self.backend {
            Backend::Store(store) => store.read(operation),
            Backend::ReadOnly(connection) => {
                let connection = lock(connection);
                let connection = connection
                    .as_ref()
                    .ok_or_else(|| Error::msg("workspace symbol catalog is closed"))?;
                operation(connection)
            }
        }
    }

    fn store(&self) -> Result<&Store> {
        match &self.backend {
            Backend::Store(store) => Ok(store),
            Backend::ReadOnly(_) => {
                Err(Error::msg("writable workspace symbol catalog is required"))
            }
        }
    }

    fn in_mutation(
        &self,
        cancel: &CancelToken,
        operation: impl FnOnce(&Mutation) -> Result<()>,
    ) -> Result<()> {
        let mutation = self.store()?.begin_mutation(cancel)?;
        if let Err(error) = operation(&mutation) {
            let rollback = mutation.rollback().err();
            return Err(Error::join([Some(error), rollback].into_iter().flatten())
                .expect("at least one error"));
        }
        mutation.commit()
    }

    /// Defers FTS maintenance while a cold index writes many small file
    /// transactions; [`end_bulk_population`](Self::end_bulk_population)
    /// builds the inverted index once. Populating an empty catalog also
    /// defers the priority index.
    pub fn begin_bulk_population(&self) {
        self.bulk_dirty.store(false, Ordering::Release);
        self.bulk.store(true, Ordering::Release);
        let deferred = self.defer_priority_index().unwrap_or(false);
        self.index_deferred.store(deferred, Ordering::Release);
    }

    /// Drops the priority index of an empty catalog. A populated catalog
    /// keeps it: rebuilding it would cost a warm start more than the few
    /// changed files save.
    fn defer_priority_index(&self) -> Result<bool> {
        let Ok(store) = self.store() else {
            return Ok(false);
        };
        let empty = self.read(|connection| {
            connection
                .query_row(
                    "SELECT NOT EXISTS (SELECT 1 FROM workspace_symbols)",
                    [],
                    |row| row.get::<_, bool>(0),
                )
                .context("inspect workspace symbol catalog")
        })?;
        if !empty {
            return Ok(false);
        }
        store.write(|mutation| {
            mutation.with_connection(|connection| {
                connection
                    .connection()
                    .execute_batch("DROP INDEX IF EXISTS idx_workspace_symbols_priority")
                    .context("defer workspace symbol priority index")
            })
        })?;
        Ok(true)
    }

    /// Forces the deferred FTS rebuild even when the run indexes no file: an
    /// interrupted run leaves the catalog populated but unrebuilt.
    pub fn require_bulk_rebuild(&self) {
        self.bulk_dirty.store(true, Ordering::Release);
    }

    pub fn end_bulk_population(&self, cancel: &CancelToken) -> Result<()> {
        if self.store().is_err() {
            return Ok(());
        }
        self.bulk.store(false, Ordering::Release);
        let dirty = self.bulk_dirty.swap(false, Ordering::AcqRel);
        let deferred = self.index_deferred.swap(false, Ordering::AcqRel);
        if !dirty && !deferred {
            return Ok(());
        }
        self.in_mutation(cancel, |mutation| {
            mutation.with_connection(|connection| {
                let connection = connection.connection();
                if deferred {
                    connection
                        .execute_batch(PRIORITY_INDEX)
                        .context("build workspace symbol priority index")?;
                }
                if dirty {
                    connection
                        .execute_batch("INSERT INTO workspace_symbols_fts(workspace_symbols_fts) VALUES('rebuild')")
                        .context("rebuild workspace symbol FTS")?;
                }
                Ok(())
            })
        })
    }

    /// Whether the catalog describes a complete index generation.
    pub fn ready(&self) -> Result<bool> {
        self.read(|connection| {
            let value: Option<i64> = connection
                .query_row(
                    "SELECT value FROM workspace_symbol_meta WHERE key = 'ready'",
                    [],
                    |row| row.get(0),
                )
                .map(Some)
                .or_else(|error| match error {
                    rusqlite::Error::QueryReturnedNoRows => Ok(None),
                    other => Err(other),
                })
                .context("query workspace symbol readiness")?;
            Ok(value.is_some_and(|value| value != 0))
        })
    }

    pub fn set_ready(&self, cancel: &CancelToken, ready: bool) -> Result<()> {
        self.in_mutation(cancel, |mutation| self.set_ready_in(mutation, ready))
    }

    pub fn set_ready_in(&self, mutation: &Mutation, ready: bool) -> Result<()> {
        mutation.with_connection(|connection| {
            connection
                .prepare_cached(
                    "INSERT INTO workspace_symbol_meta(key, value) VALUES('ready', ?)
                     ON CONFLICT(key) DO UPDATE SET value = excluded.value",
                )?
                .execute([i64::from(ready)])?;
            Ok(())
        })
    }

    pub fn replace_files(
        &self,
        cancel: &CancelToken,
        files: &[WorkspaceSymbolDocument],
    ) -> Result<()> {
        if files.is_empty() {
            return Ok(());
        }
        self.store()?;
        self.in_mutation(cancel, |mutation| self.replace_files_in(mutation, files))
    }

    pub fn delete_files(&self, cancel: &CancelToken, paths: &[String]) -> Result<()> {
        if paths.is_empty() {
            return Ok(());
        }
        self.store()?;
        self.in_mutation(cancel, |mutation| self.delete_files_in(mutation, paths))
    }

    pub fn clear(&self, cancel: &CancelToken) -> Result<()> {
        self.store()?;
        if self.bulk.load(Ordering::Acquire) {
            self.bulk_dirty.store(true, Ordering::Release);
        }
        self.in_mutation(cancel, |mutation| self.clear_in(mutation))
    }

    /// Replaces every document's symbols inside `mutation`.
    pub fn replace_files_in(
        &self,
        mutation: &Mutation,
        files: &[WorkspaceSymbolDocument],
    ) -> Result<()> {
        let rows: Vec<Vec<WorkspaceSymbolRow>> = files
            .iter()
            .map(|file| WorkspaceSymbolRow::of_all(file.symbols.iter().cloned()))
            .collect();
        let files: Vec<WorkspaceSymbolRows<'_>> = files
            .iter()
            .zip(&rows)
            .map(|(file, rows)| WorkspaceSymbolRows {
                path: &file.path,
                rows: [rows.as_slice(), &[]],
            })
            .collect();
        self.replace_rows_in(mutation, &files)
    }

    /// Replaces every document's symbols inside `mutation` with rows whose
    /// derived columns were computed ahead of the transaction.
    pub(crate) fn replace_rows_in(
        &self,
        mutation: &Mutation,
        files: &[WorkspaceSymbolRows<'_>],
    ) -> Result<()> {
        let paths: Vec<&str> = files
            .iter()
            .map(|file| file.path)
            .filter(|path| !path.is_empty())
            .collect();
        let bulk = self.bulk.load(Ordering::Acquire);
        if bulk && !paths.is_empty() {
            self.bulk_dirty.store(true, Ordering::Release);
        }
        self.delete_files_in(mutation, &paths)?;

        mutation.with_connection(|connection| {
            for file in files {
                if file.path.is_empty() || file.rows.iter().all(|rows| rows.is_empty()) {
                    continue;
                }
                let owner_id = connection.path_id(file.path)?;
                let mut insert = connection.prepare_cached(WORKSPACE_SYMBOL_INSERT)?;
                let mut index = (!bulk)
                    .then(|| connection.prepare_cached(WORKSPACE_SYMBOL_FTS_INSERT))
                    .transpose()?;
                for row in file.rows.iter().flat_map(|rows| rows.iter()) {
                    let symbol = &row.symbol;
                    let location_path = if symbol.path == file.path {
                        ""
                    } else {
                        symbol.path.as_str()
                    };
                    insert
                        .execute(rusqlite::params![
                            owner_id,
                            location_path,
                            symbol.name,
                            symbol.container_name,
                            row.aliases,
                            row.search_text,
                            symbol.domain,
                            i64::from(symbol.kind.0),
                            i64::from(symbol.priority),
                            i64::from(symbol.range.start.line.max(0)),
                            i64::from(symbol.range.start.character.max(0)),
                            i64::from(symbol.range.end.line.max(0)),
                            i64::from(symbol.range.end.character.max(0)),
                        ])
                        .context("insert workspace symbol")?;
                    if let Some(index) = &mut index {
                        index
                            .execute(rusqlite::params![
                                connection.connection().last_insert_rowid(),
                                row.search_text,
                            ])
                            .context("index workspace symbol")?;
                    }
                }
            }
            Ok(())
        })
    }

    /// Deletes the symbols owned by `paths` inside `mutation`.
    pub fn delete_files_in<S: AsRef<str>>(&self, mutation: &Mutation, paths: &[S]) -> Result<()> {
        if paths.is_empty() {
            return Ok(());
        }
        let bulk = self.bulk.load(Ordering::Acquire);
        mutation.release_paths();
        mutation.with_connection(|connection| {
            for batch in paths.chunks(DELETE_BATCH_SIZE) {
                let placeholders = vec!["?"; batch.len()].join(",");
                let arguments = batch.iter().map(AsRef::as_ref);
                if !bulk {
                    connection
                        .prepare_cached(&format!(
                            "DELETE FROM workspace_symbols_fts WHERE docid IN (SELECT id FROM workspace_symbols WHERE owner_id IN (SELECT id FROM paths WHERE path IN ({placeholders})))"
                        ))?
                        .execute(rusqlite::params_from_iter(arguments.clone()))
                        .context("delete workspace symbol search rows")?;
                }
                connection
                    .prepare_cached(&format!("DELETE FROM workspace_symbols WHERE owner_id IN (SELECT id FROM paths WHERE path IN ({placeholders}))"))?
                    .execute(rusqlite::params_from_iter(arguments))
                    .context("delete workspace symbols")?;
            }
            Ok(())
        })
    }

    pub fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        mutation.release_paths();
        mutation.with_connection(|connection| {
            connection.connection().execute_batch(
                "DELETE FROM workspace_symbols_fts; DELETE FROM workspace_symbols; DELETE FROM workspace_symbol_meta;",
            )?;
            Ok(())
        })
    }

    pub fn query(&self, query: &str, limit: usize) -> Result<Vec<WorkspaceSymbol>> {
        self.query_with_options(query, limit, &WorkspaceSymbolQueryOptions::default())
    }

    /// Ranked symbols matching `query`: FTS candidates ordered by priority,
    /// re-ranked by textual relevance, then priority, name, container, path.
    pub fn query_with_options(
        &self,
        query: &str,
        limit: usize,
        options: &WorkspaceSymbolQueryOptions,
    ) -> Result<Vec<WorkspaceSymbol>> {
        if limit == 0 {
            return Ok(Vec::new());
        }
        let query = query.trim();
        let candidate_limit = (limit.saturating_mul(8)).clamp(1000, 8000) as i64;
        let mut sql = String::from(
            "SELECT s.name, s.container_name, s.aliases,
                CASE WHEN s.file_path = '' THEN o.path ELSE s.file_path END,
                s.domain, s.kind, s.priority, s.start_line, s.start_character,
                s.end_line, s.end_character
            FROM workspace_symbols s JOIN paths o ON o.id = s.owner_id",
        );
        let (domain_predicate, domain_arguments) =
            workspace_symbol_domain_exclusion(&options.excluded_domains);
        let mut arguments: Vec<rusqlite::types::Value> = Vec::new();
        if query.is_empty() {
            if !domain_predicate.is_empty() {
                sql.push_str(" WHERE ");
                sql.push_str(&domain_predicate);
            }
        } else {
            let matched = workspace_symbol_match_query(query);
            if matched.is_empty() {
                return Ok(Vec::new());
            }
            sql.push_str(" JOIN workspace_symbols_fts f ON f.docid = s.id WHERE workspace_symbols_fts MATCH ?");
            arguments.push(matched.into());
            if !domain_predicate.is_empty() {
                sql.push_str(" AND ");
                sql.push_str(&domain_predicate);
            }
        }
        arguments.extend(domain_arguments.into_iter().map(Into::into));
        // Ties (same name) are broken by location so results do not depend
        // on storage order.
        sql.push_str(
            " ORDER BY s.priority DESC, s.name, o.path, s.file_path, s.start_line, s.start_character, s.id LIMIT ?",
        );
        arguments.push(candidate_limit.into());

        let mut ranked: Vec<(usize, WorkspaceSymbol)> = self
            .read(|connection| {
                let mut statement = connection.prepare_cached(&sql)?;
                let rows = statement.query_map(rusqlite::params_from_iter(arguments), |row| {
                    let aliases: String = row.get(2)?;
                    Ok(WorkspaceSymbol {
                        name: row.get(0)?,
                        container_name: row.get(1)?,
                        aliases: if aliases.is_empty() {
                            Vec::new()
                        } else {
                            aliases.split(ALIAS_SEPARATOR).map(str::to_owned).collect()
                        },
                        path: row.get(3)?,
                        domain: row.get(4)?,
                        kind: WorkspaceSymbolKind(row.get(5)?),
                        priority: row.get(6)?,
                        range: WorkspaceSymbolRange {
                            start: WorkspaceSymbolPosition {
                                line: row.get(7)?,
                                character: row.get(8)?,
                            },
                            end: WorkspaceSymbolPosition {
                                line: row.get(9)?,
                                character: row.get(10)?,
                            },
                        },
                    })
                })?;
                rows.collect::<rusqlite::Result<Vec<_>>>()
                    .map_err(Error::from)
            })
            .context("query workspace symbols")?
            .into_iter()
            .map(|symbol| (workspace_symbol_text_score(query, &symbol), symbol))
            .collect();
        ranked.sort_by(|(left_score, left), (right_score, right)| {
            left_score
                .cmp(right_score)
                .then_with(|| right.priority.cmp(&left.priority))
                .then_with(|| left.name.to_lowercase().cmp(&right.name.to_lowercase()))
                .then_with(|| left.container_name.cmp(&right.container_name))
                .then_with(|| left.path.cmp(&right.path))
        });
        ranked.truncate(limit);
        Ok(ranked.into_iter().map(|(_, symbol)| symbol).collect())
    }
}

const WORKSPACE_SYMBOL_INSERT: &str = "INSERT INTO workspace_symbols(
        owner_id, file_path, name, container_name, aliases, search_text, domain,
        kind, priority, start_line, start_character, end_line, end_character
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)";

const WORKSPACE_SYMBOL_FTS_INSERT: &str =
    "INSERT INTO workspace_symbols_fts(docid, search_text) VALUES (?, ?)";

/// Trimmed, deduplicated aliases joined by the unit separator.
fn workspace_symbol_aliases(aliases: &[String]) -> String {
    let mut result = String::new();
    let mut seen: Vec<&str> = Vec::new();
    for alias in aliases {
        let alias = alias.trim();
        if alias.is_empty() || seen.contains(&alias) {
            continue;
        }
        if !seen.is_empty() {
            result.push(ALIAS_SEPARATOR);
        }
        seen.push(alias);
        result.push_str(alias);
    }
    result
}

fn workspace_symbol_domain_exclusion(domains: &[String]) -> (String, Vec<String>) {
    let mut ordered: Vec<String> = domains
        .iter()
        .map(|domain| domain.trim())
        .filter(|domain| !domain.is_empty())
        .map(str::to_owned)
        .collect();
    ordered.sort();
    ordered.dedup();
    if ordered.is_empty() {
        return (String::new(), Vec::new());
    }
    (
        format!("s.domain NOT IN ({})", vec!["?"; ordered.len()].join(",")),
        ordered,
    )
}

fn workspace_symbol_text_score(query: &str, symbol: &WorkspaceSymbol) -> usize {
    let query = query.trim().to_lowercase();
    if query.is_empty() {
        return 4;
    }
    let mut best = 5;
    let values = [symbol.name.as_str(), symbol.container_name.as_str()]
        .into_iter()
        .chain(symbol.aliases.iter().map(String::as_str));
    for value in values {
        let value = value.to_lowercase();
        let score = if value == query {
            0
        } else if value.starts_with(&query) {
            1
        } else if value.contains(&query) {
            2
        } else {
            3
        };
        best = best.min(score);
    }
    best
}

fn workspace_symbol_match_query(query: &str) -> String {
    let terms = identifier_words(query);
    if terms.is_empty() {
        return String::new();
    }
    if query
        .chars()
        .all(|character| character.is_alphabetic() || character.is_numeric())
    {
        return format!("{}*", terms.concat().to_lowercase());
    }
    // Identifier words never contain FTS operators or punctuation, so each
    // can use SQLite's unquoted prefix form.
    terms
        .iter()
        .map(|term| format!("{}*", term.to_lowercase()))
        .collect::<Vec<_>>()
        .join(" AND ")
}

pub(crate) fn workspace_symbol_index_text(symbol: &WorkspaceSymbol) -> String {
    let mut text = SymbolText::default();
    text.add(&symbol.name);
    let container = workspace_symbol_leaf_name(&symbol.container_name);
    if !container.is_empty() {
        text.add(container);
    }
    for alias in &symbol.aliases {
        let leaf = workspace_symbol_leaf_name(alias);
        if !leaf.is_empty() {
            text.add(leaf);
        }
    }
    text.finish()
}

/// The last non-empty segment separated by `\`, `/`, `·` or `:`.
pub(crate) fn workspace_symbol_leaf_name(value: &str) -> &str {
    let mut segment_start = 0;
    let mut last: Option<(usize, usize)> = None;
    for (index, character) in value.char_indices() {
        if matches!(character, '\\' | '/' | '·' | ':') {
            if segment_start < index {
                last = Some((segment_start, index));
            }
            segment_start = index + character.len_utf8();
        }
    }
    if segment_start < value.len() {
        last = Some((segment_start, value.len()));
    }
    match last {
        Some((start, end)) => value[start..end].trim(),
        None => value.trim(),
    }
}

#[cfg(test)]
pub(crate) fn workspace_symbol_search_text(values: &[&str]) -> String {
    let mut text = SymbolText::default();
    for value in values {
        text.add(value);
    }
    text.finish()
}

/// Deduplicated, ordered search terms: every identifier word plus every
/// word-boundary suffix of each identifier, so `ConfigService` matches
/// `SystemConfigService` through FTS prefix queries.
#[derive(Default)]
struct SymbolText {
    values: Vec<String>,
}

impl SymbolText {
    fn add(&mut self, value: &str) {
        let mut start = None;
        for (index, character) in value.char_indices() {
            if character.is_alphabetic() || character.is_numeric() {
                start.get_or_insert(index);
                continue;
            }
            if let Some(chunk_start) = start.take() {
                self.add_chunk(&value[chunk_start..index]);
            }
        }
        if let Some(chunk_start) = start {
            self.add_chunk(&value[chunk_start..]);
        }
    }

    fn add_chunk(&mut self, chunk: &str) {
        let words: Vec<String> = identifier_words(chunk)
            .into_iter()
            .map(|word| word.to_lowercase())
            .collect();
        for word in &words {
            self.push(word);
        }
        if words.len() < 2 {
            return;
        }
        let joined = words.concat();
        let mut offset = 0;
        for word in &words[..words.len() - 1] {
            self.push(&joined[offset..]);
            offset += word.len();
        }
    }

    fn push(&mut self, value: &str) {
        if !value.is_empty() && !self.values.iter().any(|existing| existing == value) {
            self.values.push(value.to_owned());
        }
    }

    fn finish(self) -> String {
        self.values.join(" ")
    }
}

/// Splits identifiers into words on non-alphanumerics, lower-to-upper
/// transitions, acronym boundaries (`XMLHttp` -> `XML`, `Http`) and
/// letter/digit transitions.
pub(crate) fn identifier_words(value: &str) -> Vec<&str> {
    let characters: Vec<(usize, char)> = value.char_indices().collect();
    let mut words = Vec::new();
    let mut start: Option<usize> = None;
    for (position, &(index, current)) in characters.iter().enumerate() {
        if !(current.is_alphabetic() || current.is_numeric()) {
            if let Some(word_start) = start.take() {
                words.push(&value[word_start..index]);
            }
            continue;
        }
        let Some(word_start) = start else {
            start = Some(index);
            continue;
        };
        let previous = characters[position - 1].1;
        let next_lower = characters
            .get(position + 1)
            .is_some_and(|(_, next)| next.is_lowercase());
        let boundary = (current.is_uppercase() && previous.is_lowercase())
            || (current.is_uppercase() && previous.is_uppercase() && next_lower)
            || (current.is_numeric() != previous.is_numeric());
        if boundary {
            words.push(&value[word_start..index]);
            start = Some(index);
        }
    }
    if let Some(word_start) = start {
        words.push(&value[word_start..]);
    }
    words
}

#[cfg(test)]
mod tests;

/// Whether the catalog predates interned owner paths and must be rebuilt.
fn catalog_uses_path_text(connection: &Connection) -> rusqlite::Result<bool> {
    let mut statement = connection.prepare("PRAGMA table_info(workspace_symbols)")?;
    let mut names = statement.query_map([], |row| row.get::<_, String>(1))?;
    names.try_fold(false, |legacy, name| Ok(legacy || name? == "owner_path"))
}
