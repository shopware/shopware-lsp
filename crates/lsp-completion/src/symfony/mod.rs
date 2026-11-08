//! Symfony configuration, translation and snippet completion (ports of Go
//! `service_completion.go`, `translation_completion.go`,
//! `snippet_completion.go`, `bundle_resource_completion.go`,
//! `yaml_service_authoring_completion.go` and
//! `yaml_route_authoring_completion.go`).

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

use crate::common::go_to_lower;

/// Go `strings.ToLower(filepath.Ext(uri))`.
fn lower_ext(uri: &str) -> String {
    go_to_lower(swlsp_uriutil::filepath::ext(uri))
}

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

/// The byte offset of the request position (Go
/// `LineIndex.OffsetUTF16(Position)`).
fn request_offset(request: &CompletionRequest) -> Option<u32> {
    let line_index = request.line_index()?;
    Some(line_index.offset_utf16(
        request.params.position.line,
        request.params.position.character,
    ))
}
