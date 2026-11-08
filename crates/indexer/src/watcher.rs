//! The scanner's filesystem watcher, the only source of index events.
//!
//! Linux and other inotify/kqueue platforms watch each entered directory
//! non-recursively, so skipped trees such as `node_modules` never consume
//! watch descriptors. macOS (FSEvents) and Windows watch the root
//! recursively and filter events by path. Events are debounced and then
//! applied as one indexing and one removal operation; PHAR archive changes
//! and dropped-event notifications trigger a full reconciliation.

use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use crossbeam_channel::{Receiver, RecvTimeoutError};
use notify::event::{EventKind, ModifyKind, RenameMode};
use notify::{RecursiveMode, Watcher};

use crate::error::{CancelToken, Error, Result, ResultExt};
use crate::paths;
use crate::phar_sources::is_phar_archive_path;
use crate::scanner::Inner;
use crate::store::lock;

const WATCHER_DEBOUNCE: Duration = Duration::from_millis(200);
const POLL_INTERVAL: Duration = Duration::from_millis(50);
const RECURSIVE: bool = cfg!(any(target_os = "macos", target_os = "windows"));

pub(crate) struct WatcherHandle {
    cancel: CancelToken,
    thread: JoinHandle<()>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Operation {
    Create,
    Write,
    Remove,
    /// The platform could not say what happened; the path is classified by
    /// its current state.
    Any,
}

pub(crate) fn start(scanner: &Arc<Inner>) -> Result<()> {
    let mut slot = lock(&scanner.watcher);
    if slot.is_some() {
        return Err(Error::msg("file watcher is already running"));
    }
    let (sender, events) = crossbeam_channel::unbounded();
    let mut watcher = notify::recommended_watcher(move |event| {
        let _ = sender.send(event);
    })
    .map_err(Error::other)
    .context("create file watcher")?;
    let root = PathBuf::from(&scanner.project_root);
    let resolved_root = std::fs::canonicalize(&root)
        .ok()
        .and_then(|path| path.to_str().map(paths::clean))
        .unwrap_or_else(|| paths::clean(&scanner.project_root));
    if RECURSIVE {
        watcher
            .watch(&root, RecursiveMode::Recursive)
            .map_err(Error::other)
            .context("watch workspace")?;
    } else {
        add_directory(scanner, &mut watcher, &scanner.project_root, None)?;
    }
    let cancel = CancelToken::new();
    let state = WatchLoop {
        scanner: scanner.clone(),
        watcher,
        events,
        cancel: cancel.clone(),
        resolved_root,
        adds: HashSet::new(),
        removes: HashSet::new(),
        full_scan: false,
    };
    let thread = std::thread::Builder::new()
        .name("shopware-lsp-watcher".into())
        .spawn(move || state.run())
        .context("start file watcher")?;
    *slot = Some(WatcherHandle { cancel, thread });
    Ok(())
}

pub(crate) fn stop(scanner: &Inner) {
    let handle = lock(&scanner.watcher).take();
    if let Some(handle) = handle {
        handle.cancel.cancel();
        let _ = handle.thread.join();
    }
}

/// Watches `directory` and every enterable descendant non-recursively. With
/// `pending`, indexable files found below a newly created directory are
/// queued, because their creation events predate the watch.
fn add_directory(
    scanner: &Inner,
    watcher: &mut notify::RecommendedWatcher,
    directory: &str,
    mut pending: Option<&mut HashSet<String>>,
) -> Result<()> {
    let mut stack = vec![directory.to_owned()];
    while let Some(current) = stack.pop() {
        if !scanner.should_enter_directory(&current) {
            continue;
        }
        watcher
            .watch(Path::new(&current), RecursiveMode::NonRecursive)
            .map_err(Error::other)
            .with_context(|| format!("watch directory {current}"))?;
        let entries =
            std::fs::read_dir(&current).with_context(|| format!("access watch path {current}"))?;
        for entry in entries {
            let entry = entry.with_context(|| format!("access watch path {current}"))?;
            let Some(path) = entry.path().to_str().map(str::to_owned) else {
                continue;
            };
            let file_type = entry.file_type()?;
            if file_type.is_dir() {
                stack.push(path);
            } else if let Some(pending) = pending.as_deref_mut()
                && scanner.should_index_path(&path)
            {
                pending.insert(path);
            }
        }
    }
    Ok(())
}

/// Queues indexable files below a directory without adding watches
/// (recursive platforms).
fn collect_files(scanner: &Inner, directory: &str, pending: &mut HashSet<String>) {
    let mut stack = vec![directory.to_owned()];
    while let Some(current) = stack.pop() {
        if !scanner.should_enter_directory(&current) {
            continue;
        }
        let Ok(entries) = std::fs::read_dir(&current) else {
            continue;
        };
        for entry in entries.flatten() {
            let Some(path) = entry.path().to_str().map(str::to_owned) else {
                continue;
            };
            match entry.file_type() {
                Ok(file_type) if file_type.is_dir() => stack.push(path),
                Ok(_) if scanner.should_index_path(&path) => {
                    pending.insert(path);
                }
                _ => {}
            }
        }
    }
}

struct WatchLoop {
    scanner: Arc<Inner>,
    watcher: notify::RecommendedWatcher,
    events: Receiver<notify::Result<notify::Event>>,
    cancel: CancelToken,
    resolved_root: String,
    adds: HashSet<String>,
    removes: HashSet<String>,
    full_scan: bool,
}

impl WatchLoop {
    fn run(mut self) {
        let mut deadline: Option<Instant> = None;
        loop {
            if self.cancel.is_cancelled() {
                return;
            }
            let timeout = deadline.map_or(POLL_INTERVAL, |deadline| {
                deadline
                    .saturating_duration_since(Instant::now())
                    .min(POLL_INTERVAL)
            });
            match self.events.recv_timeout(timeout) {
                Ok(Ok(event)) => {
                    if self.record(event) {
                        deadline = Some(Instant::now() + WATCHER_DEBOUNCE);
                    }
                }
                Ok(Err(error)) => log::warn!("File watcher error: {error}"),
                Err(RecvTimeoutError::Timeout) => {}
                Err(RecvTimeoutError::Disconnected) => return,
            }
            if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                deadline = None;
                self.process();
            }
        }
    }

