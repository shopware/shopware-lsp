//! Lazy loading of persisted graph details after a warm start.
//!
//! Restored summaries keep declaration cores and hierarchy edges resident and
//! load signatures and metadata through [`WorkspaceGraphRepositoryLoader`].
//! A bounded LRU keeps recently requested detail graphs hot without letting a
//! broad catalog query recreate the complete graph in memory, and concurrent
//! requests for one path share a single load.

use std::collections::HashMap;
use std::num::NonZeroUsize;
use std::sync::{Arc, Condvar, Mutex, MutexGuard};

use lru::LruCache;
use swlsp_indexer::DataIndexer;
use swlsp_php_semantic::{LoadError, WorkspaceGraph, WorkspaceGraphDecoder, WorkspaceGraphLoader};

use crate::persistence::{PersistedWorkspaceGraph, decode_persisted_workspace_graph_borrowed};

/// Number of recently requested detail graphs kept decoded.
pub const WORKSPACE_GRAPH_DETAIL_CACHE_SIZE: usize = 128;

type LoadResult = Result<Arc<WorkspaceGraph>, LoadError>;

struct PendingLoad {
    result: Mutex<Option<LoadResult>>,
    done: Condvar,
    generation: u64,
}

struct LoaderState {
    entries: LruCache<String, Arc<WorkspaceGraph>>,
    loading: HashMap<String, Arc<PendingLoad>>,
    generation: u64,
}

impl Default for LoaderState {
    fn default() -> Self {
        const CAPACITY: NonZeroUsize = NonZeroUsize::new(WORKSPACE_GRAPH_DETAIL_CACHE_SIZE)
            .expect("detail cache size is non-zero");
        LoaderState {
            entries: LruCache::new(CAPACITY),
            loading: HashMap::new(),
            generation: 0,
        }
    }
}

pub(crate) struct WorkspaceGraphRepositoryLoader {
    repository: Arc<DataIndexer<PersistedWorkspaceGraph>>,
    state: Mutex<LoaderState>,
}

fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|poison| poison.into_inner())
}

impl WorkspaceGraphRepositoryLoader {
    pub(crate) fn new(repository: Arc<DataIndexer<PersistedWorkspaceGraph>>) -> Self {
        WorkspaceGraphRepositoryLoader {
            repository,
            state: Mutex::new(LoaderState::default()),
        }
    }

    fn load_uncached(&self, path: &str) -> LoadResult {
        let mut result: Option<WorkspaceGraph> = None;
        let mut failure: Option<String> = None;
        let visited = self
            .repository
            .visit_encoded_values_by_path(path, |encoded| {
                if result.is_some() {
                    failure = Some("multiple persisted workspace graphs".to_owned());
                    return Ok(());
                }
                let decoded = decode_persisted_workspace_graph_borrowed(encoded)
                    .and_then(|persisted| persisted.decode(&mut WorkspaceGraphDecoder::new()));
                match decoded {
                    Ok(graph) => result = Some(graph),
                    Err(err) => failure = Some(err.to_string()),
                }
                Ok(())
            });
        if let Err(err) = visited {
            return Err(LoadError::new(format!(
                "load PHP workspace graph {path}: {err}"
            )));
        }
        if let Some(failure) = failure {
            return Err(LoadError::new(format!(
                "load PHP workspace graph {path}: {failure}"
            )));
        }
        result
            .map(Arc::new)
            .ok_or_else(|| LoadError::new(format!("load PHP workspace graph {path}: not found")))
    }

    /// Drops a cached graph after its file changed.
    pub(crate) fn forget(&self, path: &str) {
        lock(&self.state).entries.pop(path);
    }

    /// Drops every cached graph and invalidates loads still in flight.
    pub(crate) fn clear(&self) {
        let mut state = lock(&self.state);
        state.generation += 1;
        state.entries.clear();
    }

    #[cfg(test)]
    pub(crate) fn cached_paths(&self) -> Vec<String> {
        let mut paths: Vec<String> = lock(&self.state)
            .entries
            .iter()
            .map(|(path, _)| path.clone())
            .collect();
        paths.sort();
        paths
    }
}

impl WorkspaceGraphLoader for WorkspaceGraphRepositoryLoader {
    fn load_workspace_graph(&self, path: &str) -> LoadResult {
        let pending = {
            let mut state = lock(&self.state);
            if let Some(graph) = state.entries.get(path) {
                return Ok(graph.clone());
            }
            if let Some(pending) = state.loading.get(path) {
                let pending = pending.clone();
                drop(state);
                let mut result = lock(&pending.result);
                while result.is_none() {
                    result = pending
                        .done
                        .wait(result)
                        .unwrap_or_else(|poison| poison.into_inner());
                }
                return result.clone().expect("completed load");
            }
            let pending = Arc::new(PendingLoad {
                result: Mutex::new(None),
                done: Condvar::new(),
                generation: state.generation,
            });
            state.loading.insert(path.to_owned(), pending.clone());
            pending
        };

        let result = self.load_uncached(path);
        {
            let mut state = lock(&self.state);
            if state
                .loading
                .get(path)
                .is_some_and(|current| Arc::ptr_eq(current, &pending))
            {
                state.loading.remove(path);
            }
            if let Ok(graph) = &result
                && pending.generation == state.generation
            {
                state.entries.put(path.to_owned(), graph.clone());
            }
        }
        *lock(&pending.result) = Some(result.clone());
        pending.done.notify_all();
        result
    }
}
