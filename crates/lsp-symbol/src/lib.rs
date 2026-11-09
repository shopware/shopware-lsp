//! Document and workspace symbol providers.

mod admin_document;
mod admin_workspace;
mod catalog;
mod dal;
mod symfony;

pub use admin_document::AdminDocumentSymbolProvider;
pub use admin_workspace::AdminWorkspaceSymbolProvider;
pub use catalog::CatalogWorkspaceSymbolProvider;
pub use dal::DalWorkspaceSymbolProvider;
pub use symfony::SymfonyWorkspaceSymbolProvider;

use swlsp_lsp_protocol as protocol;

#[cfg(test)]
mod tests;

/// The LSP symbol kind with numeric value `value` (lsp-types keeps the
/// constructor private).
pub(crate) fn symbol_kind(value: i32) -> protocol::SymbolKind {
    serde_json::from_value(serde_json::Value::from(value)).unwrap_or(protocol::SymbolKind::NULL)
}

pub(crate) fn symbol_kind_number(kind: protocol::SymbolKind) -> i64 {
    serde_json::to_value(kind)
        .ok()
        .and_then(|value| value.as_i64())
        .unwrap_or_default()
}

/// A zero-width location at the 1-based `line` (lines below 1 clamp to 1).
pub(crate) fn line_location(path: &str, line: i64) -> protocol::Location {
    let line = (line.max(1) - 1) as u32;
    let position = protocol::Position { line, character: 0 };
    protocol::Location {
        uri: swlsp_uriutil::file_uri(path),
        range: protocol::Range {
            start: position,
            end: position,
        },
    }
}

/// Whether the lowercased `text` contains `lowered_query`, which the caller
/// has already lowercased.
pub(crate) fn lower_contains(text: &str, lowered_query: &str) -> bool {
    swlsp_textutil::lowercase(text).contains(lowered_query)
}
