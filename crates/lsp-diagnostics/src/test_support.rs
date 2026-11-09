//! Shared test helpers (Go `helpers_test.go`, `document_test.go`).

use std::sync::Arc;

use swlsp_lsp::{Context, Problem, TextDocument, fixture};

use crate::ProblemAnalyzer;

/// Parses `source` as an open document at version 1.
pub fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

/// Asserts that `analyzer` reports exactly the problems marked in `fixture`
/// (see [`swlsp_lsp::fixture`]) for the document `uri`, and returns them in
/// markup order.
pub fn check_problems(
    analyzer: &dyn ProblemAnalyzer,
    uri: &str,
    fixture: &str,
) -> (Arc<TextDocument>, Vec<Problem>) {
    fixture::check(uri, fixture, |document| {
        analyzer
            .analyze(&Context::background(), document)
            .expect("analyze")
    })
}
