//! Semantic token providers (port of Go `internal/lsp/semantic`).
//!
//! - [`AdminMarkupProvider`] gives registry-backed Vue component markup in
//!   Administration Twig/Vue templates semantic meaning.
//! - [`EmbeddedLanguageProvider`] (and the single-language
//!   [`EmbeddedJsonProvider`], [`EmbeddedCssProvider`],
//!   [`EmbeddedXPathProvider`]) color JSON, CSS selectors and XPath embedded
//!   in Symfony PHP string arguments.
//! - [`TwigUxToolkitProvider`] colors `@prop`, `@block` and `@var` Twig
//!   comment annotations.

mod admin_markup;
mod embedded;
mod twig_ux_toolkit;

#[cfg(test)]
mod tests;

pub use admin_markup::AdminMarkupProvider;
pub use embedded::{
    EmbeddedCssProvider, EmbeddedJsonProvider, EmbeddedLanguageProvider, EmbeddedXPathProvider,
};
pub use twig_ux_toolkit::TwigUxToolkitProvider;

use swlsp_lsp::SemanticToken;
use swlsp_syntax::TextRange;

fn token(range: TextRange, token_type: u32) -> SemanticToken {
    SemanticToken {
        range,
        token_type,
        modifiers: 0,
    }
}

/// Orders tokens by start, then end (Go `sort.Slice` on the same keys).
fn sort_tokens(tokens: &mut [SemanticToken]) {
    tokens.sort_by(|left, right| {
        left.range
            .start()
            .cmp(&right.range.start())
            .then(left.range.end().cmp(&right.range.end()))
    });
}

fn has_suffix_fold(uri: &str, suffix: &str) -> bool {
    uri.to_lowercase().ends_with(suffix)
}
