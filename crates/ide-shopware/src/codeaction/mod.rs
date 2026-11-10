//! Code actions of the Shopware domain.
//!
//! Every edit is a validated workspace edit built through `swlsp-rewrite`
//! or a protocol text edit derived from CST byte ranges; providers never
//! write files.

mod snippet;

pub use snippet::{
    COPY_SNIPPET_USAGE_ACTION, SnippetCodeActionProvider, SnippetCopyCodeActionProvider,
};
