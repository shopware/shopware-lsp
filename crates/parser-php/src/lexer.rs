//! Allocation-free PHP lexer (port of Go `internal/parser/php/lexer`).
//!
//! The lexer runs in PHP mode from the first byte; inline HTML is lexed as
//! ordinary PHP tokens exactly like the Go implementation.

use swlsp_syntax::{Kind, Token};

use crate::syntax::*;

/// Tokenizes `source` into a fresh token vector.
pub fn lex(source: &str) -> Vec<Token<'_>> {
    let mut tokens = Vec::new();
    lex_into(source, &mut tokens);
    tokens
}

/// Tokenizes `source` into `tokens`, reusing its allocation.
pub fn lex_into<'a>(source: &'a str, tokens: &mut Vec<Token<'a>>) {
    tokens.clear();
    // Production PHP averages about one token per five source bytes.
    tokens.reserve(source.len() / 4 + 1);
    let bytes = source.as_bytes();
    let mut position = 0;
    while position < bytes.len() {
        let (kind, length) = next(bytes, position);
        let end = position + length.max(1);
        tokens.push(Token::new(kind, source, position as u32, end as u32));
        position = end;
    }
}

/// Returns the kind and byte length of the token starting at `position`.
pub(crate) fn next(source: &[u8], position: usize) -> (Kind, usize) {
    let rest = &source[position..];
    let first = rest[0];
    match first {
        b'<' => {
            if rest.starts_with(b"<?php") {
                return (TK_OPEN_TAG, 5);
            }
            if rest.starts_with(b"<?=") {
                return (TK_OPEN_TAG, 3);
            }
            if rest.starts_with(b"<?") {
                return (TK_OPEN_TAG, 2);
            }
            if rest.starts_with(b"<<<") {
                return (TK_STRING, scan_heredoc(source, position));
            }
        }
        b'?' => {
            if rest.starts_with(b"?>") {
                return (TK_CLOSE_TAG, 2);
            }
            if rest.starts_with(b"?->") {
                return (TK_NULLSAFE_OBJECT_OPERATOR, 3);
            }
        }
        b'-' if rest.starts_with(b"->") => return (TK_OBJECT_OPERATOR, 2),
        b':' if rest.starts_with(b"::") => return (TK_SCOPE_RESOLUTION, 2),
        b'#' if rest.starts_with(b"#[") => return (TK_ATTRIBUTE_OPEN, 2),
        b'.' if rest.starts_with(b"...") => return (TK_ELLIPSIS, 3),
        b'=' if rest.starts_with(b"=>") => return (TK_ARROW, 2),
        _ => {}
    }
    let compound = compound_operator_length(rest);
    if compound != 0 {
        return (TK_OPERATOR, compound);
    }

    match first {
        b' ' | b'\t' | b'\x0c' => {
            let length = rest
                .iter()
                .take_while(|b| matches!(b, b' ' | b'\t' | b'\x0c'))
                .count();
            (TK_WHITESPACE, length)
        }
        b'\r' => (
            TK_LINE_BREAK,
            if rest.get(1) == Some(&b'\n') { 2 } else { 1 },
        ),
        b'\n' => (TK_LINE_BREAK, 1),
        b'/' => match rest.get(1) {
            Some(b'/') => (TK_LINE_COMMENT, scan_line_comment(source, position, 2)),
            Some(b'*') => (TK_BLOCK_COMMENT, scan_block_comment(source, position)),
            _ => (TK_OPERATOR, scan_operator(source, position)),
        },
        b'#' => (TK_LINE_COMMENT, scan_line_comment(source, position, 1)),
        b'\'' | b'"' | b'`' => (TK_STRING, scan_quoted(source, position, first)),
        b'$' => (
            TK_VARIABLE,
            scan_identifier_tail(source, position + 1) - position,
        ),
        b'\\' => (TK_BACKSLASH, 1),
        b'(' => (TK_OPEN_PAREN, 1),
        b')' => (TK_CLOSE_PAREN, 1),
        b'{' => (TK_OPEN_BRACE, 1),
        b'}' => (TK_CLOSE_BRACE, 1),
        b'[' => (TK_OPEN_BRACKET, 1),
        b']' => (TK_CLOSE_BRACKET, 1),
        b',' => (TK_COMMA, 1),
        b':' => (TK_COLON, 1),
        b';' => (TK_SEMICOLON, 1),
        b'=' => (TK_EQUALS, 1),
        b'?' => (TK_QUESTION, 1),
        b'|' => (TK_PIPE, 1),
        b'&' => (TK_AMPERSAND, 1),
        _ if is_identifier_start(first) => {
            let end = scan_identifier_tail(source, position + 1);
            let kind = if is_keyword(&source[position..end]) {
                TK_KEYWORD
            } else {
                TK_IDENTIFIER
            };
            (kind, end - position)
        }
        _ if first.is_ascii_digit() => (TK_NUMBER, scan_number(source, position) - position),
        _ if is_operator_byte(first) => (TK_OPERATOR, scan_operator(source, position)),
        _ => (TK_UNKNOWN, 1),
    }
}

