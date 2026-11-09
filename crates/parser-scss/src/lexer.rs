//! SCSS lexer.

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
    let at = |index: usize| source.get(index).copied();
    match source[position] {
        b' ' | b'\t' | 0x0C => {
            let length = source[position..]
                .iter()
                .take_while(|byte| matches!(byte, b' ' | b'\t' | 0x0C))
                .count();
            (TK_WHITESPACE, length)
        }
        b'\r' if at(position + 1) == Some(b'\n') => (TK_LINE_BREAK, 2),
        b'\r' | b'\n' => (TK_LINE_BREAK, 1),
        b'/' if at(position + 1) == Some(b'*') => {
            (TK_BLOCK_COMMENT, scan_block_comment(source, position))
        }
        b'/' if at(position + 1) == Some(b'/') && is_line_comment_start(source, position) => {
            (TK_LINE_COMMENT, scan_line_comment(source, position))
        }
        b'/' => (TK_OPERATOR, 1),
        b'{' => (TK_OPEN_BRACE, 1),
        b'}' => (TK_CLOSE_BRACE, 1),
        b'(' => (TK_OPEN_PAREN, 1),
        b')' => (TK_CLOSE_PAREN, 1),
        b'[' => (TK_OPEN_BRACKET, 1),
        b']' => (TK_CLOSE_BRACKET, 1),
        b':' => (TK_COLON, 1),
        b';' => (TK_SEMICOLON, 1),
        b',' => (TK_COMMA, 1),
        b'\'' => (
            TK_SINGLE_QUOTED_STRING,
            scan_quoted(source, position, b'\''),
        ),
        b'"' => (TK_DOUBLE_QUOTED_STRING, scan_quoted(source, position, b'"')),
        b'#' if at(position + 1) == Some(b'{') => (TK_INTERPOLATION_OPEN, 2),
        b'#' => (TK_HASH, 1),
        b'$' if at(position + 1).is_some_and(is_name_start) => {
            (TK_VARIABLE, scan_name(source, position + 1) - position)
        }
        b'@' if at(position + 1).is_some_and(is_name_start) => {
            (TK_AT_KEYWORD, scan_name(source, position + 1) - position)
        }
        b'$' | b'@' => (TK_OPERATOR, 1),
        first @ (b'+' | b'-' | b'*' | b'%' | b'=' | b'!' | b'<' | b'>' | b'&' | b'|' | b'~'
        | b'^') => {
            if first == b'-'
                && (at(position + 1).is_some_and(is_name_start)
                    || (at(position + 1) == Some(b'-')
                        && at(position + 2).is_some_and(is_name_start)))
            {
                return (TK_IDENTIFIER, scan_name(source, position) - position);
            }
            match at(position + 1) {
                Some(second) if is_operator_pair(first, second) => (TK_OPERATOR, 2),
                _ => (TK_OPERATOR, 1),
            }
        }
        byte if byte.is_ascii_digit()
            || (byte == b'.' && at(position + 1).is_some_and(|b| b.is_ascii_digit())) =>
        {
            (TK_NUMBER, scan_number(source, position) - position)
        }
        byte if is_name_start(byte) || byte == b'\\' => {
            // A trailing lone backslash scans as an empty name; consume the
            // byte so the lexer always advances.
            (
                TK_IDENTIFIER,
                (scan_name(source, position) - position).max(1),
            )
        }
        // Non-ASCII bytes are name starts, so unknown input is always ASCII.
        _ => (TK_UNKNOWN, 1),
    }
}

fn scan_block_comment(source: &[u8], position: usize) -> usize {
    source[position + 2..]
        .windows(2)
        .position(|pair| pair == b"*/")
        .map_or(source.len() - position, |index| index + 4)
}

fn scan_line_comment(source: &[u8], position: usize) -> usize {
    source[position + 2..]
        .iter()
        .position(|byte| matches!(byte, b'\r' | b'\n'))
        .map_or(source.len() - position, |index| index + 2)
}

