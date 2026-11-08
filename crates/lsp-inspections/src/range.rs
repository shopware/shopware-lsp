use swlsp_lsp::BoxError;
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, TextRange, TextSize};

/// Go `protocolTextRange`: converts an LSP UTF-16 range to byte offsets.
/// An inverted range is an error (Go's builder rejects it as invalid).
pub fn protocol_text_range(
    index: &LineIndex,
    range: protocol::Range,
) -> Result<TextRange, BoxError> {
    let start = index.offset_utf16(range.start.line, range.start.character);
    let end = index.offset_utf16(range.end.line, range.end.character);
    if end < start {
        return Err(format!("rewrite: invalid range {start}..{end}").into());
    }
    Ok(TextRange::new(TextSize::from(start), TextSize::from(end)))
}
