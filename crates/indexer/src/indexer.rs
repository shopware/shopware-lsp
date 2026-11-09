//! Indexer traits.
//!
//! Indexers implement [`Indexer`] and opt into further capabilities by
//! overriding the `as_*` accessors.

use std::any::Any;

use crate::error::{CancelToken, Result};
use crate::workspace_symbols::WorkspaceSymbol;
use crate::{Mutation, ParsedFile};

/// A value produced by [`PreparingIndexer::prepare`] ahead of the write
/// transaction.
pub type Prepared = Box<dyn Any + Send + Sync>;

/// A domain indexer driven by the file scanner.
pub trait Indexer: Send + Sync {
    /// Stable identifier used in error context.
    fn id(&self) -> &str;

    /// Indexes one file. Coordinated runs expose the shared transaction
    /// through [`ParsedFile::mutation`].
    fn index(&self, file: &ParsedFile) -> Result<()>;

    /// Removes the given source paths outside a coordinated mutation.
    fn removed_files(&self, paths: &[String]) -> Result<()>;

    /// Releases resources. The scanner never calls this: it does not own its
    /// indexers.
    fn close(&self) -> Result<()> {
        Ok(())
    }

    /// Clears all data outside a coordinated mutation.
    fn clear(&self) -> Result<()>;

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        None
    }

    fn as_supplemental_path(&self) -> Option<&dyn SupplementalPathIndexer> {
        None
    }

    fn as_supplemental_syntax(&self) -> Option<&dyn SupplementalSyntaxIndexer> {
        None
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        None
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        None
    }

    fn as_batch(&self) -> Option<&dyn BatchIndexer> {
        None
    }

    fn as_workspace_symbol_contributor(&self) -> Option<&dyn WorkspaceSymbolContributor> {
        None
    }
}

/// Moves CPU- and allocation-heavy, read-only work ahead of the workspace
/// write transaction. `prepare` may run concurrently for different files and
/// never sees a mutation. `index_prepared` must only persist the prepared
/// value and arrange post-commit publication; it replaces
/// [`Indexer::index`] for this indexer.
pub trait PreparingIndexer: Send + Sync {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>>;

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()>;
}

/// Lets an indexer opt into files outside the syntax registry and
/// selectively reopen otherwise skipped directories (for example public
/// assets) without making every indexer scan generated media or caches.
pub trait SupplementalPathIndexer: Send + Sync {
    fn should_enter_directory(&self, path: &str) -> bool;

    fn should_index_path(&self, path: &str) -> bool;
}

/// Lets a supplemental path indexer request the normal ahead-of-transaction
/// syntax preparation for selected files.
pub trait SupplementalSyntaxIndexer: Send + Sync {
    fn should_preparse_path(&self, path: &str) -> bool;
}

/// Groups removals of all indexers in one shared workspace transaction.
pub trait TransactionalRemover: Send + Sync {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()>;
}

/// Groups clears of all indexers in one shared workspace transaction.
pub trait TransactionalClearer: Send + Sync {
    fn clear_in(&self, mutation: &Mutation) -> Result<()>;
}

/// Notified around one coordinated indexing run. Indexers with expensive
/// immutable publication can accumulate committed updates and publish once.
///
/// `candidate_files` is the filtered, sorted input and may only be inspected
/// for allocation hints. `end_indexing_batch` is called for every begin,
/// including cancelled and failed runs.
pub trait BatchIndexer: Send + Sync {
    fn begin_indexing_batch(&self, candidate_files: &[String]);

    fn end_indexing_batch(&self) -> Result<()>;

    /// Called after every batch indexer ended a run that was not cancelled,
    /// before consumers are notified. Lets an indexer whose prepared data
    /// depends on other files of the run refresh it now that the whole run is
    /// published, preparing on up to `workers` threads.
    fn settle_indexing_batch(&self, _cancel: &CancelToken, _workers: usize) -> Result<()> {
        Ok(())
    }
}

/// Publishes declarations from the same prepared value an indexer already
/// computed for persistence.
pub trait WorkspaceSymbolContributor: Send + Sync {
    fn workspace_symbols(
        &self,
        file: &ParsedFile,
        prepared: Option<&Prepared>,
    ) -> Result<Vec<WorkspaceSymbol>>;
}
