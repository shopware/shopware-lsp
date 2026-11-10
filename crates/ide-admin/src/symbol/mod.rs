//! Document and workspace symbol providers.

mod admin_document;
mod admin_workspace;

pub use admin_document::AdminDocumentSymbolProvider;
pub use admin_workspace::AdminWorkspaceSymbolProvider;
// What the symbol providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::symbol::*;

#[cfg(test)]
mod tests;
