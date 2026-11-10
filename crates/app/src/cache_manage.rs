//! Listing and clearing the workspace caches of this machine.
//!
//! A cache directory is named after a hash of its project root, so it
//! records the root in a marker file to stay identifiable. A workspace
//! that uses a cache holds a shared lock on a file in it; clearing takes
//! the lock exclusively and so leaves the cache of a running server alone.

use std::fs::File;
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use serde::Serialize;

use crate::cache::{CacheDirError, project_cache_folder_in};

const ROOT_FILE: &str = "root";
const LOCK_FILE: &str = "lock";
const VERSION_FILE: &str = "index_version";

/// Why a cache is, or is not, worth keeping.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum CacheState {
    /// The project exists and the index has the current format.
    Current,
    /// Written by another version: the next start rebuilds it.
    Outdated,
    /// The project directory is gone.
    ProjectGone,
    /// Nothing says which project it belongs to (written before caches
    /// recorded their root, or not a cache of this server).
    Unknown,
}

/// How long an outdated or unidentified cache must be unused before it
/// counts as stale. Servers from before caches were locked while open do
/// not show as in use, and a cache they wrote does not record its project:
/// a recently written one is assumed to be theirs.
const STALE_AFTER_SECONDS: u64 = 7 * 24 * 60 * 60;

/// One cache directory below the cache base.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CacheEntry {
    pub path: PathBuf,
    /// The project root, when the cache recorded it.
    pub root: Option<String>,
    pub state: CacheState,
    /// A workspace (an editor's server, a running command) has it open.
    pub in_use: bool,
    pub bytes: u64,
    pub files: u64,
    /// Seconds since the cache was last written, when known.
    pub idle_seconds: Option<u64>,
    /// `cache clear --stale` removes it: the project is gone, or it is
    /// outdated or unidentified and was not written for a week.
    pub stale: bool,
}

/// Which caches to clear.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ClearScope<'a> {
    /// The cache of this project root.
    Project(&'a str),
    /// Every cache marked [`CacheEntry::stale`].
    Stale,
    All,
}

/// What clearing did.
#[derive(Debug, Clone, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ClearReport {
    pub cleared: Vec<CacheEntry>,
    /// Caches left alone because a workspace has them open.
    pub in_use: Vec<CacheEntry>,
    pub freed_bytes: u64,
    /// Nothing was deleted: the report says what would be.
    pub dry_run: bool,
}

/// The shared lock a workspace holds on its cache while it is open.
#[derive(Debug)]
pub struct CacheUse {
    _lock: Option<File>,
}

impl CacheUse {
    /// Marks `cache_dir` as the cache of `root` and in use. Never fails: a
    /// cache that cannot be marked still works, it is only not protected
    /// from `cache clear`.
    pub fn begin(cache_dir: &Path, root: &str) -> Self {
        let marker = cache_dir.join(ROOT_FILE);
        if std::fs::read_to_string(&marker).ok().as_deref() != Some(root) {
            let _ = std::fs::write(&marker, root);
        }
        let lock = File::options()
            .create(true)
            .truncate(false)
            .write(true)
            .open(cache_dir.join(LOCK_FILE))
            .ok()
            .filter(|file| file.lock_shared().is_ok());
        Self { _lock: lock }
    }
}

/// The directory all workspace caches are in.
pub fn caches_dir(cache_base: &Path) -> PathBuf {
    cache_base.join("shopware-lsp")
}

/// Every cache below `cache_base`, the largest first.
pub fn list_caches(cache_base: &Path) -> std::io::Result<Vec<CacheEntry>> {
    let directory = caches_dir(cache_base);
    let entries = match std::fs::read_dir(&directory) {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(error) => return Err(error),
    };
    let mut caches = Vec::new();
    for entry in entries {
        let entry = entry?;
        if entry.file_type()?.is_dir() {
            caches.push(inspect(&entry.path()));
        }
    }
    caches.sort_by(|left, right| {
        right
            .bytes
            .cmp(&left.bytes)
            .then_with(|| left.path.cmp(&right.path))
    });
    Ok(caches)
}

/// Clears the caches `scope` selects, except those in use.
pub fn clear_caches(
    cache_base: &Path,
    scope: ClearScope<'_>,
    dry_run: bool,
) -> Result<ClearReport, ClearError> {
    let selected: Vec<CacheEntry> = match scope {
        ClearScope::Project(root) => {
            // Resolving the directory creates it; an empty one is not a
            // cache worth reporting.
            let path = project_cache_folder_in(cache_base, root)?;
            let entry = inspect(&path);
            if entry.files == 0 {
                let _ = std::fs::remove_dir(&path);
                Vec::new()
            } else {
                vec![entry]
            }
        }
        ClearScope::Stale => list_caches(cache_base)
            .map_err(ClearError::List)?
            .into_iter()
            .filter(|cache| cache.stale)
            .collect(),
        ClearScope::All => list_caches(cache_base).map_err(ClearError::List)?,
    };
    let mut report = ClearReport {
        dry_run,
        ..ClearReport::default()
    };
    for cache in selected {
        // Held until the directory is gone, so no workspace opens it
        // half deleted.
        let Some(lock) = exclusive(&cache.path) else {
            report.in_use.push(cache);
            continue;
        };
        if !dry_run {
            // Windows does not delete a directory with an open file in it.
            #[cfg(windows)]
            drop(lock);
            std::fs::remove_dir_all(&cache.path).map_err(|source| ClearError::Remove {
                path: cache.path.clone(),
                source,
            })?;
        }
        #[cfg(not(windows))]
        drop(lock);
        report.freed_bytes += cache.bytes;
        report.cleared.push(cache);
    }
    Ok(report)
}

