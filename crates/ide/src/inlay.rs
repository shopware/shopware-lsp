//! Inlay hint providers.

use swlsp_lsp::TextDocument;
use swlsp_lsp_protocol as protocol;

/// The byte range of the requested hint range, normalized to start <= end.
pub fn inlay_hint_byte_range(document: &TextDocument, range: protocol::Range) -> (u32, u32) {
    let lines = document.line_index();
    let start = lines.offset_utf16(range.start.line, range.start.character);
    let end = lines.offset_utf16(range.end.line, range.end.character);
    if end < start {
        (end, start)
    } else {
        (start, end)
    }
}

pub fn position(document: &TextDocument, offset: u32) -> protocol::Position {
    let (line, character) = document.line_index().position_utf16(offset);
    protocol::Position { line, character }
}

pub fn line_position(line: u32) -> protocol::Position {
    protocol::Position { line, character: 0 }
}
