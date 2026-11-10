//! Completion in Shopware's own formats: the data abstraction layer,
//! feature flags, system configuration, themes and snippets.

mod dal;
mod feature;
mod snippet;
mod systemconfig;
mod theme;

#[cfg(test)]
mod tests;

pub use dal::DalCompletionProvider;
pub use feature::FeatureCompletionProvider;
pub use snippet::SnippetCompletionProvider;
pub use systemconfig::SystemConfigCompletionProvider;
pub use theme::ThemeCompletionProvider;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;

pub(crate) use crate::completion::common::lower_ext;

pub(crate) fn item(
    label: impl Into<String>,
    kind: protocol::CompletionItemKind,
    detail: impl Into<String>,
) -> protocol::CompletionItem {
    protocol::CompletionItem {
        label: label.into(),
        kind: Some(kind),
        detail: detail.into(),
        ..Default::default()
    }
}

/// A plaintext documentation value.
fn plaintext(value: impl Into<String>) -> Option<protocol::MarkupContent> {
    Some(protocol::MarkupContent {
        kind: protocol::MarkupKind::PlainText,
        value: value.into(),
    })
}

/// The byte offset of the request position.
fn request_offset(request: &CompletionRequest) -> Option<u32> {
    let line_index = request.line_index()?;
    Some(line_index.offset_utf16(
        request.params.position.line,
        request.params.position.character,
    ))
}