#[inline]
fn index_any2(source: &[u8], start: usize, first: u8, second: u8) -> usize {
    source[start.min(source.len())..]
        .iter()
        .position(|&b| b == first || b == second)
        .map_or(source.len(), |offset| start + offset)
}

fn scan_line_comment(source: &[u8], position: usize, prefix: usize) -> usize {
    index_any2(source, position + prefix, b'\r', b'\n') - position
}

fn scan_block_comment(source: &[u8], position: usize) -> usize {
    let mut end = position + 2;
    while end + 1 < source.len() {
        end = source[end..]
            .iter()
            .position(|&b| b == b'*')
            .map_or(source.len(), |offset| end + offset);
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

/// Trims ASCII and Unicode white space like Go `strings.TrimSpace`.
fn trim_space(bytes: &[u8]) -> &[u8] {
    match std::str::from_utf8(bytes) {
        Ok(text) => text.trim().as_bytes(),
        Err(_) => bytes.trim_ascii(),
    }
}

fn scan_heredoc(source: &[u8], position: usize) -> usize {
    let header_end = index_any2(source, position, b'\r', b'\n');
    let mut header = trim_space(&source[position + 3..header_end]);
    if header.len() >= 2
        && (header[0] == b'\'' || header[0] == b'"')
        && header[header.len() - 1] == header[0]
    {
        header = &header[1..header.len() - 1];
    }
    if header.is_empty() {
        return 3;
    }
    let skip_line_break = |mut index: usize| {
        if index < source.len() && source[index] == b'\r' {
            index += 1;
        }
        if index < source.len() && source[index] == b'\n' {
            index += 1;
        }
        index
    };
    let mut line_start = skip_line_break(header_end);
    while line_start < source.len() {
        let line_end = index_any2(source, line_start, b'\r', b'\n');
        let line = &source[line_start..line_end];
        let indent = line
            .iter()
            .take_while(|b| matches!(b, b' ' | b'\t'))
            .count();
        let trimmed = &line[indent..];
        if let Some(after) = trimmed.strip_prefix(header)
            && (after.is_empty()
                || (after[0] != b' ' && after[0] != b'\t' && !is_identifier_continue(after[0])))
        {
            return line_start + indent + header.len() - position;
        }
        line_start = skip_line_break(line_end);
    }
    source.len() - position
}

#[inline]
fn scan_identifier_tail(source: &[u8], mut position: usize) -> usize {
    while position < source.len() && is_identifier_continue(source[position]) {
        position += 1;
    }
    position
}

fn scan_number(source: &[u8], mut position: usize) -> usize {
    position += 1;
    while position < source.len()
        && (is_identifier_continue(source[position]) || source[position] == b'.')
    {
        position += 1;
    }
    position
}

pub(crate) fn compound_operator_length(source: &[u8]) -> usize {
    if source.len() >= 3
        && matches!(
            &source[..3],
            b"??=" | b"===" | b"!==" | b"<=>" | b"**=" | b"<<=" | b">>="
        )
    {
        return 3;
    }
    if source.len() >= 2
        && matches!(
            &source[..2],
            b"??"
                | b"=="
                | b"!="
                | b"<="
                | b">="
                | b"++"
                | b"--"
                | b"**"
                | b"<<"
                | b">>"
                | b"&&"
                | b"||"
                | b"+="
                | b"-="
                | b"*="
                | b"/="
                | b".="
                | b"%="
                | b"&="
                | b"|="
                | b"^="
        )
    {
        return 2;
    }
    0
}

fn scan_operator(source: &[u8], position: usize) -> usize {
    let mut end = position + 1;
    while end < source.len() && end - position < 3 && is_operator_byte(source[end]) {
        end += 1;
    }
    end - position
}

#[inline]
fn is_identifier_start(value: u8) -> bool {
    value == b'_' || value.is_ascii_alphabetic() || value >= 0x80
}

#[inline]
pub(crate) fn is_identifier_continue(value: u8) -> bool {
    is_identifier_start(value) || value.is_ascii_digit()
}

#[inline]
fn is_operator_byte(value: u8) -> bool {
    matches!(
        value,
        b'+' | b'-' | b'*' | b'%' | b'!' | b'<' | b'>' | b'^' | b'~' | b'/' | b'.' | b'@'
    )
}

/// Reports whether `text` is a (case-insensitive) PHP keyword.
pub fn is_keyword(text: &[u8]) -> bool {
    if text.len() < 2 || text.len() > 12 {
        return false;
    }
    let mut buffer = [0u8; 12];
    for (slot, &byte) in buffer.iter_mut().zip(text) {
        if !(byte.is_ascii_alphabetic() || byte == b'_') {
            return false;
        }
        *slot = byte.to_ascii_lowercase();
    }
    matches!(
        &buffer[..text.len()],
        b"abstract"
            | b"and"
            | b"array"
            | b"as"
            | b"break"
            | b"callable"
            | b"case"
            | b"catch"
            | b"class"
            | b"clone"
            | b"const"
            | b"continue"
            | b"declare"
            | b"default"
            | b"do"
            | b"echo"
            | b"else"
            | b"elseif"
            | b"empty"
            | b"enddeclare"
            | b"endfor"
            | b"endforeach"
            | b"endif"
            | b"endswitch"
            | b"endwhile"
            | b"enum"
            | b"eval"
            | b"exit"
            | b"extends"
            | b"false"
            | b"final"
            | b"finally"
            | b"fn"
            | b"for"
            | b"foreach"
            | b"from"
            | b"function"
            | b"global"
            | b"goto"
            | b"if"
            | b"implements"
            | b"include"
            | b"include_once"
            | b"instanceof"
            | b"insteadof"
            | b"interface"
            | b"isset"
            | b"list"
            | b"match"
            | b"namespace"
            | b"new"
            | b"null"
            | b"or"
            | b"parent"
            | b"print"
            | b"private"
            | b"protected"
            | b"public"
            | b"readonly"
            | b"require"
            | b"require_once"
            | b"return"
            | b"self"
            | b"static"
            | b"switch"
            | b"throw"
            | b"trait"
            | b"true"
            | b"try"
            | b"unset"
            | b"use"
            | b"var"
            | b"while"
            | b"xor"
            | b"yield"
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn joined(tokens: &[Token<'_>]) -> String {
        tokens.iter().map(|token| token.text).collect()
    }

    #[test]
    fn keyword_recognition_is_case_insensitive() {
        let keywords = "abstract and array as break callable case catch class clone const \
            continue declare default do echo else elseif empty enddeclare \
            endfor endforeach endif endswitch endwhile enum eval exit extends \
            false final finally fn for foreach from function global goto if \
            implements include include_once instanceof insteadof interface \
            isset list match namespace new null or parent print private \
            protected public readonly require require_once return self static \
            switch throw trait true try unset use var while xor yield";
        for keyword in keywords.split_whitespace() {
            assert!(is_keyword(keyword.as_bytes()), "{keyword}");
            assert!(is_keyword(keyword.to_uppercase().as_bytes()), "{keyword}");
        }
        for name in ["Product", "App_Service", "yielded", "über"] {
            assert!(!is_keyword(name.as_bytes()), "{name}");
        }
    }

    #[test]
    fn lex_is_lossless() {
        let source = "<?php\n#[Route(path: '/x')]\nfinal class C { public function f(?Foo $x = null): string { return $this->trans('key'); } }\n";
        let tokens = lex(source);
        assert_eq!(joined(&tokens), source);
        for kind in [
            TK_OPEN_TAG,
            TK_ATTRIBUTE_OPEN,
            TK_VARIABLE,
            TK_OBJECT_OPERATOR,
            TK_STRING,
        ] {
            assert!(
                tokens.iter().any(|token| token.kind == kind),
                "missing {kind:?}"
            );
        }
    }

    #[test]
    fn compound_operator_recognition() {
        for operator in [
            "??=", "===", "!==", "<=>", "**=", "<<=", ">>=", "??", "==", "!=", "<=", ">=", "++",
            "--", "**", "<<", ">>", "&&", "||", "+=", "-=", "*=", "/=", ".=", "%=", "&=", "|=",
            "^=",
        ] {
            assert_eq!(
                next(operator.as_bytes(), 0),
                (TK_OPERATOR, operator.len()),
                "{operator}"
            );
        }
    }

    #[test]
    fn lex_into_reuses_destination() {
        let mut tokens = Vec::with_capacity(128);
        let data = tokens.as_ptr();
        lex_into("<?php $value = 1;", &mut tokens);
        assert_eq!(tokens.as_ptr(), data);
        assert!(!tokens.is_empty());
    }

    #[test]
    fn heredoc_and_nowdoc_are_strings() {
        let source =
            "<?php\n$a = <<<TEXT\nhello $name\nTEXT;\n$b = <<<'RAW'\nliteral $name\nRAW;\n";
        let tokens = lex(source);
        assert_eq!(joined(&tokens), source);
        assert_eq!(
            tokens
                .iter()
                .filter(|token| token.kind == TK_STRING)
                .count(),
            2
        );
    }

    #[test]
    fn indented_heredoc_before_call_closing_tokens() {
        let source = "<?php\n$statement = $connection->prepare(<<<'SQL'\n    SELECT *\n    FROM product\n    SQL);\n$after = true;\n";
        let tokens = lex(source);
        assert_eq!(joined(&tokens), source);
        let heredoc = tokens
            .iter()
            .find(|token| token.text.starts_with("<<<"))
            .unwrap();
        assert!(heredoc.text.ends_with("    SQL"), "{:?}", heredoc.text);
    }

    #[test]
    fn non_ascii_never_splits_characters() {
        let source = "<?php $über = 'ä\\ö'; // ü\n/* é */ \"x\\é";
        let tokens = lex(source);
        assert_eq!(joined(&tokens), source);
    }
}
