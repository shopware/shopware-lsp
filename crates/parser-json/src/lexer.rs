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
    let run = |predicate: fn(u8) -> bool| {
        source[position + 1..]
            .iter()
            .take_while(|byte| predicate(**byte))
            .count()
            + 1
    };
    match source[position] {
        b' ' | b'\t' => (TK_WHITESPACE, run(|b| b == b' ' || b == b'\t')),
        b'\r' | b'\n' => (TK_LINE_BREAK, run(|b| b == b'\r' || b == b'\n')),
        b'{' => (TK_OPEN_BRACE, 1),
        b'}' => (TK_CLOSE_BRACE, 1),
        b'[' => (TK_OPEN_BRACKET, 1),
        b']' => (TK_CLOSE_BRACKET, 1),
        b':' => (TK_COLON, 1),
        b',' => (TK_COMMA, 1),
        b'"' => (TK_STRING, scan_string(source, position)),
        b'-' if source.get(position + 1).is_some_and(u8::is_ascii_digit) => {
            (TK_NUMBER, scan_number(source, position))
        }
        b'-' => (TK_UNKNOWN, 1),
        byte if byte.is_ascii_digit() => (TK_NUMBER, scan_number(source, position)),
        byte if byte.is_ascii_alphabetic() => {
            let length = run(|b| b.is_ascii_alphabetic());
            let kind = match &source[position..position + length] {
                b"true" => TK_TRUE,
                b"false" => TK_FALSE,
                b"null" => TK_NULL,
                _ => TK_UNKNOWN,
            };
            (kind, length)
        }
        _ => (TK_UNKNOWN, utf8_len(source[position])),
    }
}

/// Go lexes one byte for unknown input; Rust tokens must stay on character
/// boundaries, so an unknown non-ASCII character is consumed whole.
fn utf8_len(first: u8) -> usize {
    match first {
        0xF0..=0xF7 => 4,
        0xE0..=0xEF => 3,
        0xC0..=0xDF => 2,
        _ => 1,
    }
}

fn scan_string(source: &[u8], position: usize) -> usize {
    let mut end = position + 1;
    while end < source.len() {
        match source[end] {
            b'"' => return end - position + 1,
            b'\\' => end += if end + 1 < source.len() { 2 } else { 1 },
            b'\r' | b'\n' => return end - position,
            _ => end += 1,
        }
    }
    // An escape may have jumped into the middle of a multi-byte character.
    source.len() - position
}

fn scan_number(source: &[u8], position: usize) -> usize {
    let digit = |index: usize| source.get(index).is_some_and(u8::is_ascii_digit);
    let mut end = position;
    if source[end] == b'-' {
        end += 1;
    }
    if source[end] == b'0' {
        end += 1;
    } else {
        while digit(end) {
            end += 1;
        }
    }
    if source.get(end) == Some(&b'.') && digit(end + 1) {
        end += 2;
        while digit(end) {
            end += 1;
        }
    }
    if matches!(source.get(end), Some(b'e' | b'E')) {
        let mut exponent = end + 1;
        if matches!(source.get(exponent), Some(b'+' | b'-')) {
            exponent += 1;
        }
        if digit(exponent) {
            end = exponent + 1;
            while digit(end) {
                end += 1;
            }
        }
    }
    end - position
}