    fn record(&mut self, event: notify::Event) -> bool {
        if event.need_rescan() {
            self.full_scan = true;
            return true;
        }
        let operations: Vec<(PathBuf, Operation)> = match event.kind {
            EventKind::Create(_) => event
                .paths
                .into_iter()
                .map(|path| (path, Operation::Create))
                .collect(),
            EventKind::Remove(_) => event
                .paths
                .into_iter()
                .map(|path| (path, Operation::Remove))
                .collect(),
            EventKind::Modify(ModifyKind::Metadata(_)) | EventKind::Access(_) => return false,
            EventKind::Modify(ModifyKind::Name(RenameMode::From)) => event
                .paths
                .into_iter()
                .map(|path| (path, Operation::Remove))
                .collect(),
            EventKind::Modify(ModifyKind::Name(RenameMode::To)) => event
                .paths
                .into_iter()
                .map(|path| (path, Operation::Create))
                .collect(),
            EventKind::Modify(ModifyKind::Name(RenameMode::Both)) => {
                let mut paths = event.paths.into_iter();
                let mut operations = Vec::new();
                if let Some(from) = paths.next() {
                    operations.push((from, Operation::Remove));
                }
                operations.extend(paths.map(|to| (to, Operation::Create)));
                operations
            }
            EventKind::Modify(ModifyKind::Name(_)) | EventKind::Any | EventKind::Other => event
                .paths
                .into_iter()
                .map(|path| (path, Operation::Any))
                .collect(),
            EventKind::Modify(_) => event
                .paths
                .into_iter()
                .map(|path| (path, Operation::Write))
                .collect(),
        };
        let mut changed = false;
        for (path, operation) in operations {
            let Some(path) = path.to_str() else {
                continue;
            };
            let path = self.workspace_path(path);
            changed |= self.record_path(&path, operation);
        }
        changed
    }

