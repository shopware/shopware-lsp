use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, TextRange, TextSize};

/// A protocol range whose end lies before its start.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
#[error("invalid range {start}..{end}: end precedes start")]
pub struct InvertedRangeError {
    pub start: u32,
    pub end: u32,
}

/// Converts an LSP UTF-16 range to byte offsets.
pub fn protocol_text_range(
    index: &LineIndex,
    range: protocol::Range,
) -> Result<TextRange, InvertedRangeError> {
    let start = index.offset_utf16(range.start.line, range.start.character);
    let end = index.offset_utf16(range.end.line, range.end.character);
    if end < start {
        return Err(InvertedRangeError { start, end });
    }
    Ok(TextRange::new(TextSize::from(start), TextSize::from(end)))
}
