//! Allocation-free PHP lexer.
//!
//! The lexer runs in PHP mode from the first byte, so a fragment without an
//! opening tag is PHP. Markup outside PHP tags is one trivia token: what
//! follows a closing tag up to the next opening tag, and what a file that
//! starts with markup (or an interpreter line) has before its first tag.

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
    let mut position = leading_markup(bytes);
    if position > 0 {
        tokens.push(Token::new(TK_INLINE_HTML, source, 0, position as u32));
    }
    while position < bytes.len() {
        let (kind, length) = next(bytes, position);
        let end = position + length.max(1);
        tokens.push(Token::new(kind, source, position as u32, end as u32));
        position = end;
        if kind == TK_CLOSE_TAG {
            let markup = next_open_tag(bytes, position);
            if markup > position {
                tokens.push(Token::new(
                    TK_INLINE_HTML,
                    source,
                    position as u32,
                    markup as u32,
                ));
                position = markup;
            }
        }
    }
}

/// The position of the next opening tag at or after `position`, or the end
/// of the source.
fn next_open_tag(source: &[u8], position: usize) -> usize {
    source[position..]
        .windows(2)
        .position(|pair| pair == b"<?")
        .map_or(source.len(), |found| position + found)
}

/// The length of the markup a file starts with: it begins like markup
/// (`<html`, `<!--`) or with an interpreter line, and an opening tag
/// follows. Anything else is PHP from its first byte.
fn leading_markup(source: &[u8]) -> usize {
    let start = source
        .iter()
        .position(|byte| !byte.is_ascii_whitespace())
        .unwrap_or(source.len());
    let rest = &source[start..];
    let markup = rest.starts_with(b"#!")
        || rest.first() == Some(&b'<')
            && rest
                .get(1)
                .is_some_and(|byte| byte.is_ascii_alphabetic() || matches!(byte, b'!' | b'/'));
    let open = next_open_tag(source, start);
    if markup && open < source.len() {
        open
    } else {
        0
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
        // A number may start with its decimal point: `.5`.
        b'.' if rest.get(1).is_some_and(u8::is_ascii_digit) => {
            return (TK_NUMBER, scan_number(source, position) - position);
        }
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
            // An operator that is not a compound one is one character:
            // `!!$a` is two operators, `'a'.++$i` a concatenation and an
            // increment.
            _ => (TK_OPERATOR, 1),
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
        _ if is_operator_byte(first) => (TK_OPERATOR, 1),
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

/// Trims Unicode white space, or only ASCII white space when `bytes` is not
/// valid UTF-8.
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
            && (after.is_empty() || !is_identifier_continue(after[0]))
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
    let start = position;
    position += 1;
    loop {
        while position < source.len()
            && (is_identifier_continue(source[position]) || source[position] == b'.')
        {
            position += 1;
        }
        // The sign of an exponent belongs to the number: `1e-10`. A
        // hexadecimal number has no exponent, its `e` is a digit.
        let exponent = matches!(source[position - 1], b'e' | b'E')
            && !source[start..position].starts_with(b"0x")
            && !source[start..position].starts_with(b"0X")
            && matches!(source.get(position), Some(b'+' | b'-'))
            && source.get(position + 1).is_some_and(u8::is_ascii_digit);
        if !exponent {
            return position;
        }
        position += 1;
    }
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
                | b"<>"
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
    fn markup_outside_php_tags_is_one_token() {
        let kinds = |source: &str| -> Vec<(Kind, String)> {
            lex(source)
                .iter()
                .filter(|token| token.kind != TK_WHITESPACE && token.kind != TK_LINE_BREAK)
                .map(|token| (token.kind, token.text.to_string()))
                .collect()
        };
        assert_eq!(
            kinds("<b><?= $a ?></b>\n<?php if ($b): ?>\n  <i>x</i>\n<?php endif;"),
            [
                (TK_INLINE_HTML, "<b>".to_owned()),
                (TK_OPEN_TAG, "<?=".to_owned()),
                (TK_VARIABLE, "$a".to_owned()),
                (TK_CLOSE_TAG, "?>".to_owned()),
                (TK_INLINE_HTML, "</b>\n".to_owned()),
                (TK_OPEN_TAG, "<?php".to_owned()),
                (TK_KEYWORD, "if".to_owned()),
                (TK_OPEN_PAREN, "(".to_owned()),
                (TK_VARIABLE, "$b".to_owned()),
                (TK_CLOSE_PAREN, ")".to_owned()),
                (TK_COLON, ":".to_owned()),
                (TK_CLOSE_TAG, "?>".to_owned()),
                (TK_INLINE_HTML, "\n  <i>x</i>\n".to_owned()),
                (TK_OPEN_TAG, "<?php".to_owned()),
                (TK_KEYWORD, "endif".to_owned()),
                (TK_SEMICOLON, ";".to_owned()),
            ]
        );
        // A fragment without tags is PHP, and so is a comparison.
        assert_eq!(kinds("$a < $b")[1].0, TK_OPERATOR);
        assert_eq!(
            kinds("#!/usr/bin/env php\n<?php echo 1;")[0].0,
            TK_INLINE_HTML
        );
    }

    #[test]
    fn operators_next_to_each_other_stay_apart() {
        let texts = |source: &str| -> Vec<String> {
            lex(source)
                .iter()
                .map(|token| token.text.to_string())
                .filter(|text| !text.trim().is_empty())
                .collect()
        };
        assert_eq!(
            texts("<?php !!$a; 'b'.++$c; 1+-2; $d<>$e;"),
            [
                "<?php", "!", "!", "$a", ";", "'b'", ".", "++", "$c", ";", "1", "+", "-", "2", ";",
                "$d", "<>", "$e", ";"
            ]
        );
    }

    #[test]
    fn exponent_sign_belongs_to_the_number() {
        let texts = |source: &str| -> Vec<String> {
            lex(source)
                .iter()
                .map(|token| token.text.to_string())
                .filter(|text| !text.trim().is_empty())
                .collect()
        };
        assert_eq!(
            texts("<?php 1e-10 + 2.5E+3 - 0x1e-1 * .5 . 'a';"),
            [
                "<?php", "1e-10", "+", "2.5E+3", "-", "0x1e", "-", "1", "*", ".5", ".", "'a'", ";"
            ]
        );
    }

    #[test]
    fn heredoc_ends_at_its_marker_before_a_space() {
        // Since PHP 7.3 the closing marker ends the heredoc wherever a line
        // starts with it, whatever follows.
        let source = "<?php\n$a = <<<EOT\n  text\n  EOT . 'tail';\n";
        let tokens = lex(source);
        let heredoc = tokens
            .iter()
            .find(|token| token.text.starts_with("<<<"))
            .expect("heredoc token");
        assert_eq!(heredoc.text, "<<<EOT\n  text\n  EOT");
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