    /// Maps canonical event paths (FSEvents reports resolved symlinks) back
    /// to the indexed workspace without filesystem reads.
    fn workspace_path(&self, path: &str) -> String {
        if self.resolved_root == paths::clean(&self.scanner.project_root) {
            return path.to_owned();
        }
        match paths::relative_path_within(&self.resolved_root, path) {
            Some(relative) => paths::join(&[&self.scanner.project_root, &relative]),
            None => path.to_owned(),
        }
    }

    fn record_path(&mut self, path: &str, operation: Operation) -> bool {
        if is_phar_archive_path(path) && !self.scanner.is_explicitly_excluded(path, false) {
            // An archive update can add, modify and remove thousands of
            // logical sources; a workspace scan refreshes them atomically.
            self.full_scan = true;
            return true;
        }
        let metadata = std::fs::metadata(path);
        let exists = metadata.is_ok();
        let operation = match operation {
            Operation::Any if exists => Operation::Write,
            Operation::Any => Operation::Remove,
            other => other,
        };
        if let Ok(metadata) = &metadata
            && metadata.is_dir()
        {
            if operation == Operation::Create {
                if !self.scanner.should_enter_directory(path) {
                    return false;
                }
                let mut found = HashSet::new();
                if RECURSIVE {
                    collect_files(&self.scanner, path, &mut found);
                } else if let Err(error) =
                    add_directory(&self.scanner, &mut self.watcher, path, Some(&mut found))
                {
                    log::warn!("Error watching new directory: {error}");
                }
                for file in found {
                    self.removes.remove(&file);
                    self.adds.insert(file);
                }
            }
            return !self.adds.is_empty();
        }
        let removal = operation == Operation::Remove;
        if !self.scanner.should_index_path(path) {
            if !exists && removal {
                // A removed directory: drop every tracked file below it.
                return match self.scanner.tracked_paths_under(path) {
                    Ok(tracked) => {
                        let changed = !tracked.is_empty();
                        for file in tracked {
                            self.adds.remove(&file);
                            self.removes.insert(file);
                        }
                        changed
                    }
                    Err(error) => {
                        log::warn!("Error resolving removed directory contents: {error}");
                        false
                    }
                };
            }
            return false;
        }
        if !exists || removal {
            if !removal {
                return false;
            }
            self.adds.remove(path);
            self.removes.insert(path.to_owned());
            return true;
        }
        self.removes.remove(path);
        self.adds.insert(path.to_owned());
        true
    }

    fn process(&mut self) {
        let mut adds: Vec<String> = std::mem::take(&mut self.adds).into_iter().collect();
        let mut removes: Vec<String> = std::mem::take(&mut self.removes).into_iter().collect();
        if std::mem::take(&mut self.full_scan) {
            if let Err(error) = self.scanner.index_all(&self.cancel)
                && !self.cancel.is_cancelled()
            {
                log::warn!("Error rescanning workspace changes: {error}");
            }
            return;
        }
        adds.sort();
        removes.sort();
        if !adds.is_empty()
            && let Err(error) = self.scanner.index_files(&self.cancel, adds, true, None)
            && !self.cancel.is_cancelled()
        {
            log::warn!("Error indexing changed files: {error}");
        }
        if !removes.is_empty()
            && let Err(error) = self.scanner.remove_files(&self.cancel, &removes)
            && !self.cancel.is_cancelled()
        {
            log::warn!("Error removing deleted files: {error}");
        }
    }
}
