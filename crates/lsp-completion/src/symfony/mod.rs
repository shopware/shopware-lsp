//! Symfony configuration, translation and snippet completion: services,
//! translations, snippets, bundle resources and YAML service and route
//! authoring.

mod bundle_resource;
mod service;
mod snippet;
mod translation;
mod yaml_route_authoring;
mod yaml_service_authoring;

#[cfg(test)]
mod tests;

pub use bundle_resource::BundleResourceCompletionProvider;
pub use service::ServiceCompletionProvider;
pub use snippet::SnippetCompletionProvider;
pub use translation::TranslationCompletionProvider;
pub use yaml_route_authoring::YamlRouteAuthoringCompletionProvider;
pub use yaml_service_authoring::YamlServiceAuthoringCompletionProvider;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;

/// The lowercase extension of `uri`, including the leading dot.
use crate::common::lower_ext;

/// A markdown documentation value.
fn markdown(value: impl Into<String>) -> Option<protocol::MarkupContent> {
    Some(protocol::MarkupContent {
        kind: protocol::MarkupKind::Markdown,
        value: value.into(),
    })
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
