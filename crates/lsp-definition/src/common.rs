//! Location helpers shared by the definition providers.

use swlsp_lsp::DefinitionRequest;
use swlsp_lsp_protocol as protocol;
use swlsp_php_semantic::Symbol;
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_uriutil::file_uri;

/// Lowercased extension of a URI or path including the dot.
pub(crate) fn ext_lower(uri: &str) -> String {
    let base = uri.rsplit('/').next().unwrap_or(uri);
    match base.rfind('.') {
        Some(index) => base[index..].to_ascii_lowercase(),
        None => String::new(),
    }
}

/// The byte offset of the request position in the open document.
pub(crate) fn request_offset(request: &DefinitionRequest) -> Option<u32> {
    let lines = request.line_index()?;
    Some(lines.offset_utf16(
        request.params.position.line,
        request.params.position.character,
    ))
}

pub(crate) fn request_uri(request: &DefinitionRequest) -> &str {
    &request.params.text_document.uri
}

pub(crate) fn position(lines: &LineIndex, offset: u32) -> protocol::Position {
    let (line, character) = lines.position_utf16(offset);
    protocol::Position { line, character }
}

pub(crate) fn protocol_range(lines: &LineIndex, range: TextRange) -> protocol::Range {
    protocol::Range {
        start: position(lines, range.start().into()),
        end: position(lines, range.end().into()),
    }
}

/// Reads `path` and converts a byte range to a location; `None` when the
/// file cannot be read.
pub(crate) fn file_range_location(path: &str, range: TextRange) -> Option<protocol::Location> {
    let source = std::fs::read(path).ok()?;
    let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
    Some(protocol::Location {
        uri: file_uri(path),
        range: protocol_range(&lines, range),
    })
}

/// Like [`file_range_location`] but falls back to an empty range at the file
/// URI.
pub(crate) fn file_range_location_or_uri(path: &str, range: TextRange) -> protocol::Location {
    file_range_location(path, range).unwrap_or_else(|| protocol::Location {
        uri: file_uri(path),
        range: protocol::Range::default(),
    })
}

/// The location of the symbol's selection range.
pub(crate) fn php_symbol_location(symbol: &Symbol) -> protocol::Location {
    file_range_location_or_uri(&symbol.path, symbol.selection_range)
}

/// A zero-width location at the start
/// of a one-based line (clamped to 1).
pub(crate) fn line_location(path: &str, line: i64) -> protocol::Location {
    let line = line.max(1) as u32 - 1;
    let position = protocol::Position { line, character: 0 };
    protocol::Location {
        uri: file_uri(path),
        range: protocol::Range {
            start: position,
            end: position,
        },
    }
}
