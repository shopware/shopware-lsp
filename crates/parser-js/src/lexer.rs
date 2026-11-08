//! Lossless JavaScript/TypeScript lexer.
//!
//! Non-ASCII bytes are identifier characters, so every token boundary falls
//! on an ASCII byte (or the end of input) and never splits a UTF-8 character.

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
    let at = |offset: usize| source.get(position + offset).copied();
    match source[position] {
        b' ' | b'\t' | 0x0C => {
            let length = source[position..]
                .iter()
                .take_while(|byte| matches!(byte, b' ' | b'\t' | 0x0C))
                .count();
            (TK_WHITESPACE, length)
        }
        b'\r' if at(1) == Some(b'\n') => (TK_LINE_BREAK, 2),
        b'\r' | b'\n' => (TK_LINE_BREAK, 1),
        b'/' if at(1) == Some(b'/') => (TK_LINE_COMMENT, scan_line_comment(source, position)),
        b'/' if at(1) == Some(b'*') => (TK_BLOCK_COMMENT, scan_block_comment(source, position)),
        b'/' => (TK_OPERATOR, scan_operator(source, position)),
        quote @ (b'\'' | b'"') => (TK_STRING, scan_quoted(source, position, quote)),
        b'`' => (TK_TEMPLATE, scan_quoted(source, position, b'`')),
        b'(' => (TK_OPEN_PAREN, 1),
        b')' => (TK_CLOSE_PAREN, 1),
        b'{' => (TK_OPEN_BRACE, 1),
        b'}' => (TK_CLOSE_BRACE, 1),
        b'[' => (TK_OPEN_BRACKET, 1),
        b']' => (TK_CLOSE_BRACKET, 1),
        b'.' if at(1) == Some(b'.') && at(2) == Some(b'.') => (TK_OPERATOR, 3),
        b'.' => (TK_DOT, 1),
        b',' => (TK_COMMA, 1),
        b':' => (TK_COLON, 1),
        b';' => (TK_SEMICOLON, 1),
        b'?' if at(1) == Some(b'.') => (TK_OPTIONAL_CHAIN, 2),
        b'?' if at(1) == Some(b'?') && at(2) == Some(b'=') => (TK_OPERATOR, 3),
        b'?' if at(1) == Some(b'?') => (TK_OPERATOR, 2),
        b'?' => (TK_QUESTION, 1),
        b'=' if at(1) == Some(b'>') => (TK_ARROW, 2),
        b'=' => (TK_OPERATOR, scan_operator(source, position)),
        byte if is_identifier_start(byte) => {
            let end = scan_identifier(source, position);
            let kind = if is_keyword(&source[position..end]) {
                TK_KEYWORD
            } else {
                TK_IDENTIFIER
            };
            (kind, end - position)
        }
        byte if byte.is_ascii_digit() => (TK_NUMBER, scan_number(source, position) - position),
        byte if is_operator_byte(byte) => (TK_OPERATOR, scan_operator(source, position)),
        _ => (TK_UNKNOWN, 1),
    }
}

fn index_any2(source: &[u8], start: usize, first: u8, second: u8) -> usize {
    source
        .get(start..)
        .and_then(|rest| {
            rest.iter()
                .position(|byte| *byte == first || *byte == second)
        })
        .map_or(source.len(), |offset| start + offset)
}

fn scan_line_comment(source: &[u8], position: usize) -> usize {
    index_any2(source, position + 2, b'\r', b'\n') - position
}

fn scan_block_comment(source: &[u8], position: usize) -> usize {
    let mut end = position + 2;
    while end + 1 < source.len() {
        end = index_any2(source, end, b'*', b'*');
        if end + 1 < source.len() && source[end + 1] == b'/' {
            return end - position + 2;
        }
        end += 1;
    }
    source.len() - position
}

fn scan_quoted(source: &[u8], position: usize, quote: u8) -> usize {
    let mut end = position + 1;
    while end < source.len() {
        end = index_any2(source, end, quote, b'\\');
        if end >= source.len() {
            break;
        }
        if source[end] == quote {
            return end - position + 1;
        }
        end += 2;
    }
    source.len() - position
}

fn scan_identifier(source: &[u8], position: usize) -> usize {
    let mut end = position + 1;
    while end < source.len() && is_identifier_continue(source[end]) {
        end += 1;
    }
    end
}

fn scan_number(source: &[u8], position: usize) -> usize {
    let mut end = position + 1;
    while end < source.len() && (is_identifier_continue(source[end]) || source[end] == b'.') {
        end += 1;
    }
    end
}

fn scan_operator(source: &[u8], position: usize) -> usize {
    let mut end = position + 1;
    while end < source.len() && end - position < 4 && is_operator_byte(source[end]) {
        if source[end] == b'/' {
            break;
        }
        end += 1;
    }
    end - position
}

fn is_identifier_start(value: u8) -> bool {
    value == b'_' || value == b'$' || value.is_ascii_alphabetic() || value >= 0x80
}

fn is_identifier_continue(value: u8) -> bool {
    is_identifier_start(value) || value.is_ascii_digit()
}

fn is_operator_byte(value: u8) -> bool {
    matches!(
        value,
        b'+' | b'-' | b'*' | b'%' | b'=' | b'!' | b'<' | b'>' | b'&' | b'|' | b'^' | b'~' | b'/'
    )
}

fn is_keyword(text: &[u8]) -> bool {
    matches!(
        text,
        b"as"
            | b"async"
            | b"await"
            | b"break"
            | b"case"
            | b"catch"
            | b"class"
            | b"const"
            | b"continue"
            | b"debugger"
            | b"default"
            | b"delete"
            | b"do"
            | b"else"
            | b"export"
            | b"extends"
            | b"false"
            | b"finally"
            | b"for"
            | b"from"
            | b"function"
            | b"get"
            | b"if"
            | b"import"
            | b"in"
            | b"instanceof"
            | b"let"
            | b"new"
            | b"null"
            | b"of"
            | b"return"
            | b"satisfies"
            | b"set"
            | b"static"
            | b"super"
            | b"switch"
            | b"this"
            | b"throw"
            | b"true"
            | b"try"
            | b"typeof"
            | b"undefined"
            | b"var"
            | b"void"
            | b"while"
            | b"with"
            | b"yield"
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lossless_javascript() {
        let source = "import template from './x.html.twig';\nexport default { props: { title: String }, load: () => import('./x') }; // end";
        let tokens = lex(source);
        let rebuilt: String = tokens.iter().map(|token| token.text).collect();
        assert_eq!(rebuilt, source);
        let kinds: Vec<Kind> = tokens.iter().map(|token| token.kind).collect();
        assert!(kinds.contains(&TK_STRING));
        assert!(kinds.contains(&TK_ARROW));
        assert!(kinds.contains(&TK_LINE_COMMENT));
    }

    #[test]
    fn incomplete_string() {
        let tokens = lex("Component.extend('child', 'sw-parent");
        let last = tokens.last().unwrap();
        assert_eq!(last.kind, TK_STRING);
        assert_eq!(last.text, "'sw-parent");
    }

    #[test]
    fn non_ascii_stays_on_character_boundaries() {
        for source in ["const ä = 'ö\\", "/* ü", "x\\ü", "1ü.2", "'\\ü'"] {
            let rebuilt: String = lex(source).iter().map(|token| token.text).collect();
            assert_eq!(rebuilt, source);
        }
    }
}
