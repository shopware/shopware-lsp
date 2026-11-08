//! Indentation-aware YAML lexer (port of Go `internal/parser/yaml/lexer`).

use swlsp_syntax::{Kind, Token};

use crate::syntax::*;

struct Scanner<'a> {
    source: &'a str,
    bytes: &'a [u8],
    position: usize,
    line_start: usize,
    current_indent: usize,
    flow_depth: usize,
    line_content_start: bool,
}

/// Splits `source` into lossless tokens.
pub fn lex(source: &str) -> Vec<Token<'_>> {
    let mut scanner = Scanner {
        source,
        bytes: source.as_bytes(),
        position: 0,
        line_start: 0,
        current_indent: 0,
        flow_depth: 0,
        line_content_start: true,
    };
    let mut tokens = Vec::with_capacity(source.len() / 4 + 1);
    while scanner.position < source.len() {
        let start = scanner.position;
        let (kind, end) = scanner.next();
        assert!(end > start, "yaml lexer did not make progress");
        tokens.push(Token::new(kind, source, start as u32, end as u32));
        scanner.position = end;
    }
    tokens
}

impl Scanner<'_> {
    fn next(&mut self) -> (Kind, usize) {
        let source = self.bytes;
        let position = self.position;
        if position == self.line_start {
            self.current_indent = 0;
            self.line_content_start = true;
            let mut end = position;
            while end < source.len() && (source[end] == b' ' || source[end] == b'\t') {
                self.current_indent += if source[end] == b'\t' { 2 } else { 1 };
                end += 1;
            }
            if end > position {
                return (TK_INDENT, end);
            }
        }

        match source[position] {
            b'\r' | b'\n' => {
                let end = scan_line_break(source, position);
                self.line_start = end;
                self.current_indent = 0;
                self.line_content_start = true;
                return (TK_LINE_BREAK, end);
            }
            b' ' | b'\t' => {
                let mut end = position + 1;
                while end < source.len() && (source[end] == b' ' || source[end] == b'\t') {
                    end += 1;
                }
                return (TK_WHITESPACE, end);
            }
            b'#' => return (TK_COMMENT, line_end(source, position)),
            b'%' if self.line_content_start && self.flow_depth == 0 => {
                self.line_content_start = false;
                return (TK_DIRECTIVE, line_end(source, position));
            }
            b'-' if self.line_content_start && has_marker(source, position, b"---") => {
                self.line_content_start = false;
                return (TK_DOCUMENT_START, position + 3);
            }
            b'-' if self.line_content_start && followed_by_separation(source, position + 1) => {
                self.line_content_start = false;
                return (TK_DASH, position + 1);
            }
            b'.' if self.line_content_start && has_marker(source, position, b"...") => {
                self.line_content_start = false;
                return (TK_DOCUMENT_END, position + 3);
            }
            b':' if colon_is_indicator(source, position, self.flow_depth) => {
                self.line_content_start = false;
                return (TK_COLON, position + 1);
            }
            b',' if self.flow_depth > 0 => {
                self.line_content_start = false;
                return (TK_COMMA, position + 1);
            }
            b'[' | b'{' => {
                self.flow_depth += 1;
                self.line_content_start = false;
                let kind = if source[position] == b'[' {
                    TK_OPEN_BRACKET
                } else {
                    TK_OPEN_BRACE
                };
                return (kind, position + 1);
            }
            b']' | b'}' => {
                self.flow_depth = self.flow_depth.saturating_sub(1);
                self.line_content_start = false;
                let kind = if source[position] == b']' {
                    TK_CLOSE_BRACKET
                } else {
                    TK_CLOSE_BRACE
                };
                return (kind, position + 1);
            }
            b'\'' => {
                self.line_content_start = false;
                return (
                    TK_SINGLE_QUOTED_SCALAR,
                    scan_single_quoted(source, position),
                );
            }
            b'"' => {
                self.line_content_start = false;
                return (
                    TK_DOUBLE_QUOTED_SCALAR,
                    scan_double_quoted(source, position),
                );
            }
            b'|' | b'>'
                if self.flow_depth == 0 && is_block_scalar_header(self.source, position) =>
            {
                let end = scan_block_scalar(source, position, self.current_indent);
                if let Some(newline) = source[position..end]
                    .iter()
                    .rposition(|byte| *byte == b'\n')
                {
                    self.line_start = position + newline + 1;
                    if end == self.line_start {
                        self.line_content_start = true;
                        self.current_indent = 0;
                    }
                }
                return (TK_BLOCK_SCALAR, end);
            }
            _ => {}
        }

        let end = scan_plain(source, position, self.flow_depth);
        self.line_content_start = false;
        if end == position {
            // Go emits one byte; Rust keeps the character whole.
            return (TK_UNKNOWN, position + utf8_len(source[position]));
        }
        (TK_PLAIN_SCALAR, end)
    }
}

fn utf8_len(first: u8) -> usize {
    match first {
        0xF0..=0xF7 => 4,
        0xE0..=0xEF => 3,
        0xC0..=0xDF => 2,
        _ => 1,
    }
}

fn scan_line_break(source: &[u8], position: usize) -> usize {
    if source[position] == b'\r' && source.get(position + 1) == Some(&b'\n') {
        position + 2
    } else {
        position + 1
    }
}

fn line_end(source: &[u8], position: usize) -> usize {
    source[position..]
        .iter()
        .position(|byte| *byte == b'\r' || *byte == b'\n')
        .map_or(source.len(), |offset| position + offset)
}

fn has_marker(source: &[u8], position: usize, marker: &[u8]) -> bool {
    source[position..].starts_with(marker)
        && followed_by_separation(source, position + marker.len())
}

