//! Workspace indexing infrastructure (port of Go `internal/indexer`).
//!
//! - [`Store`] is the single namespaced SQLite database of a workspace; a
//!   [`Mutation`] is its one coordinated write transaction.
//! - [`DataIndexer`] is the generic per-path repository on top of it.
//! - [`FileScanner`] discovers files, detects changes, prepares them outside
//!   the transaction and persists each batch through one mutation. Its
//!   watcher is the only source of filesystem index events.
//! - [`WorkspaceSymbolCatalog`] is the FTS-backed workspace symbol search.

mod dataindex;
mod error;
mod filetypes;
mod indexer;
mod parsed_file;
mod path_exclusions;
mod paths;
mod phar_sources;
mod scanner;
mod scanner_index;
mod store;
mod vendor_packages;
mod version;
mod watcher;
mod workspace_symbols;

pub use dataindex::{DataIndexer, decode_value, encode_value};
pub use error::{CancelToken, Error, Result, ResultExt};
pub use filetypes::{is_scanned_path, language_for_path};
pub use indexer::{
    BatchIndexer, Indexer, Prepared, PreparingIndexer, SupplementalPathIndexer,
    SupplementalSyntaxIndexer, TransactionalClearer, TransactionalRemover,
    WorkspaceSymbolContributor,
};
pub use parsed_file::ParsedFile;
pub use path_exclusions::PathExclusions;
pub use scanner::{
    DEFAULT_MAX_FILE_SIZE_BYTES, FileScanner, FileScannerStats, FileSizeStats,
    MAXIMUM_SOURCE_FILE_BYTES, SkippedFileStats, should_skip_relative_path,
};
pub use store::{Mutation, MutationCache, MutationConnection, Store};
pub use vendor_packages::VendorPackages;
pub use version::{
    INDEX_VERSION, cache_version_current, check_and_migrate_cache, check_and_migrate_configuration,
    index_version_marker,
};
pub use workspace_symbols::{
    WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
    WORKSPACE_SYMBOL_PRIORITY_PHP_GLOBAL, WORKSPACE_SYMBOL_PRIORITY_PHP_MEMBER,
    WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE, WorkspaceSymbol, WorkspaceSymbolCatalog,
    WorkspaceSymbolDocument, WorkspaceSymbolKind, WorkspaceSymbolPosition,
    WorkspaceSymbolQueryOptions, WorkspaceSymbolRange,
};

#[cfg(test)]
mod tests;