/// Why caches could not be cleared.
#[derive(Debug, thiserror::Error)]
pub enum ClearError {
    #[error(transparent)]
    CacheDir(#[from] CacheDirError),
    #[error("list caches: {0}")]
    List(#[source] std::io::Error),
    #[error("remove {}: {source}", path.display())]
    Remove {
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },
}

/// The exclusive lock on a cache, or `None` while a workspace uses it. A
/// cache without a lock file is free, and gets one here.
fn exclusive(cache_dir: &Path) -> Option<File> {
    let file = File::options()
        .create(true)
        .truncate(false)
        .write(true)
        .open(cache_dir.join(LOCK_FILE))
        .ok()?;
    file.try_lock().ok()?;
    Some(file)
}

fn inspect(path: &Path) -> CacheEntry {
    let root = std::fs::read_to_string(path.join(ROOT_FILE))
        .ok()
        .map(|root| root.trim().to_owned())
        .filter(|root| !root.is_empty());
    let version = std::fs::read_to_string(path.join(VERSION_FILE)).ok();
    let state = match &root {
        None => CacheState::Unknown,
        Some(root) if !Path::new(root).is_dir() => CacheState::ProjectGone,
        Some(_)
            if version.as_deref().map(str::trim)
                != Some(swlsp_indexer::index_version_marker().as_str()) =>
        {
            CacheState::Outdated
        }
        Some(_) => CacheState::Current,
    };
    let mut usage = Usage::default();
    usage.add(path);
    // A lock file created by looking does not make the cache newer.
    let in_use = path.join(LOCK_FILE).exists() && exclusive(path).is_none();
    let idle_seconds = usage
        .modified
        .and_then(|modified| SystemTime::now().duration_since(modified).ok())
        .map(|idle| idle.as_secs());
    let unused = idle_seconds.is_none_or(|idle| idle >= STALE_AFTER_SECONDS);
    CacheEntry {
        path: path.to_owned(),
        root,
        state,
        in_use,
        bytes: usage.bytes,
        files: usage.files,
        idle_seconds,
        stale: !in_use
            && match state {
                CacheState::Current => false,
                CacheState::ProjectGone => true,
                CacheState::Outdated | CacheState::Unknown => unused,
            },
    }
}

#[derive(Default)]
struct Usage {
    bytes: u64,
    files: u64,
    modified: Option<SystemTime>,
}

impl Usage {
    fn add(&mut self, path: &Path) {
        let Ok(entries) = std::fs::read_dir(path) else {
            return;
        };
        for entry in entries.flatten() {
            let Ok(metadata) = entry.metadata() else {
                continue;
            };
            if metadata.is_dir() {
                self.add(&entry.path());
                continue;
            }
            let name = entry.file_name();
            // The markers are not content: they are rewritten on every use.
            if name == ROOT_FILE || name == LOCK_FILE {
                continue;
            }
            self.bytes += metadata.len();
            self.files += 1;
            if let Ok(modified) = metadata.modified()
                && self.modified.is_none_or(|latest| modified > latest)
            {
                self.modified = Some(modified);
            }
        }
    }
}

/// The custom LSP commands editors manage caches with.
pub(crate) fn commands(cache_base: &Path) -> Vec<(&'static str, swlsp_lsp::CommandFunc)> {
    use std::sync::Arc;

    use serde_json::{Value, json};

    let list_base = cache_base.to_owned();
    let clear_base = cache_base.to_owned();
    vec![
        (
            "shopware/cache/list",
            Arc::new(move |_ctx, _params| {
                let caches = list_caches(&list_base)?;
                Ok(json!({
                    "directory": caches_dir(&list_base),
                    "caches": caches,
                }))
            }),
        ),
        (
            "shopware/cache/clear",
            Arc::new(move |_ctx, params: Option<&Value>| {
                let scope = params
                    .and_then(|params| params.get("scope"))
                    .and_then(Value::as_str)
                    .unwrap_or("stale");
                let scope = match scope {
                    "stale" => ClearScope::Stale,
                    "all" => ClearScope::All,
                    // The server's own cache is open: it is rebuilt in
                    // place instead.
                    other => {
                        return Err(format!(
                            "unknown cache scope {other:?}: expected \"stale\" or \"all\"; \
                             shopware/forceReindex rebuilds the cache of this workspace"
                        )
                        .into());
                    }
                };
                let dry_run = params
                    .and_then(|params| params.get("dryRun"))
                    .and_then(Value::as_bool)
                    .unwrap_or(false);
                let report = clear_caches(&clear_base, scope, dry_run)?;
                Ok(serde_json::to_value(report)?)
            }),
        ),
    ]
}