fn followed_by_separation(source: &[u8], position: usize) -> bool {
    match source.get(position) {
        None => true,
        Some(byte) => matches!(
            byte,
            b' ' | b'\t' | b'\r' | b'\n' | b'#' | b',' | b']' | b'}'
        ),
    }
}

fn colon_is_indicator(source: &[u8], position: usize, flow_depth: usize) -> bool {
    match source.get(position + 1) {
        None => true,
        Some(b' ' | b'\t' | b'\r' | b'\n' | b'#') => true,
        Some(b',' | b']' | b'}') => flow_depth > 0,
        Some(_) => false,
    }
}

fn scan_single_quoted(source: &[u8], position: usize) -> usize {
    let mut end = position + 1;
    while end < source.len() {
        match source[end..].iter().position(|byte| *byte == b'\'') {
            None => break,
            Some(offset) => end += offset,
        }
        if source.get(end + 1) == Some(&b'\'') {
            end += 2;
            continue;
        }
        return end + 1;
    }
    source.len()
}

fn scan_double_quoted(source: &[u8], position: usize) -> usize {
    let mut end = position + 1;
    while end < source.len() {
        match source[end..]
            .iter()
            .position(|byte| *byte == b'"' || *byte == b'\\')
        {
            None => break,
            Some(offset) => end += offset,
        }
        if source[end] == b'"' {
            return end + 1;
        }
        end += 2;
    }
    source.len()
}

fn is_block_scalar_header(source: &str, position: usize) -> bool {
    let end = line_end(source.as_bytes(), position);
    let mut header = source[position + 1..end].trim();
    if let Some(comment) = header.find('#') {
        header = header[..comment].trim();
    }
    if header.len() > 2 {
        return false;
    }
    header
        .chars()
        .all(|value| value == '+' || value == '-' || ('1'..='9').contains(&value))
}

fn scan_block_scalar(source: &[u8], position: usize, base_indent: usize) -> usize {
    let header_end = line_end(source, position);
    if header_end == source.len() {
        return header_end;
    }
    let mut cursor = scan_line_break(source, header_end);
    while cursor < source.len() {
        let end = line_end(source, cursor);
        let (indent, content) = indentation(source, cursor, end);
        if content == end {
            if end == source.len() {
                return end;
            }
            cursor = scan_line_break(source, end);
            continue;
        }
        if indent <= base_indent {
            return cursor;
        }
        if end == source.len() {
            return end;
        }
        cursor = scan_line_break(source, end);
    }
    cursor
}

fn indentation(source: &[u8], start: usize, end: usize) -> (usize, usize) {
    let mut width = 0;
    let mut position = start;
    while position < end {
        match source[position] {
            b' ' => width += 1,
            b'\t' => width += 2,
            _ => return (width, position),
        }
        position += 1;
    }
    (width, position)
}

fn scan_plain(source: &[u8], position: usize, flow_depth: usize) -> usize {
    if flow_depth == 0 {
        return scan_block_plain(source, position);
    }
    let mut end = position;
    while end < source.len() {
        let value = source[end];
        if value == b'\r' || value == b'\n' {
            break;
        }
        if value == b':' && colon_is_indicator(source, end, flow_depth) {
            break;
        }
        if value == b'#' && (end == position || source[end - 1] == b' ' || source[end - 1] == b'\t')
        {
            break;
        }
        if matches!(value, b',' | b'[' | b']' | b'{' | b'}') {
            return end;
        }
        end += 1;
    }
    end
}

fn scan_block_plain(source: &[u8], position: usize) -> usize {
    let mut end = position;
    while end < source.len() {
        match source[end..]
            .iter()
            .position(|byte| matches!(byte, b'\r' | b'\n' | b':' | b'#'))
        {
            None => return source.len(),
            Some(offset) => end += offset,
        }
        match source[end] {
            b'\r' | b'\n' => return end,
            b':' if colon_is_indicator(source, end, 0) => return end,
            b'#' if end == position || source[end - 1] == b' ' || source[end - 1] == b'\t' => {
                return end;
            }
            _ => {}
        }
        end += 1;
    }
    source.len()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn texts_of(source: &str, kind: Kind) -> Vec<&str> {
        lex(source)
            .into_iter()
            .filter(|token| token.kind == kind)
            .map(|token| token.text)
            .collect()
    }

    #[test]
    fn lossless_and_recognizes_structure() {
        let source = "services:\n  app.service:\n    class: 'App\\\\Service'\n    tags: [{ name: kernel.event }]\n";
        let tokens = lex(source);
        assert_eq!(
            tokens.iter().map(|token| token.text).collect::<String>(),
            source
        );
        for kind in [TK_INDENT, TK_OPEN_BRACE, TK_SINGLE_QUOTED_SCALAR] {
            assert!(
                tokens.iter().any(|token| token.kind == kind),
                "missing {kind:?}"
            );
        }
    }

    #[test]
    fn route_path_keeps_braces_in_plain_scalar() {
        assert_eq!(
            texts_of("path: /product/{id}\n", TK_PLAIN_SCALAR),
            ["path", "/product/{id}"]
        );
    }

    #[test]
    fn block_scalar_is_single_token() {
        let source = "description: |\n  first\n  second\nnext: value\n";
        assert_eq!(
            texts_of(source, TK_BLOCK_SCALAR),
            ["|\n  first\n  second\n"]
        );
    }

    #[test]
    fn multibyte_input_never_splits_characters() {
        for source in ["k: \"\\ä\"", "ä: ö\n", "[\u{1F600}]", "'é", "#ü\n"] {
            let tokens = lex(source);
            assert_eq!(
                tokens.iter().map(|token| token.text).collect::<String>(),
                source
            );
        }
    }
}
