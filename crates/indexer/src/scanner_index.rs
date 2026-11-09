//! One coordinated indexing run (Go `fileIndexRun`).
//!
//! Workers read changed files, parse and prepare them in bounded batches
//! outside any transaction, then persist each batch in one workspace
//! mutation. A failing batch is rolled back and retried in halves so one bad
//! file cannot discard its neighbours. Tracked file states are published in
//! chunks after their mutations commit, also when the run is cancelled.

use std::sync::Arc;
use std::sync::Mutex;
use std::time::Duration;

use crossbeam_channel::{RecvTimeoutError, SendTimeoutError};

use crate::dataindex::clear_message_pack_buffers;
use crate::error::{CancelToken, Error, Result, ResultExt};
use crate::indexer::{Indexer, Prepared};
use crate::scanner::{
    FILE_STATS_LIMIT, FileMeta, FileState, Inner, SkippedFileStats, StoredFileState, content_hash,
    log_skipped_file, log_skipped_file_count,
};
use crate::store::lock;
use crate::workspace_symbols::{WorkspaceSymbol, WorkspaceSymbolDocument};
use crate::{Mutation, ParsedFile};

/// Bound preparation by source weight as well as file count: tiny files share
/// an efficient transaction, while one generated container file does not keep
/// dozens of syntax trees alive until a 50-file batch fills.
pub(crate) const MAX_PREPARATION_BATCH_FILES: usize = 50;
pub(crate) const MAX_PREPARATION_BATCH_BYTES: usize = 128 << 10;
/// Tracked states are published in chunks so the extra transaction stays
/// negligible while an interrupted run loses at most this many files.
const FILE_STATE_PUBLISH_THRESHOLD: usize = 512;
const BULK_STATE_LOOKUP_THRESHOLD: usize = 1024;
const CHANNEL_POLL: Duration = Duration::from_millis(10);

pub(crate) fn preparation_batch_ready(file_count: usize, source_bytes: usize) -> bool {
    file_count >= MAX_PREPARATION_BATCH_FILES || source_bytes >= MAX_PREPARATION_BATCH_BYTES
}

pub(crate) fn preparation_batch_would_overflow(
    file_count: usize,
    source_bytes: usize,
    next_source_bytes: usize,
) -> bool {
    file_count > 0
        && (file_count + 1 > MAX_PREPARATION_BATCH_FILES
            || source_bytes + next_source_bytes > MAX_PREPARATION_BATCH_BYTES)
}

struct Candidate {
    path: String,
    state: Option<StoredFileState>,
}

struct FileWork {
    path: String,
    content: Vec<u8>,
    meta: FileMeta,
    hash: i64,
}

struct PreparedFileWork {
    file: ParsedFile,
    meta: FileMeta,
    hash: i64,
    prepared: Vec<Option<Prepared>>,
}

enum Change {
    Unchanged,
    /// Size or modification time changed, the content did not: only the
    /// tracked state is refreshed.
    Touched(FileState),
    Skipped {
        stats: SkippedFileStats,
        tracked: bool,
    },
    Changed {
        content: Vec<u8>,
        meta: FileMeta,
        hash: i64,
    },
}

#[derive(Default)]
struct RunResults {
    errors: Vec<Error>,
    pending_states: Vec<FileState>,
    /// States of touched but unchanged files; published with the pending
    /// states but not counted as index updates.
    touched_states: Vec<FileState>,
    published_files: usize,
    skipped: Vec<(SkippedFileStats, bool)>,
}

struct Run<'a> {
    scanner: &'a Inner,
    cancel: &'a CancelToken,
    results: Mutex<RunResults>,
    /// Serialises the tracked-state writes, which go to a different database
    /// than the index mutations and therefore cannot join them.
    publish: Mutex<()>,
    /// States of files whose grouped index mutation committed durably,
    /// filled by after-commit callbacks on whichever worker commits.
    durable: Arc<Mutex<Vec<FileState>>>,
}

