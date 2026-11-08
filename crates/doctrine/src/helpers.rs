//! Small range and name helpers (port of Go `helpers.go`).

use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

pub(crate) fn go_trim_space(value: &str) -> &str {
    value.trim_matches(|character: char| {
        matches!(
            character,
            '\t' | '\n' | '\u{0B}' | '\u{0C}' | '\r' | ' ' | '\u{85}' | '\u{A0}'
        ) || character.is_whitespace()
    })
}

pub(crate) fn normalize_class(value: &str) -> String {
    go_trim_space(value).trim_matches('\\').to_owned()
}

pub(crate) fn has_range(range: TextRange) -> bool {
    range != TextRange::default()
}

pub(crate) fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end.max(start)))
}

/// The value range of an XML attribute, without quotes.
pub(crate) fn xml_value_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let full = node.range_trimmed_trivia();
    let raw = node.text().to_string();
    let text = go_trim_space(&raw);
    let Some(equals) = text.find('=') else {
        return full;
    };
    let value = go_trim_space(&text[equals + 1..]);
    let Some(value_offset) = text.find(value) else {
        return full;
    };
    let mut start = u32::from(full.start()) + value_offset as u32;
    let mut end = start + value.len() as u32;
    let bytes = value.as_bytes();
    if !bytes.is_empty() && (bytes[0] == b'"' || bytes[0] == b'\'') {
        start += 1;
        if bytes.len() >= 2 && bytes[bytes.len() - 1] == bytes[0] {
            end -= 1;
        }
    }
    range(start, end)
}

/// The scalar range of a YAML node, without quotes.
pub(crate) fn yaml_scalar_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let full = node.range_trimmed_trivia();
    let raw = node.text().to_string();
    let text = go_trim_space(&raw).as_bytes();
    let mut start = u32::from(full.start());
    let mut end = u32::from(full.end());
    if !text.is_empty() && (text[0] == b'\'' || text[0] == b'"') {
        start += 1;
        if text.len() >= 2 && text[text.len() - 1] == text[0] {
            end -= 1;
        }
    }
    range(start, end)
}

pub(crate) fn range_contains_cursor(range: TextRange, offset: TextSize) -> bool {
    has_range(range) && offset >= range.start() && offset <= range.end()
}
