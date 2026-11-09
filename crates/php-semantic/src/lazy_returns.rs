//! On-demand inferred return types.
//!
//! A declaration without a declared return type gets one inferred from its
//! body ([`Flags::INFERRED_RETURN`](crate::Flags::INFERRED_RETURN)). The body
//! can call into other files, so the type stored with the declaration is only
//! as complete as the generation its file was indexed against, and it goes
//! stale when a file it depends on changes.
//!
//! Like rust-analyzer's query system, a generation published with a
//! [`ReturnInference`] does not trust stored inferred types: materializing
//! such a declaration asks the inference for its file, analyzed against this
//! generation. Results are memoized per file for the lifetime of the
//! generation; a new generation starts with an empty memo, so every answer
//! reflects the files of the generation it was asked in. Analyses recurse on
//! demand, and a cycle (a file whose analysis needs its own inferred returns)
//! falls back to the stored types.

use std::cell::RefCell;
use std::sync::{Arc, Mutex, Weak};

use rustc_hash::FxHashMap;
use swlsp_php_types::Type;

use crate::Snapshot;
use crate::symbol::SymbolId;

/// Analyzes files on demand for [`Snapshot`]s published with it.
pub trait ReturnInference: Send + Sync {
    /// The return types of the declarations in `path` flagged
    /// [`Flags::INFERRED_RETURN`](crate::Flags::INFERRED_RETURN), analyzed
    /// against `generation`. `None` when the file cannot be analyzed; the
    /// stored types are used then.
    fn infer_returns(
        &self,
        generation: &Arc<Snapshot>,
        path: &str,
    ) -> Option<Vec<(SymbolId, Type)>>;
}

type FileReturns = Arc<FxHashMap<SymbolId, Type>>;

/// The memoized inferred returns of one generation, shared by every
/// snapshot derived from it.
pub(crate) struct LazyReturns {
    inference: Arc<dyn ReturnInference>,
    generation: Weak<Snapshot>,
    files: Mutex<FxHashMap<String, FileReturns>>,
}

thread_local! {
    /// Files this thread is inferring, per generation (by memo address).
    static ACTIVE: RefCell<Vec<(usize, String)>> = const { RefCell::new(Vec::new()) };
}

impl LazyReturns {
    pub(crate) fn new(inference: Arc<dyn ReturnInference>, generation: Weak<Snapshot>) -> Self {
        LazyReturns {
            inference,
            generation,
            files: Mutex::default(),
        }
    }

    /// The inferred return type of declaration `id` in `path` in this
    /// generation, or `None` to keep the stored one.
    pub(crate) fn return_type(&self, path: &str, id: &str) -> Option<Type> {
        self.file(path)?.get(id).cloned()
    }

    /// Number of files inferred so far (tests and statistics).
    pub(crate) fn inferred_files(&self) -> usize {
        self.lock().len()
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, FxHashMap<String, FileReturns>> {
        self.files
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
    }

    fn file(&self, path: &str) -> Option<FileReturns> {
        if let Some(found) = self.lock().get(path) {
            return Some(found.clone());
        }
        let key = self as *const LazyReturns as usize;
        let cyclic = ACTIVE.with(|active| {
            active
                .borrow()
                .iter()
                .any(|(owner, active)| *owner == key && active == path)
        });
        if cyclic {
            return None;
        }
        let generation = self.generation.upgrade()?;
        // Computed without holding the memo lock: inference recurses into
        // other files, possibly on other threads. Concurrent computations of
        // one file are equal; the first stored wins.
        let returns = {
            let _active = ActiveFile::enter(key, path);
            self.inference.infer_returns(&generation, path)
        };
        let returns: FileReturns = Arc::new(returns.unwrap_or_default().into_iter().collect());
        Some(
            self.lock()
                .entry(path.to_owned())
                .or_insert(returns)
                .clone(),
        )
    }
}

/// Marks a file as being inferred on this thread until dropped.
struct ActiveFile;

impl ActiveFile {
    fn enter(key: usize, path: &str) -> Self {
        ACTIVE.with(|active| active.borrow_mut().push((key, path.to_owned())));
        ActiveFile
    }
}

impl Drop for ActiveFile {
    fn drop(&mut self) {
        ACTIVE.with(|active| {
            active.borrow_mut().pop();
        });
    }
}
