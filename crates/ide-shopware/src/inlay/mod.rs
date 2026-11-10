//! Inlay hint providers.

mod snippet_preview;

pub use snippet_preview::SnippetPreviewProvider;
// What the inlay providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inlay::*;

#[cfg(test)]
mod tests;
