//! Document and workspace symbol providers.

mod dal;

pub use dal::DalWorkspaceSymbolProvider;
// What the symbol providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::symbol::*;

#[cfg(test)]
mod tests;
