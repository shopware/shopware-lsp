//! Rename and refactor providers: Twig template file renames, typed Twig usages
//! of renamed PHP symbols and Administration registry/contract renames.

use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, TextRange};

/// A byte range as a UTF-16 protocol range.
pub fn protocol_range(line_index: &LineIndex, range: TextRange) -> protocol::Range {
    let (start_line, start_character) = line_index.position_utf16(u32::from(range.start()));
    let (end_line, end_character) = line_index.position_utf16(u32::from(range.end()));
    protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    }
}

/// The lowercased extension (with its dot) of a path or URI.
pub fn lower_ext(value: &str) -> String {
    swlsp_uriutil::filepath::ext(value).to_lowercase()
}
