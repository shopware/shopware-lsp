//! Publication of immutable workspace generations.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, RwLock};

use crate::str::Str;

use crate::atom::{Collection, Generation, current_generation, should_collect};
use crate::canon::Canon;
use crate::document::Document;
use crate::lazy_returns::ReturnInference;
use crate::packed::PackedDocument;
use crate::snapshot::Snapshot;
use crate::workspace::{LoadError, WorkspaceGraph, WorkspaceGraphDecoder};

/// Allocation hints for a streamed cache restore. Hints may be zero or
/// underestimate without changing semantics.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct WorkspaceRestoreCapacity {
    pub documents: usize,
    pub strings: usize,
    pub types: usize,
}

/// Publishes immutable workspace snapshots. Per-file updates are cheap;
/// readers never observe a partially mutated graph.
pub struct Store {
    documents: Mutex<BTreeMap<Str, Arc<PackedDocument>>>,
    revision: AtomicU64,
    current: RwLock<Arc<Snapshot>>,
    collection_listeners: Mutex<Vec<CollectionListener>>,
    /// Attached to every published generation.
    inference: RwLock<Option<Arc<dyn ReturnInference>>>,
}

type CollectionListener = Box<dyn Fn() + Send + Sync>;

impl Default for Store {
    fn default() -> Self {
        Store::new()
    }
}

impl Store {
    pub fn new() -> Store {
        Store {
            documents: Mutex::new(BTreeMap::new()),
            revision: AtomicU64::new(0),
            current: RwLock::new(Snapshot::from_packed(0, Vec::new())),
            collection_listeners: Mutex::new(Vec::new()),
            inference: RwLock::new(None),
        }
    }

    /// Lets every generation published from now on recompute inferred
    /// return types on demand through `inference` (see
    /// [`ReturnInference`]).
    pub fn set_return_inference(&self, inference: Arc<dyn ReturnInference>) {
        *self
            .inference
            .write()
            .unwrap_or_else(|poison| poison.into_inner()) = Some(inference);
    }

