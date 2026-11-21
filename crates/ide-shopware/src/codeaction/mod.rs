//! Code actions of the Shopware domain.
//!
//! Every edit is a validated workspace edit built through `swlsp-rewrite`
//! or a protocol text edit derived from CST byte ranges; providers never
//! write files.

mod snippet;
mod snippet_sync;

pub use snippet::{
    COPY_SNIPPET_USAGE_ACTION, SnippetCodeActionProvider, SnippetCopyCodeActionProvider,
};
pub use snippet_sync::SnippetSyncCodeActionProvider;
