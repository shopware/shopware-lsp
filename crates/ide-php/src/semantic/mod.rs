//! Semantic token providers.
//!
//! - [`AdminMarkupProvider`] gives registry-backed Vue component markup in
//!   Administration Twig/Vue templates semantic meaning.
//! - [`EmbeddedLanguageProvider`] (and the single-language
//!   [`EmbeddedJsonProvider`], [`EmbeddedCssProvider`],
//!   [`EmbeddedXPathProvider`]) color JSON, CSS selectors and XPath embedded
//!   in Symfony PHP string arguments.
//! - [`TwigUxToolkitProvider`] colors `@prop`, `@block` and `@var` Twig
//!   comment annotations.

mod embedded;

pub use embedded::{
    EmbeddedCssProvider, EmbeddedJsonProvider, EmbeddedLanguageProvider, EmbeddedXPathProvider,
};
// What the semantic providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::semantic::*;

#[cfg(test)]
mod tests;
