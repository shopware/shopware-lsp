//! Errors of the domain analyzers.

use swlsp_lsp::BoxError;

/// An index query or resolution that failed while analyzing a document,
/// with the operation that was attempted.
#[derive(Debug, thiserror::Error)]
#[error("{operation}: {source}")]
pub struct LookupError {
    operation: String,
    #[source]
    source: BoxError,
}

impl LookupError {
    pub(crate) fn new(operation: impl Into<String>, source: impl Into<BoxError>) -> Self {
        Self {
            operation: operation.into(),
            source: source.into(),
        }
    }

    /// The attempted operation, e.g. `query Symfony routes`.
    pub fn operation(&self) -> &str {
        &self.operation
    }
}
