//! Small helpers shared by the component group.

use regex::Regex;
use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;

/// The byte offset of the request position.
pub(crate) fn request_offset(request: &CompletionRequest) -> Option<u32> {
    let line_index = request.line_index()?;
    Some(line_index.offset_utf16(
        request.params.position.line,
        request.params.position.character,
    ))
}

/// The file path of the request URI; empty when it is not a file URI.
pub(crate) fn request_path(request: &CompletionRequest) -> String {
    swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default()
}

/// The source before `offset`, clamped to the source length and moved back
/// to a character boundary.
pub(crate) fn source_before(source: &str, offset: u32) -> &str {
    let mut offset = (offset as usize).min(source.len());
    while !source.is_char_boundary(offset) {
        offset -= 1;
    }
    &source[..offset]
}

/// Compiles a context pattern with ASCII semantics for `\b` and `\s`: word
/// boundaries only consider ASCII word characters and `\s` matches
/// `[\t\n\f\r ]`, so non-ASCII text next to an attribute cannot change a match.
pub(crate) fn ascii_regex(pattern: &str) -> Regex {
    let translated = pattern
        .replace(r"\b", r"(?-u:\b)")
        .replace(r"\s", r"[\t\n\f\r ]");
    Regex::new(&translated).expect("valid context pattern")
}

/// The byte index of the last space, tab, carriage return or newline.
pub(crate) fn last_index_whitespace(value: &str) -> Option<usize> {
    value.rfind([' ', '\t', '\r', '\n'])
}

pub(crate) fn kind(kind: protocol::CompletionItemKind) -> Option<protocol::CompletionItemKind> {
    Some(kind)
}

pub(crate) fn markdown(value: &str) -> Option<protocol::MarkupContent> {
    Some(protocol::MarkupContent {
        kind: protocol::MarkupKind::Markdown,
        value: value.to_string(),
    })
}

pub(crate) fn snippet() -> Option<protocol::InsertTextFormat> {
    Some(protocol::InsertTextFormat::SNIPPET)
}
