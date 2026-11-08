//! Lossless XPath lexer.
//!
//! Every token boundary falls on an ASCII byte (non-ASCII bytes are name
//! characters), so tokens never split a UTF-8 character.

use swlsp_syntax::{Kind, Token};

use crate::syntax::*;

pub fn lex(source: &str) -> Vec<Token<'_>> {
    let bytes = source.as_bytes();
    let mut tokens = Vec::with_capacity(source.len() / 3 + 1);
    let mut position = 0;
    while position < bytes.len() {
        let (kind, length) = next(bytes, position);
        let end = position + length;
        tokens.push(Token::new(kind, source, position as u32, end as u32));
        position = end;
    }
    tokens
}

fn next(source: &[u8], position: usize) -> (Kind, usize) {
    let following = source.get(position + 1).copied();
    let pair = |second: u8, double: Kind, single: Kind| {
        if following == Some(second) {
            (double, 2)
        } else {
            (single, 1)
        }
    };
    match source[position] {
        b' ' | b'\t' | 0x0C => {
            let run = source[position + 1..]
                .iter()
                .take_while(|b| matches!(b, b' ' | b'\t' | 0x0C))
                .count();
            (TK_WHITESPACE, run + 1)
        }
        b'\r' if following == Some(b'\n') => (TK_LINE_BREAK, 2),
        b'\r' | b'\n' => (TK_LINE_BREAK, 1),
        quote @ (b'\'' | b'"') => {
            let length = memchr::memchr(quote, &source[position + 1..])
                .map_or(source.len() - position, |offset| offset + 2);
            (TK_STRING, length)
        }
        b'/' => pair(b'/', TK_DOUBLE_SLASH, TK_SLASH),
        b'.' if following == Some(b'.') => (TK_DOUBLE_DOT, 2),
        b'.' if following.is_some_and(|b| b.is_ascii_digit()) => {
            (TK_NUMBER, scan_number(source, position))
        }
        b'.' => (TK_DOT, 1),
        b':' => pair(b':', TK_DOUBLE_COLON, TK_COLON),
        b'!' => pair(b'=', TK_NOT_EQUALS, TK_UNKNOWN),
        b'<' => pair(b'=', TK_LESS_EQUALS, TK_LESS),
        b'>' => pair(b'=', TK_GREATER_EQUALS, TK_GREATER),
        b'@' => (TK_AT, 1),
        b'$' => (TK_DOLLAR, 1),
        b'|' => (TK_PIPE, 1),
        b'+' => (TK_PLUS, 1),
        b'-' => (TK_MINUS, 1),
        b'*' => (TK_STAR, 1),
        b'=' => (TK_EQUALS, 1),
        b'(' => (TK_OPEN_PAREN, 1),
        b')' => (TK_CLOSE_PAREN, 1),
        b'[' => (TK_OPEN_BRACKET, 1),
        b']' => (TK_CLOSE_BRACKET, 1),
        b',' => (TK_COMMA, 1),
        byte if byte.is_ascii_digit() => (TK_NUMBER, scan_number(source, position)),
        byte if is_name_start(byte) => {
            let run = source[position + 1..]
                .iter()
                .take_while(|b| is_name_continue(**b))
                .count();
            (TK_NAME, run + 1)
        }
        _ => (TK_UNKNOWN, 1),
    }
}

fn scan_number(source: &[u8], position: usize) -> usize {
    let digit = |index: usize| source.get(index).is_some_and(u8::is_ascii_digit);
    let mut end = position;
    if source[end] == b'.' {
        end += 1;
    }
    while digit(end) {
        end += 1;
    }
    if source.get(end) == Some(&b'.') {
        end += 1;
        while digit(end) {
            end += 1;
        }
    }
    end - position
}

fn is_name_start(value: u8) -> bool {
    value == b'_' || value.is_ascii_alphabetic() || value >= 0x80
}

fn is_name_continue(value: u8) -> bool {
    is_name_start(value) || value.is_ascii_digit() || value == b'-' || value == b'.'
}

#[cfg(test)]
mod tests {
    use super::*;

    fn lexed(source: &str) -> Vec<(Kind, &str)> {
        lex(source)
            .into_iter()
            .map(|token| (token.kind, token.text))
            .collect()
    }

    #[test]
    fn operators_numbers_and_names() {
        assert_eq!(
            lexed("//a/..::.5 1.2. != <= >= ! x-y.z ü"),
            [
                (TK_DOUBLE_SLASH, "//"),
                (TK_NAME, "a"),
                (TK_SLASH, "/"),
                (TK_DOUBLE_DOT, ".."),
                (TK_DOUBLE_COLON, "::"),
                (TK_NUMBER, ".5"),
                (TK_WHITESPACE, " "),
                (TK_NUMBER, "1.2"),
                (TK_DOT, "."),
                (TK_WHITESPACE, " "),
                (TK_NOT_EQUALS, "!="),
                (TK_WHITESPACE, " "),
                (TK_LESS_EQUALS, "<="),
                (TK_WHITESPACE, " "),
                (TK_GREATER_EQUALS, ">="),
                (TK_WHITESPACE, " "),
                (TK_UNKNOWN, "!"),
                (TK_WHITESPACE, " "),
                (TK_NAME, "x-y.z"),
                (TK_WHITESPACE, " "),
                (TK_NAME, "ü"),
            ]
        );
    }

    #[test]
    fn strings_and_line_breaks() {
        assert_eq!(
            lexed("'a\"b'\r\n\"open"),
            [
                (TK_STRING, "'a\"b'"),
                (TK_LINE_BREAK, "\r\n"),
                (TK_STRING, "\"open")
            ]
        );
        assert_eq!(
            lexed("\r\r"),
            [(TK_LINE_BREAK, "\r"), (TK_LINE_BREAK, "\r")]
        );
    }
}
