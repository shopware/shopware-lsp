//! Lazy loading of persisted graph details after a warm start.
//!
//! Restored summaries keep declaration cores and hierarchy edges resident and
//! load signatures and metadata through [`WorkspaceGraphRepositoryLoader`].
//! A bounded LRU keeps recently requested detail graphs hot without letting a
//! broad catalog query recreate the complete graph in memory, and concurrent
//! requests for one path share a single load.

use std::collections::HashMap;
use std::sync::{Arc, Condvar, Mutex, MutexGuard};

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

#[derive(Default)]
struct LoaderState {
    entries: HashMap<String, (Arc<WorkspaceGraph>, u64)>,
    clock: u64,
    loading: HashMap<String, Arc<PendingLoad>>,
    generation: u64,
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
        lock(&self.state).entries.remove(path);
    }

    /// Drops every cached graph and invalidates loads still in flight.
    pub(crate) fn clear(&self) {
        let mut state = lock(&self.state);
        state.generation += 1;
        state.entries = HashMap::new();
    }

    #[cfg(test)]
    pub(crate) fn cached_paths(&self) -> Vec<String> {
        let mut paths: Vec<String> = lock(&self.state).entries.keys().cloned().collect();
        paths.sort();
        paths
    }
}

impl WorkspaceGraphLoader for WorkspaceGraphRepositoryLoader {
    fn load_workspace_graph(&self, path: &str) -> LoadResult {
        let pending = {
            let mut state = lock(&self.state);
            state.clock += 1;
            let clock = state.clock;
            if let Some((graph, used)) = state.entries.get_mut(path) {
                *used = clock;
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
                state.clock += 1;
                let clock = state.clock;
                state
                    .entries
                    .insert(path.to_owned(), (graph.clone(), clock));
                if state.entries.len() > WORKSPACE_GRAPH_DETAIL_CACHE_SIZE
                    && let Some(oldest) = state
                        .entries
                        .iter()
                        .min_by_key(|(_, (_, used))| *used)
                        .map(|(path, _)| path.clone())
                {
                    state.entries.remove(&oldest);
                }
            }
        }
        *lock(&pending.result) = Some(result.clone());
        pending.done.notify_all();
        result
    }
}
