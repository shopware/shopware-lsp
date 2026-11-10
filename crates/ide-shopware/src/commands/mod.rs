//! Custom `shopware/...` workspace commands.
//!
//! Command IDs and JSON shapes are client contracts used by the editors.
//! Commands never write files: edits are compiled from a validated
//! [`WorkspacePlan`](swlsp_rewrite::WorkspacePlan) through the host and
//! returned to the client.

mod extension;
mod json_edit;
mod pretty;
mod snippet;

pub use extension::ExtensionCommandProvider;
pub use snippet::{SnippetCommandProvider, SnippetFile};
// What the commands providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::commands::*;

#[cfg(test)]
mod tests;
