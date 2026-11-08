//! Shared test helpers (Go `helpers_test.go`, `document_test.go`).

use std::sync::Arc;

use swlsp_lsp::TextDocument;

/// Parses `source` as an open document at version 1.
pub fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

/// Go `diagnosticsDocument`.
pub fn diagnostics_document(uri: &str, content: &str) -> Arc<TextDocument> {
    document(uri, content)
}