fn scan_quoted(source: &[u8], position: usize, quote: u8) -> usize {
    let mut end = position + 1;
    while end < source.len() {
        match source[end] {
            byte if byte == quote => return end - position + 1,
            b'\\' => end += 2,
            _ => end += 1,
        }
    }
    // An escape may have jumped into the middle of a multi-byte character.
    source.len() - position
}

fn scan_name(source: &[u8], position: usize) -> usize {
    let mut end = position;
    while end < source.len() {
        if is_name_continue(source[end]) {
            end += 1;
        } else if source[end] == b'\\' && end + 1 < source.len() {
            // An escaped multi-byte character is completed by its
            // continuation bytes, which are name characters.
            end += 2;
        } else {
            break;
        }
    }
    end
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
    while source
        .get(end)
        .is_some_and(|byte| is_name_continue(*byte) || *byte == b'%')
    {
        end += 1;
    }
    end
}

fn is_name_start(value: u8) -> bool {
    value.is_ascii_alphabetic() || value == b'_' || value >= 0x80
}

fn is_name_continue(value: u8) -> bool {
    is_name_start(value) || value.is_ascii_digit() || value == b'-'
}

fn is_operator_pair(first: u8, second: u8) -> bool {
    match first {
        b'=' | b'!' | b'<' | b'>' => second == b'=',
        b'&' => second == b'&',
        b'|' => second == b'|',
        _ => false,
    }
}

fn is_line_comment_start(source: &[u8], position: usize) -> bool {
    position == 0
        || matches!(
            source[position - 1],
            b' ' | b'\t' | b'\r' | b'\n' | 0x0C | b';' | b'{' | b'}'
        )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lossless_and_recognizes_scss() {
        let source = "$color: #fff;\n.button-#{$variant} {\n  color: feature(\"ACCESSIBILITY_TWEAKS\");\n  // comment\n}";
        let tokens = lex(source);
        let reconstructed: String = tokens.iter().map(|token| token.text).collect();
        assert_eq!(reconstructed, source);
        for kind in [
            TK_VARIABLE,
            TK_INTERPOLATION_OPEN,
            TK_DOUBLE_QUOTED_STRING,
            TK_LINE_COMMENT,
        ] {
            assert!(
                tokens.iter().any(|token| token.kind == kind),
                "missing {kind:?}: {tokens:?}"
            );
        }
    }

    #[test]
    fn url_is_not_a_line_comment() {
        let tokens = lex("background: url(data:image/png;base64,abc//def==);");
        assert!(
            tokens.iter().all(|token| token.kind != TK_LINE_COMMENT),
            "{tokens:?}"
        );
    }

    #[test]
    fn edge_tokens() {
        let kinds = |source| {
            lex(source)
                .iter()
                .map(|token| (token.kind, token.text))
                .collect::<Vec<_>>()
        };
        assert_eq!(kinds("--gap"), [(TK_IDENTIFIER, "--gap")]);
        assert_eq!(
            kinds("-1.5e3px"),
            [(TK_OPERATOR, "-"), (TK_NUMBER, "1.5e3px")]
        );
        assert_eq!(kinds(".5%"), [(TK_NUMBER, ".5%")]);
        assert_eq!(kinds("a\\"), [(TK_IDENTIFIER, "a"), (TK_IDENTIFIER, "\\")]);
        assert_eq!(kinds("/* x"), [(TK_BLOCK_COMMENT, "/* x")]);
        assert_eq!(kinds("/**/"), [(TK_BLOCK_COMMENT, "/**/")]);
        assert_eq!(kinds("'a\\'"), [(TK_SINGLE_QUOTED_STRING, "'a\\'")]);
        assert_eq!(kinds("\r\n"), [(TK_LINE_BREAK, "\r\n")]);
        assert_eq!(kinds("é"), [(TK_IDENTIFIER, "é")]);
        assert_eq!(
            kinds("<= && @ $"),
            [
                (TK_OPERATOR, "<="),
                (TK_WHITESPACE, " "),
                (TK_OPERATOR, "&&"),
                (TK_WHITESPACE, " "),
                (TK_OPERATOR, "@"),
                (TK_WHITESPACE, " "),
                (TK_OPERATOR, "$"),
            ]
        );
    }
}