    /// The current generation.
    pub fn snapshot(&self) -> Arc<Snapshot> {
        self.current
            .read()
            .unwrap_or_else(|poison| poison.into_inner())
            .clone()
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, BTreeMap<Str, Arc<PackedDocument>>> {
        self.documents
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
    }

    /// Publishes the workspace projection of one document.
    pub fn replace(&self, document: &Document) -> Arc<Snapshot> {
        self.replace_many(std::slice::from_ref(document))
    }

    /// Atomically publishes the workspace projections of several documents
    /// as one generation.
    pub fn replace_many(&self, documents: &[Document]) -> Arc<Snapshot> {
        self.publish_packed(
            documents
                .iter()
                .map(|document| PackedDocument::pack(document, true)),
            true,
        )
    }

    /// Publishes documents that are already workspace graphs (or whose full
    /// content should be retained) without projecting them.
    pub fn replace_many_owned(&self, documents: Vec<Document>) -> Arc<Snapshot> {
        self.publish_packed(
            documents
                .iter()
                .map(|document| PackedDocument::pack(document, false)),
            true,
        )
    }

    /// Publishes compact graphs, canonicalizing them as one batch.
    pub fn replace_workspace_graphs_owned(&self, graphs: Vec<WorkspaceGraph>) -> Arc<Snapshot> {
        self.publish_packed(graphs.into_iter().map(|graph| graph.document), true)
    }

    /// Publishes graphs already canonicalized by a batch
    /// [`crate::WorkspaceGraphDetacher`].
    pub fn replace_canonical_workspace_graphs_owned(
        &self,
        graphs: Vec<WorkspaceGraph>,
    ) -> Arc<Snapshot> {
        self.publish_packed(graphs.into_iter().map(|graph| graph.document), false)
    }

    fn publish_packed(
        &self,
        documents: impl Iterator<Item = PackedDocument>,
        canonicalize: bool,
    ) -> Arc<Snapshot> {
        let mut canon = canonicalize.then(Canon::default);
        let mut state = self.lock();
        let mut changed = false;
        for mut document in documents {
            if document.path.is_empty() {
                continue;
            }
            if let Some(canon) = &mut canon {
                document.canonicalize(canon);
            }
            state.insert(document.path.clone(), Arc::new(document));
            changed = true;
        }
        if !changed {
            return self.snapshot();
        }
        self.publish_locked(&mut state)
    }

    /// Replaces the store contents from a streaming loader of public
    /// documents and publishes one generation. On error the published
    /// generation is unchanged.
    pub fn restore_owned<E>(
        &self,
        load: impl FnOnce(&mut dyn FnMut(Document)) -> Result<(), E>,
    ) -> Result<Arc<Snapshot>, E> {
        let mut canon = Canon::default();
        self.restore(|accept| {
            load(&mut |document| {
                let mut packed = PackedDocument::pack(&document, false);
                packed.canonicalize(&mut canon);
                accept(packed);
            })
        })
    }

    /// Restores already compacted graphs without expanding them.
    pub fn restore_workspace_graphs_owned<E>(
        &self,
        capacity: WorkspaceRestoreCapacity,
        load: impl FnOnce(&mut dyn FnMut(WorkspaceGraph)) -> Result<(), E>,
    ) -> Result<Arc<Snapshot>, E> {
        let mut canon = Canon::with_capacity(capacity.types);
        self.restore(|accept| {
            load(&mut |mut graph| {
                graph.document.canonicalize(&mut canon);
                accept(graph.document);
            })
        })
    }

    /// Lets a streaming loader decode graphs through a restore-scoped
    /// decoder that shares parsed types across the restore. Graphs must come
    /// from that decoder (they are already canonical).
    pub fn restore_workspace_graphs_decoded<E>(
        &self,
        capacity: WorkspaceRestoreCapacity,
        load: impl FnOnce(&mut WorkspaceGraphDecoder, &mut dyn FnMut(WorkspaceGraph)) -> Result<(), E>,
    ) -> Result<Arc<Snapshot>, E> {
        self.restore_workspace_graphs_decoded_with(capacity, &[], load)
    }

    /// Like [`Store::restore_workspace_graphs_decoded`], also publishing the
    /// workspace projections of `documents` (runtime stubs, say) in the same
    /// generation. Building a snapshot indexes every document, so restoring
    /// and then replacing would pay for that twice.
    pub fn restore_workspace_graphs_decoded_with<E>(
        &self,
        capacity: WorkspaceRestoreCapacity,
        documents: &[Document],
        load: impl FnOnce(&mut WorkspaceGraphDecoder, &mut dyn FnMut(WorkspaceGraph)) -> Result<(), E>,
    ) -> Result<Arc<Snapshot>, E> {
        let mut decoder = WorkspaceGraphDecoder::new();
        decoder.canon = Canon::with_capacity(capacity.types);
        decoder.type_cache.reserve(capacity.types);
        self.restore(|accept| {
            load(&mut decoder, &mut |graph| accept(graph.document))?;
            for document in documents {
                let mut packed = PackedDocument::pack(document, true);
                packed.canonicalize(&mut decoder.canon);
                accept(packed);
            }
            Ok(())
        })
    }

    /// Like [`Store::restore_workspace_graphs_decoded_with`] for a large
    /// repository: `feed` hands over the encoded rows and `threads` threads
    /// run `decode` on them, each with its own decoder. The documents are
    /// canonicalized afterwards, so equal types and names are still shared
    /// across the whole restore.
    pub fn restore_workspace_graphs_parallel<E: Send>(
        &self,
        capacity: WorkspaceRestoreCapacity,
        documents: &[Document],
        threads: usize,
        feed: impl FnOnce(&mut dyn FnMut(Vec<u8>)) -> Result<(), E>,
        decode: impl Fn(&mut WorkspaceGraphDecoder, &[u8]) -> Result<WorkspaceGraph, E> + Sync,
    ) -> Result<Arc<Snapshot>, E> {
        let mut canon = Canon::with_capacity(capacity.types);
        self.restore(|accept| {
            for mut document in decode_rows(threads.max(1), feed, &decode)? {
                document.canonicalize(&mut canon);
                accept(document);
            }
            for document in documents {
                let mut packed = PackedDocument::pack(document, true);
                packed.canonicalize(&mut canon);
                accept(packed);
            }
            Ok(())
        })
    }

    fn restore<E>(
        &self,
        load: impl FnOnce(&mut dyn FnMut(PackedDocument)) -> Result<(), E>,
    ) -> Result<Arc<Snapshot>, E> {
        let mut state = self.lock();
        let mut restored: BTreeMap<Str, Arc<PackedDocument>> = BTreeMap::new();
        load(&mut |document| {
            if !document.path.is_empty() {
                restored.insert(document.path.clone(), Arc::new(document));
            }
        })?;
        if restored.is_empty() && state.is_empty() {
            return Ok(self.snapshot());
        }
        *state = restored;
        Ok(self.publish_locked(&mut state))
    }

    /// Removes documents and publishes a new generation.
    pub fn remove(&self, paths: &[&str]) -> Arc<Snapshot> {
        let mut state = self.lock();
        for path in paths {
            state.remove(*path);
        }
        self.publish_locked(&mut state)
    }

    /// Removes every document and publishes an empty generation.
    pub fn clear(&self) -> Arc<Snapshot> {
        let mut state = self.lock();
        state.clear();
        self.publish_locked(&mut state)
    }

    /// A defensive public copy of the retained graph of `path`.
    pub fn document(&self, path: &str) -> Option<Document> {
        let document = self.lock().get(path).cloned()?;
        Some(document.materialize())
    }

    /// Pins the complete persisted payload of `path` before a repository
    /// mutation can replace or remove it, so existing snapshots keep serving
    /// their generation.
    pub fn ensure_document_details(&self, path: &str) -> Result<(), LoadError> {
        if path.is_empty() {
            return Ok(());
        }
        let Some(document) = self.lock().get(path).cloned() else {
            return Ok(());
        };
        document.pin_details()
    }

    /// Registers `listener` to run after this store collected the atom
    /// table, so caches of graphs from the old generation can be dropped
    /// (they would keep it alive).
    pub fn on_atom_collection(&self, listener: impl Fn() + Send + Sync + 'static) {
        self.collection_listeners
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
            .push(Box::new(listener));
    }

    fn notify_collection(&self) {
        for listener in self
            .collection_listeners
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
            .iter()
        {
            listener();
        }
    }

    /// Collects the atom table now: re-interns every retained document into
    /// a fresh generation and publishes it. Returns false when a collection
    /// could not start (another one is running, or every generation slot is
    /// still held by live snapshots).
    pub fn collect_atoms(&self) -> bool {
        let mut state = self.lock();
        let Some(collection) = Collection::begin() else {
            return false;
        };
        rehome_all(&mut state, collection.generation());
        collection.finish();
        self.publish_locked(&mut state);
        drop(state);
        self.notify_collection();
        true
    }

    fn publish_locked(&self, state: &mut BTreeMap<Str, Arc<PackedDocument>>) -> Arc<Snapshot> {
        // Every published snapshot is single-generation: documents packed
        // before a collection (by this or another store) move to the
        // current generation, and a generation that has become mostly
        // garbage is replaced.
        let collection = should_collect().then(Collection::begin).flatten();
        match &collection {
            Some(collection) => rehome_all(state, collection.generation()),
            None => {
                let current = current_generation();
                if state.values().any(|document| !document.is_in(&current)) {
                    rehome_all(state, &current);
                }
            }
        }
        let collected = collection.is_some();
        if let Some(collection) = collection {
            collection.finish();
        }
        let revision = self.revision.fetch_add(1, Ordering::SeqCst) + 1;
        let inference = self
            .inference
            .read()
            .unwrap_or_else(|poison| poison.into_inner())
            .clone();
        let snapshot =
            Snapshot::from_packed_with(revision, state.values().cloned().collect(), inference);
        *self
            .current
            .write()
            .unwrap_or_else(|poison| poison.into_inner()) = snapshot.clone();
        if collected {
            // Listeners only drop caches; they never call back into the
            // store, so running them under the document lock is safe.
            self.notify_collection();
        }
        snapshot
    }
}

/// Moves every document from another generation into `generation`. Keys
/// are the documents' own (interned) paths, so they are rebuilt too.
fn rehome_all(state: &mut BTreeMap<Str, Arc<PackedDocument>>, generation: &Arc<Generation>) {
    *state = std::mem::take(state)
        .into_values()
        .map(|document| {
            let document = if document.is_in(generation) {
                document
            } else {
                Arc::new(document.rehome(generation))
            };
            (document.path.clone(), document)
        })
        .collect();
}

/// Rows handed to a decoding thread at once, to keep channel traffic low.
const DECODE_CHUNK_ROWS: usize = 64;

/// Decodes the rows `feed` produces on `threads` threads. The first error
/// wins; the order of the documents is unspecified.
fn decode_rows<E: Send>(
    threads: usize,
    feed: impl FnOnce(&mut dyn FnMut(Vec<u8>)) -> Result<(), E>,
    decode: &(impl Fn(&mut WorkspaceGraphDecoder, &[u8]) -> Result<WorkspaceGraph, E> + Sync),
) -> Result<Vec<PackedDocument>, E> {
    let (rows, pending) = std::sync::mpsc::sync_channel::<Vec<Vec<u8>>>(threads * 2);
    let pending = Mutex::new(pending);
    std::thread::scope(|scope| {
        let workers: Vec<_> = (0..threads)
            .map(|_| {
                scope.spawn(|| {
                    let mut decoder = WorkspaceGraphDecoder::new();
                    let mut documents = Vec::new();
                    loop {
                        let chunk = pending
                            .lock()
                            .unwrap_or_else(|poison| poison.into_inner())
                            .recv();
                        let Ok(chunk) = chunk else {
                            return Ok(documents);
                        };
                        for row in chunk {
                            documents.push(decode(&mut decoder, &row)?.document);
                        }
                    }
                })
            })
            .collect();
        let mut chunk = Vec::with_capacity(DECODE_CHUNK_ROWS);
        // A failed send means every decoder stopped on an error, which the
        // joins below report.
        let fed = feed(&mut |row| {
            chunk.push(row);
            if chunk.len() == DECODE_CHUNK_ROWS {
                let _ = rows.send(std::mem::take(&mut chunk));
            }
        });
        let _ = rows.send(chunk);
        drop(rows);
        let mut documents = Vec::new();
        let mut error = fed.err();
        for worker in workers {
            match worker.join() {
                Ok(Ok(decoded)) => documents.extend(decoded),
                Ok(Err(failure)) => error = error.or(Some(failure)),
                Err(panic) => std::panic::resume_unwind(panic),
            }
        }
        match error {
            Some(error) => Err(error),
            None => Ok(documents),
        }
    })
}
