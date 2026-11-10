//! Document and workspace symbol providers.

mod symfony;

pub use symfony::SymfonyWorkspaceSymbolProvider;
// What the symbol providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::symbol::*;

#[cfg(test)]
mod tests;
