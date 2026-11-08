//! Completion providers (port of Go `internal/lsp/completion`): PHP, Twig,
//! Administration JS/Vue, YAML/XML configuration, SCSS, snippets, routes,
//! services and the other framework domains.
//!
//! Every provider implements [`swlsp_lsp::CompletionProvider`], rejects
//! unsupported contexts itself and returns deterministic items whose JSON
//! matches Go (labels, kinds, sort/filter text, text edits, data, commands).
//! Ranges are computed as byte offsets and converted to UTF-16 only through
//! [`common::protocol_range`].

pub mod common;

mod admin;
pub use admin::*;

mod twig;
pub use twig::*;

mod component;
pub use component::*;

mod symfony;
pub use symfony::*;

mod domain;
pub use domain::*;

#[cfg(test)]
pub(crate) mod test_support;
