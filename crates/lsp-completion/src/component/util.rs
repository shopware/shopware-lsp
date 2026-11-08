//! Small helpers shared by the component group.

use regex::Regex;
use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;

/// The byte offset of the request position (Go `LineIndex.OffsetUTF16`).
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

/// Go `string(source[:offset])` clamped to the source length.
pub(crate) fn source_before(source: &str, offset: u32) -> &str {
    let mut offset = (offset as usize).min(source.len());
    while !source.is_char_boundary(offset) {
        offset -= 1;
    }
    &source[..offset]
}

/// Compiles a Go RE2 pattern: `\b` is ASCII-only and `\s` is
/// `[\t\n\f\r ]` in Go.
pub(crate) fn go_regex(pattern: &str) -> Regex {
    let translated = pattern
        .replace(r"\b", r"(?-u:\b)")
        .replace(r"\s", r"[\t\n\f\r ]");
    Regex::new(&translated).expect("valid Go-compatible pattern")
}

/// Go `strings.LastIndexAny(value, " \t\r\n")`.
pub(crate) fn last_index_whitespace(value: &str) -> Option<usize> {
    value.rfind([' ', '\t', '\r', '\n'])
}

/// Go `strings.ToUpper` (one rune to one rune).
pub(crate) fn go_to_upper(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_uppercase();
    }
    value
        .chars()
        .map(|c| {
            let mut upper = c.to_uppercase();
            match (upper.next(), upper.next()) {
                (Some(first), None) => first,
                _ => c,
            }
        })
        .collect()
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
