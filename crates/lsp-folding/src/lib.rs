//! Folding range providers (port of `internal/lsp/folding`).
//!
//! Both providers read only the open document's native CST, so unsaved
//! edits are immediately authoritative and folding keeps no workspace state.

mod admin;
mod php;

pub use admin::AdminFoldingProvider;
pub use php::PhpFoldingProvider;

#[cfg(test)]
mod tests;
