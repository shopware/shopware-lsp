//! Completion providers: PHP, Twig,
//! Administration JS/Vue, YAML/XML configuration, SCSS, snippets, routes,
//! services and the other framework domains.
//!
//! Every provider implements [`swlsp_lsp::CompletionProvider`], rejects
//! unsupported contexts itself and returns deterministic items (stable
//! labels, kinds, sort/filter text, text edits, data and commands).
//! Ranges are computed as byte offsets and converted to UTF-16 only through
//! [`common::protocol_range`].

mod shopware;

pub use shopware::*;
// What the completion providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::completion::*;