impl Inner {
    pub(crate) fn index_files(
        &self,
        cancel: &CancelToken,
        files: Vec<String>,
        filter_paths: bool,
        stored_states: Option<Vec<StoredFileState>>,
    ) -> Result<()> {
        if files.is_empty() {
            return Ok(());
        }
        let _operation = lock(&self.operation);
        cancel.check()?;
        let files = if filter_paths {
            self.filtered_index_paths(files)
        } else {
            files
        };
        if files.is_empty() {
            return Ok(());
        }
        self.clear_skipped_files(&files);

        let batch_indexers: Vec<&Arc<dyn Indexer>> = self
            .indexers
            .iter()
            .filter(|indexer| indexer.as_batch().is_some())
            .collect();
        for indexer in &batch_indexers {
            indexer
                .as_batch()
                .expect("filtered batch indexer")
                .begin_indexing_batch(&files);
        }
        let run = Run {
            scanner: self,
            cancel,
            results: Mutex::new(RunResults::default()),
            publish: Mutex::new(()),
            durable: Arc::default(),
        };
        let result = run.execute(files, stored_states);

        // Batch indexers publish their generations while finishing; consumers
        // must never refresh against committed hashes before that.
        let mut finish_errors = Vec::new();
        for indexer in batch_indexers.iter().rev() {
            if let Err(error) = indexer
                .as_batch()
                .expect("filtered batch indexer")
                .end_indexing_batch()
            {
                finish_errors.push(error);
            }
        }
        if result.is_ok() && finish_errors.is_empty() && !cancel.is_cancelled() {
            let workers = self.configured_workers();
            for indexer in &batch_indexers {
                if let Err(error) = indexer
                    .as_batch()
                    .expect("filtered batch indexer")
                    .settle_indexing_batch(cancel, workers)
                {
                    finish_errors.push(error);
                }
            }
        }
        clear_message_pack_buffers();
        let published = lock(&run.results).published_files;
        if published > 0 {
            self.notify_update();
        }
        let errors = result.err().into_iter().chain(finish_errors);
        match Error::join(errors) {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }

    fn filtered_index_paths(&self, files: Vec<String>) -> Vec<String> {
        let mut filtered: Vec<String> = files
            .into_iter()
            .filter(|path| self.should_index_path(path))
            .collect();
        filtered.sort();
        filtered.dedup();
        filtered
    }

    /// Reads a candidate when its tracked state changed. A known `state`
    /// avoids one SQLite query per file during large scans.
    fn file_change(
        &self,
        cancel: &CancelToken,
        path: &str,
        state: Option<StoredFileState>,
    ) -> Result<Change> {
        cancel.check()?;
        let metadata = std::fs::metadata(path)?;
        let meta = FileMeta::of(&metadata);
        let stored = match state {
            Some(state) => state.found.then_some(state),
            None => self.query_file_state(path)?,
        };
        if let Some((reason, limit)) = self.source_size_rejection(meta.size) {
            return Ok(Change::Skipped {
                stats: SkippedFileStats {
                    path: path.to_owned(),
                    bytes: meta.size,
                    limit_bytes: limit,
                    reason: reason.to_owned(),
                },
                tracked: stored.is_some(),
            });
        }
        if stored.is_some_and(|stored| stored.meta == meta) {
            return Ok(Change::Unchanged);
        }
        let content = std::fs::read(path)?;
        // The file may have grown between the metadata read and the read.
        let actual = i64::try_from(content.len()).unwrap_or(i64::MAX);
        if let Some((reason, limit)) = self.source_size_rejection(actual) {
            return Ok(Change::Skipped {
                stats: SkippedFileStats {
                    path: path.to_owned(),
                    bytes: actual,
                    limit_bytes: limit,
                    reason: reason.to_owned(),
                },
                tracked: stored.is_some(),
            });
        }
        let hash = content_hash(&content);
        if let Some(stored) = stored
            && stored.meta.size == actual
            && stored.hash == hash
        {
            return Ok(Change::Touched(FileState {
                path: path.to_owned(),
                meta: FileMeta {
                    size: actual,
                    ..meta
                },
                hash,
            }));
        }
        Ok(Change::Changed {
            content,
            meta,
            hash,
        })
    }
}

impl Run<'_> {
    fn execute(
        &self,
        files: Vec<String>,
        stored_states: Option<Vec<StoredFileState>>,
    ) -> Result<()> {
        let stored_states = match stored_states {
            Some(states) => Some(states),
            None if files.len() >= BULK_STATE_LOOKUP_THRESHOLD => Some(
                self.scanner
                    .load_file_states(&files)
                    .context("load file states")?,
            ),
            None => None,
        };
        let completed = self.run_workers(files, stored_states);
        // Batches that succeeded are committed also on the cancelled path.
        if let Some(store) = &self.scanner.store
            && let Err(error) = store.commit_grouped()
        {
            self.record_error(error);
        }
        if completed {
            self.commit_skipped_files();
        }
        // Publishing on the cancelled path is deliberate: every state held
        // here belongs to a batch whose index mutation already committed.
        self.publish_file_states();
        let errors = std::mem::take(&mut lock(&self.results).errors);
        match Error::join(errors) {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }

    fn record_error(&self, error: Error) {
        lock(&self.results).errors.push(error);
    }

    /// Returns `false` when the producer observed cancellation.
    fn run_workers(&self, files: Vec<String>, stored_states: Option<Vec<StoredFileState>>) -> bool {
        let workers = self.scanner.configured_workers().min(files.len()).max(1);
        let (sender, receiver) = crossbeam_channel::bounded::<Candidate>(100);
        std::thread::scope(|scope| {
            for _ in 0..workers {
                let receiver = receiver.clone();
                scope.spawn(move || self.worker(&receiver));
            }
            drop(receiver);
            // Owned by the producer so workers observe disconnection as soon
            // as it returns, before the scope joins them.
            let sender = sender;
            for (index, path) in files.into_iter().enumerate() {
                let mut candidate = Candidate {
                    path,
                    state: stored_states.as_ref().map(|states| states[index]),
                };
                loop {
                    if self.cancel.is_cancelled() {
                        self.record_error(Error::Cancelled);
                        return false;
                    }
                    match sender.send_timeout(candidate, CHANNEL_POLL) {
                        Ok(()) => break,
                        Err(SendTimeoutError::Timeout(returned)) => candidate = returned,
                        Err(SendTimeoutError::Disconnected(_)) => return true,
                    }
                }
            }
            true
        })
    }

    fn worker(&self, receiver: &crossbeam_channel::Receiver<Candidate>) {
        let mut batch: Vec<FileWork> = Vec::with_capacity(MAX_PREPARATION_BATCH_FILES);
        let mut batch_bytes = 0;
        loop {
            if self.cancel.is_cancelled() {
                self.record_error(Error::Cancelled);
                self.process_batch(std::mem::take(&mut batch));
                return;
            }
            let candidate = match receiver.recv_timeout(CHANNEL_POLL) {
                Ok(candidate) => candidate,
                Err(RecvTimeoutError::Timeout) => continue,
                Err(RecvTimeoutError::Disconnected) => {
                    self.process_batch(std::mem::take(&mut batch));
                    return;
                }
            };
            let change =
                match self
                    .scanner
                    .file_change(self.cancel, &candidate.path, candidate.state)
                {
                    Ok(change) => change,
                    Err(error) => {
                        self.record_error(error.context(format!("read {}", candidate.path)));
                        continue;
                    }
                };
            let (content, meta, hash) = match change {
                Change::Unchanged => continue,
                Change::Touched(state) => {
                    lock(&self.results).touched_states.push(state);
                    continue;
                }
                Change::Skipped { stats, tracked } => {
                    lock(&self.results).skipped.push((stats, tracked));
                    continue;
                }
                Change::Changed {
                    content,
                    meta,
                    hash,
                } => (content, meta, hash),
            };
            if preparation_batch_would_overflow(batch.len(), batch_bytes, content.len()) {
                self.process_batch(std::mem::take(&mut batch));
                batch_bytes = 0;
            }
            batch_bytes += content.len();
            batch.push(FileWork {
                path: candidate.path,
                content,
                meta,
                hash,
            });
            if preparation_batch_ready(batch.len(), batch_bytes) {
                self.process_batch(std::mem::take(&mut batch));
                batch_bytes = 0;
            }
        }
    }

    fn commit_skipped_files(&self) {
        let skipped = std::mem::take(&mut lock(&self.results).skipped);
        if skipped.is_empty() {
            return;
        }
        let tracked: Vec<String> = skipped
            .iter()
            .filter(|(_, tracked)| *tracked)
            .map(|(stats, _)| stats.path.clone())
            .collect();
        if !tracked.is_empty()
            && let Err(error) = self.scanner.remove_files_locked(self.cancel, &tracked)
        {
            self.record_error(error.context("remove oversized index entries"));
        }
        let stats: Vec<SkippedFileStats> = skipped.into_iter().map(|(stats, _)| stats).collect();
        self.scanner.record_skipped_files(&stats);
        for file in stats.iter().take(FILE_STATS_LIMIT) {
            log_skipped_file(file);
        }
        if stats.len() > FILE_STATS_LIMIT {
            log_skipped_file_count(stats.len());
        }
    }

    fn process_batch(&self, items: Vec<FileWork>) {
        if items.is_empty() {
            return;
        }
        let mut prepared = self.prepare_batch(items);
        if prepared.is_empty() {
            return;
        }
        let successful = if self.scanner.store.is_some() {
            self.index_with_store(&mut prepared);
            std::mem::take(&mut *lock(&self.durable))
        } else {
            self.index_without_store(&prepared)
        };
        // Release syntax trees before the next batch is read.
        drop(prepared);
        if successful.is_empty() {
            return;
        }
        let due = {
            let mut results = lock(&self.results);
            results.pending_states.extend(successful);
            results.pending_states.len() >= FILE_STATE_PUBLISH_THRESHOLD
        };
        if due {
            self.publish_file_states();
        }
    }

    fn prepare_batch(&self, items: Vec<FileWork>) -> Vec<PreparedFileWork> {
        let mut prepared = Vec::with_capacity(items.len());
        for item in items {
            if self.cancel.is_cancelled() {
                self.record_error(Error::Cancelled);
                break;
            }
            let file = ParsedFile::new(item.path, item.content);
            if self.scanner.should_preparse_path(file.path()) {
                let _ = file.syntax_tree();
            }
            let values = self.prepare_file(&file);
            file.clear_memoized();
            match values {
                Ok(values) => prepared.push(PreparedFileWork {
                    file,
                    meta: item.meta,
                    hash: item.hash,
                    prepared: values,
                }),
                Err(error) => self.record_error(error),
            }
        }
        prepared
    }

    fn prepare_file(&self, file: &ParsedFile) -> Result<Vec<Option<Prepared>>> {
        let mut values = Vec::with_capacity(self.scanner.indexers.len());
        let mut errors = Vec::new();
        for indexer in &self.scanner.indexers {
            let Some(preparer) = indexer.as_preparing() else {
                values.push(None);
                continue;
            };
            match preparer.prepare(file) {
                Ok(value) => values.push(value),
                Err(error) => {
                    values.push(None);
                    errors.push(error.context(format!("{} prepare {}", indexer.id(), file.path())));
                }
            }
        }
        match Error::join(errors) {
            Some(error) => Err(error),
            None => Ok(values),
        }
    }

    fn index_file(&self, item: &PreparedFileWork) -> Vec<Error> {
        let mut errors = Vec::new();
        for (index, indexer) in self.scanner.indexers.iter().enumerate() {
            if self.cancel.is_cancelled() {
                errors.push(Error::Cancelled);
                break;
            }
            let result = match indexer.as_preparing() {
                Some(preparer) => {
                    preparer.index_prepared(&item.file, item.prepared[index].as_ref())
                }
                None => indexer.index(&item.file),
            };
            if let Err(error) = result {
                errors.push(error.context(format!("{} index {}", indexer.id(), item.file.path())));
            }
        }
        errors
    }

    fn index_without_store(&self, prepared: &[PreparedFileWork]) -> Vec<FileState> {
        let mut successful = Vec::with_capacity(prepared.len());
        for item in prepared {
            match Error::join(self.index_file(item)) {
                Some(error) => self.record_error(error),
                None => successful.push(file_state(item)),
            }
        }
        successful
    }

    /// Indexes `batch` in one grouped mutation, splitting it after a failure.
    /// File states reach [`Run::durable`] once the group commits.
    fn index_with_store(&self, batch: &mut [PreparedFileWork]) {
        if batch.is_empty() {
            return;
        }
        if self.cancel.is_cancelled() {
            self.record_error(Error::Cancelled);
            return;
        }
        let store = self.scanner.store.as_ref().expect("indexing with a store");
        let mutation = match store.begin_grouped_mutation(self.cancel) {
            Ok(mutation) => Arc::new(mutation),
            Err(error) => {
                self.record_error(
                    error.context(format!("begin indexing {}", batch[0].file.path())),
                );
                return;
            }
        };
        let mut errors = self.index_mutation_batch(&mutation, batch);
        for item in batch.iter_mut() {
            item.file.set_mutation(None);
        }
        if !errors.is_empty() {
            if let Err(error) = mutation.rollback() {
                errors.push(error);
            }
            if batch.len() > 1 && !self.cancel.is_cancelled() {
                let middle = batch.len() / 2;
                let (left, right) = batch.split_at_mut(middle);
                self.index_with_store(left);
                self.index_with_store(right);
                return;
            }
            self.record_error(Error::join(errors).expect("errors are not empty"));
            return;
        }
        if self.cancel.is_cancelled() {
            let rollback = mutation.rollback().err();
            self.record_error(
                Error::join([Some(Error::Cancelled), rollback].into_iter().flatten())
                    .expect("cancelled"),
            );
            return;
        }
        let states: Vec<FileState> = batch.iter().map(file_state).collect();
        let durable = self.durable.clone();
        let registered = mutation.after_commit(move || lock(&durable).extend(states));
        if let Err(error) = registered.and_then(|()| mutation.commit()) {
            self.record_error(error.context(format!(
                "commit indexing batch starting at {}",
                batch[0].file.path()
            )));
        }
    }

    fn index_mutation_batch(
        &self,
        mutation: &Arc<Mutation>,
        batch: &mut [PreparedFileWork],
    ) -> Vec<Error> {
        let mut errors = Vec::new();
        let symbols = self.scanner.symbols.as_ref();
        let mut documents = Vec::with_capacity(if symbols.is_some() { batch.len() } else { 0 });
        for item in batch.iter_mut() {
            item.file.clear_workspace_symbols();
            item.file.set_mutation(Some(mutation.clone()));
            errors.extend(self.index_file(item));
            if !errors.is_empty() {
                break;
            }
            if symbols.is_none() {
                continue;
            }
            match self.workspace_symbols(item) {
                Ok(symbols) => documents.push(WorkspaceSymbolDocument {
                    path: item.file.path().to_owned(),
                    symbols,
                }),
                Err(error) => {
                    errors.push(error);
                    break;
                }
            }
        }
        if errors.is_empty()
            && let Some(symbols) = symbols
            && let Err(error) = symbols.replace_files_in(mutation, &documents)
        {
            errors.push(error.context(format!(
                "catalog workspace symbol batch starting at {}",
                batch[0].file.path()
            )));
        }
        errors
    }

    fn workspace_symbols(&self, item: &PreparedFileWork) -> Result<Vec<WorkspaceSymbol>> {
        let mut symbols = item.file.take_workspace_symbols();
        for (index, indexer) in self.scanner.indexers.iter().enumerate() {
            let Some(contributor) = indexer.as_workspace_symbol_contributor() else {
                continue;
            };
            let current = contributor
                .workspace_symbols(&item.file, item.prepared[index].as_ref())
                .with_context(|| {
                    format!("{} workspace symbols {}", indexer.id(), item.file.path())
                })?;
            symbols.extend(current);
        }
        Ok(symbols)
    }

    /// Records the files whose index mutations already committed. The write
    /// ignores cancellation: it describes work that is already durable.
    fn publish_file_states(&self) {
        let _publish = lock(&self.publish);
        let durable = std::mem::take(&mut *lock(&self.durable));
        let (mut pending, touched) = {
            let mut results = lock(&self.results);
            results.pending_states.extend(durable);
            (
                std::mem::take(&mut results.pending_states),
                std::mem::take(&mut results.touched_states),
            )
        };
        let published = pending.len();
        pending.extend(touched);
        if pending.is_empty() {
            return;
        }
        match self.scanner.update_file_states(&pending) {
            Ok(()) => lock(&self.results).published_files += published,
            Err(error) => self.record_error(error.context("commit file state")),
        }
    }
}

fn file_state(item: &PreparedFileWork) -> FileState {
    FileState {
        path: item.file.path().to_owned(),
        meta: item.meta,
        hash: item.hash,
    }
}
