//! Lossless, error-tolerant YAML parser.

pub mod lexer;
pub mod query;
pub mod syntax;

use std::sync::Arc;

use swlsp_syntax::{Config, ErrorBuilder, Kind, Parse, Parser};

use syntax::*;

const VALUE_RECOVERY: &[Kind] = &[
    TK_LINE_BREAK,
    TK_COMMA,
    TK_CLOSE_BRACKET,
    TK_CLOSE_BRACE,
    TK_DOCUMENT_START,
    TK_DOCUMENT_END,
];
const SCALAR_TOKENS: &[Kind] = &[
    TK_PLAIN_SCALAR,
    TK_SINGLE_QUOTED_SCALAR,
    TK_DOUBLE_QUOTED_SCALAR,
    TK_BLOCK_SCALAR,
];

/// Parses one YAML stream into a lossless tree.
pub fn parse(source: &str) -> Parse {
    parse_shared(Arc::from(source))
}

/// Like [`parse`], but the tree shares `shared` instead of copying it.
pub fn parse_shared(shared: Arc<str>) -> Parse {
    let source: &str = &shared;
    let tokens = lexer::lex(source);
    let mut p = Parser::new(
        source,
        tokens,
        Config {
            general_recovery_set: &[TK_LINE_BREAK, TK_DOCUMENT_START, TK_DOCUMENT_END],
            error_kind: ERROR,
        },
    );
    let root = p.start();

    while !p.at_end() {
        skip_blank_lines(&mut p);
        while p.at(TK_DIRECTIVE) {
            p.bump();
            consume_line_end(&mut p);
            skip_blank_lines(&mut p);
        }
        if p.at_end() {
            break;
        }

        let document = p.start();
        if p.at(TK_DOCUMENT_START) {
            p.bump();
            consume_line_end(&mut p);
            skip_blank_lines(&mut p);
        }

        if !at_document_boundary(&p)
            && !{
                let indent = current_indent(&p);
                parse_block(&mut p, indent)
            }
        {
            p.add_error(ErrorBuilder::new("YAML mapping, sequence, or scalar"));
            recover_line(&mut p);
        }

        while !at_document_boundary(&p) {
            skip_blank_lines(&mut p);
            if at_document_boundary(&p) {
                break;
            }
            p.add_error(ErrorBuilder::new("end of YAML document"));
            recover_line(&mut p);
        }

        if p.at(TK_DOCUMENT_END) {
            p.bump();
            consume_line_end(&mut p);
        }
        p.complete(document, YAML_DOCUMENT);
    }

    p.complete(root, YAML_STREAM);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

/// End of input or a `---` / `...` marker.
fn at_document_boundary(p: &Parser<'_>) -> bool {
    p.at_end() || p.at(TK_DOCUMENT_START) || p.at(TK_DOCUMENT_END)
}

fn parse_block(p: &mut Parser<'_>, indent: usize) -> bool {
    if line_starts_sequence(p) {
        parse_sequence(p, indent);
    } else if line_has_top_level_colon(p) {
        parse_mapping(p, indent);
    } else {
        return parse_value_line(p);
    }
    true
}

fn parse_mapping(p: &mut Parser<'_>, indent: usize) {
    let marker = p.start();
    while !p.at_end() {
        skip_blank_lines(p);
        if current_indent(p) != indent || line_starts_sequence(p) || !line_has_top_level_colon(p) {
            break;
        }
        parse_pair_line(p, indent, true);
    }
    p.complete(marker, YAML_MAPPING);
}

fn at_collection_decorator(p: &Parser<'_>) -> bool {
    p.peek_token()
        .is_some_and(|token| token.kind == TK_PLAIN_SCALAR && is_collection_decorator(token.text))
}

fn parse_pair_line(p: &mut Parser<'_>, indent: usize, consume_leading_indent: bool) {
    let marker = p.start();
    if consume_leading_indent && p.at(TK_INDENT) {
        consume_indent(p);
    }

    if !parse_scalar(p) {
        p.add_error(ErrorBuilder::new("YAML mapping key"));
        p.recover(&[TK_COLON, TK_LINE_BREAK]);
    }
    p.expect(TK_COLON, VALUE_RECOVERY);

    let mut has_inline_value = false;
    let mut has_collection_decorator = false;
    if !at_document_boundary(p) && !p.at(TK_LINE_BREAK) {
        has_collection_decorator = at_collection_decorator(p);
        has_inline_value = parse_value(p);
        if !has_inline_value {
            p.add_error(ErrorBuilder::new("YAML value"));
            p.recover(VALUE_RECOVERY);
        }
    }

    if p.at(TK_LINE_BREAK) {
        p.bump();
    } else if !at_document_boundary(p) && !has_inline_value {
        consume_line_end(p);
    }

    if !has_inline_value || has_collection_decorator {
        skip_blank_lines(p);
        let next_indent = current_indent(p);
        if !at_document_boundary(p)
            && (next_indent > indent || next_indent == indent && line_starts_sequence(p))
        {
            if !parse_block(p, next_indent) {
                complete_null(p);
            }
        } else if !has_inline_value {
            complete_null(p);
        }
    }

    p.complete(marker, YAML_PAIR);
}

fn parse_sequence(p: &mut Parser<'_>, indent: usize) {
    let marker = p.start();
    while !p.at_end() {
        skip_blank_lines(p);
        if current_indent(p) != indent || !line_starts_sequence(p) {
            break;
        }
        parse_sequence_item(p, indent);
    }
    p.complete(marker, YAML_SEQUENCE);
}

fn parse_sequence_item(p: &mut Parser<'_>, indent: usize) {
    let marker = p.start();
    if p.at(TK_INDENT) {
        consume_indent(p);
    }
    p.expect(TK_DASH, VALUE_RECOVERY);

    if p.at(TK_LINE_BREAK) {
        p.bump();
        skip_blank_lines(p);
        let next_indent = current_indent(p);
        if !at_document_boundary(p) && next_indent > indent {
            if !parse_block(p, next_indent) {
                complete_null(p);
            }
        } else {
            complete_null(p);
        }
        p.complete(marker, YAML_SEQUENCE_ITEM);
        return;
    }

    if line_has_top_level_colon(p) {
        parse_compact_mapping(p, indent + 2);
        p.complete(marker, YAML_SEQUENCE_ITEM);
        return;
    }

    let block_scalar = p.at(TK_BLOCK_SCALAR);
    let has_collection_decorator = at_collection_decorator(p);
    if !parse_value(p) {
        p.add_error(ErrorBuilder::new("YAML sequence value"));
        p.recover(VALUE_RECOVERY);
    }

    if block_scalar {
        // The block scalar token includes its header line break and content.
    } else if p.at(TK_LINE_BREAK) {
        p.bump();
    } else if !at_document_boundary(p) {
        consume_line_end(p);
    }
    if has_collection_decorator {
        skip_blank_lines(p);
        let next_indent = current_indent(p);
        if !at_document_boundary(p) && next_indent > indent {
            parse_block(p, next_indent);
        }
    }
    p.complete(marker, YAML_SEQUENCE_ITEM);
}

fn parse_compact_mapping(p: &mut Parser<'_>, indent: usize) {
    let marker = p.start();
    parse_pair_line(p, indent, false);
    while !p.at_end() {
        skip_blank_lines(p);
        if current_indent(p) != indent || line_starts_sequence(p) || !line_has_top_level_colon(p) {
            break;
        }
        parse_pair_line(p, indent, true);
    }
    p.complete(marker, YAML_MAPPING);
}

fn parse_value_line(p: &mut Parser<'_>) -> bool {
    if p.at(TK_INDENT) {
        consume_indent(p);
    }
    let block_scalar = p.at(TK_BLOCK_SCALAR);
    if !parse_value(p) {
        return false;
    }
    if !block_scalar {
        consume_line_end(p);
    }
    true
}

fn parse_value(p: &mut Parser<'_>) -> bool {
    match p.peek() {
        Some(TK_OPEN_BRACE) => parse_flow_mapping(p),
        Some(TK_OPEN_BRACKET) => parse_flow_sequence(p),
        _ => return parse_scalar(p),
    }
    true
}

fn parse_scalar(p: &mut Parser<'_>) -> bool {
    if !p.at_set(SCALAR_TOKENS) {
        return false;
    }
    let marker = p.start();
    let token = p.bump();
    let text = token.text.as_bytes();
    match token.kind {
        TK_SINGLE_QUOTED_SCALAR if text.len() < 2 || text[text.len() - 1] != b'\'' => {
            p.add_error(ErrorBuilder::new("closing single quote").at_token(&token));
        }
        TK_DOUBLE_QUOTED_SCALAR
            if text.len() < 2
                || text[text.len() - 1] != b'"'
                || has_invalid_double_quoted_escape(text) =>
        {
            p.add_error(ErrorBuilder::new("valid double-quoted scalar").at_token(&token));
        }
        _ => {}
    }
    p.complete(marker, YAML_SCALAR);
    true
}

fn parse_flow_mapping(p: &mut Parser<'_>) {
    let marker = p.start();
    p.bump();
    skip_flow_separation(p);

    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        let pair = p.start();
        if !parse_scalar(p) {
            p.add_error(ErrorBuilder::new("flow mapping key"));
            p.recover(&[TK_COLON, TK_COMMA, TK_CLOSE_BRACE]);
        }
        p.expect(TK_COLON, &[TK_COMMA, TK_CLOSE_BRACE]);
        if !parse_value(p) {
            complete_null(p);
        }
        p.complete(pair, YAML_PAIR);
        skip_flow_separation(p);

        if p.at(TK_COMMA) {
            p.bump();
            skip_flow_separation(p);
        } else if !p.at(TK_CLOSE_BRACE) {
            p.add_error(ErrorBuilder::new("\",\" or \"}\""));
            p.recover(&[TK_COMMA, TK_CLOSE_BRACE]);
            if p.at(TK_COMMA) {
                p.bump();
            }
        }
        if p.get_pos() == position {
            break;
        }
    }

    p.expect(TK_CLOSE_BRACE, VALUE_RECOVERY);
    p.complete(marker, YAML_FLOW_MAPPING);
}

fn parse_flow_sequence(p: &mut Parser<'_>) {
    let marker = p.start();
    p.bump();
    skip_flow_separation(p);

    while !p.at_end() && !p.at(TK_CLOSE_BRACKET) {
        let position = p.get_pos();
        let item = p.start();
        if !parse_value(p) {
            p.add_error(ErrorBuilder::new("flow sequence value"));
            p.recover(&[TK_COMMA, TK_CLOSE_BRACKET]);
        }
        p.complete(item, YAML_SEQUENCE_ITEM);
        skip_flow_separation(p);

        if p.at(TK_COMMA) {
            p.bump();
            skip_flow_separation(p);
        } else if !p.at(TK_CLOSE_BRACKET) {
            p.add_error(ErrorBuilder::new("\",\" or \"]\""));
            p.recover(&[TK_COMMA, TK_CLOSE_BRACKET]);
            if p.at(TK_COMMA) {
                p.bump();
            }
        }
        if p.get_pos() == position {
            break;
        }
    }

    p.expect(TK_CLOSE_BRACKET, VALUE_RECOVERY);
    p.complete(marker, YAML_FLOW_SEQUENCE);
}

fn complete_null(p: &mut Parser<'_>) {
    let marker = p.start();
    p.complete(marker, YAML_NULL);
}

fn skip_blank_lines(p: &mut Parser<'_>) {
    loop {
        if p.at(TK_LINE_BREAK) {
            p.bump();
        } else if p.at(TK_INDENT) && p.peek_next_non_trivia_kind() == Some(TK_LINE_BREAK) {
            consume_indent(p);
            p.bump();
        } else {
            return;
        }
    }
}

fn skip_flow_separation(p: &mut Parser<'_>) {
    loop {
        if p.at(TK_INDENT) {
            consume_indent(p);
        } else if p.at(TK_LINE_BREAK) {
            p.bump();
        } else {
            return;
        }
    }
}

fn consume_indent(p: &mut Parser<'_>) {
    let token = p.bump();
    if token.text.contains('\t') {
        p.add_error(ErrorBuilder::new("spaces for YAML indentation").at_token(&token));
    }
}

fn consume_line_end(p: &mut Parser<'_>) {
    if p.at(TK_LINE_BREAK) {
        p.bump();
        return;
    }
    if at_document_boundary(p) {
        return;
    }
    p.add_error(ErrorBuilder::new("end of line"));
    recover_line(p);
}

fn recover_line(p: &mut Parser<'_>) {
    p.recover(&[TK_LINE_BREAK]);
    if p.at(TK_LINE_BREAK) {
        p.bump();
    }
}

fn current_indent(p: &Parser<'_>) -> usize {
    match p.peek_token() {
        Some(token) if token.kind == TK_INDENT => token
            .text
            .bytes()
            .map(|byte| if byte == b'\t' { 2 } else { 1 })
            .sum(),
        _ => 0,
    }
}

/// The first non-trivia token on the current line, `None` at a line break or
/// end of input.
fn line_tokens<'p, 'a>(p: &'p Parser<'a>) -> impl Iterator<Item = Kind> + 'p {
    (0..)
        .map_while(move |offset| p.peek_nth_token(offset).map(|token| token.kind))
        .take_while(|kind| *kind != TK_LINE_BREAK)
        .filter(|kind| !kind.is_trivia() && *kind != TK_INDENT)
}

fn line_starts_sequence(p: &Parser<'_>) -> bool {
    line_tokens(p).next() == Some(TK_DASH)
}

fn line_has_top_level_colon(p: &Parser<'_>) -> bool {
    let mut depth = 0usize;
    for kind in line_tokens(p) {
        match kind {
            TK_OPEN_BRACKET | TK_OPEN_BRACE => depth += 1,
            TK_CLOSE_BRACKET | TK_CLOSE_BRACE => depth = depth.saturating_sub(1),
            TK_COLON if depth == 0 => return true,
            _ => {}
        }
    }
    false
}

fn has_invalid_double_quoted_escape(text: &[u8]) -> bool {
    if text.len() < 2 {
        return true;
    }
    let limit = text.len() - 1;
    let mut position = 1;
    while position < limit {
        match text[position..limit].iter().position(|byte| *byte == b'\\') {
            None => break,
            Some(offset) => position += offset,
        }
        position += 1;
        if position >= limit {
            return true;
        }
        match text[position] {
            b'0' | b'a' | b'b' | b't' | b'n' | b'v' | b'f' | b'r' | b'e' | b' ' | b'"' | b'/'
            | b'\\' | b'N' | b'_' | b'L' | b'P' | b'\r' | b'\n' => {}
            escape @ (b'x' | b'u' | b'U') => {
                let count = match escape {
                    b'x' => 2,
                    b'u' => 4,
                    _ => 8,
                };
                if !has_hex_digits(text, position + 1, count) {
                    return true;
                }
                position += count;
            }
            _ => return true,
        }
        position += 1;
    }
    false
}

fn has_hex_digits(text: &[u8], start: usize, count: usize) -> bool {
    start + count < text.len() && text[start..start + count].iter().all(u8::is_ascii_hexdigit)
}

fn is_collection_decorator(text: &str) -> bool {
    let text = text.trim();
    let bytes = text.as_bytes();
    bytes.len() >= 2 && (bytes[0] == b'&' || bytes[0] == b'!') && !text.contains([' ', '\t'])
}

#[cfg(test)]
mod tests {
    use super::*;

    fn assert_lossless(source: &str) -> Parse {
        let parse = parse(source);
        assert_eq!(parse.root().text().to_string(), source);
        for error in &parse.errors {
            assert!(
                u32::from(error.range.end()) as usize <= source.len(),
                "invalid error range {error}"
            );
        }
        parse
    }

    #[test]
    fn parses_valid_yaml_losslessly() {
        let source = r#"---
services:
  App\Service\Example:
    class: "App\\Service\\Example"
    tags:
      - name: kernel.event_subscriber
      - { name: doctrine.listener, event: postPersist }
    arguments: ['@logger', "%kernel.debug%"]
parameters:
  message: |
    first line
    second line
...
"#;
        let parse = assert_lossless(source);
        assert!(parse.errors.is_empty(), "{}", parse.debug_dump());
    }

    #[test]
    fn parses_indentless_sequences_and_anchored_collections() {
        for source in [
            "steps:\n- name: Checkout\n  uses: actions/checkout@v5\n",
            "defaults: &defaults\n  enabled: true\nitems:\n  - !item\n    name: first\n",
            "%YAML 1.2\n---\na: 1\n---\nb: 2\n...\n",
        ] {
            let parse = assert_lossless(source);
            assert!(parse.errors.is_empty(), "{}", parse.debug_dump());
        }
    }

    #[test]
    fn reports_malformed_yaml_and_keeps_tree() {
        for source in [
            "key: value\nother scalar\n",
            "key: [one, two\n",
            "key: {name value}\n",
            "key: \"unterminated\n",
            "key:\n\tchild: value\n",
            "- one\n  invalid: value\n",
            "key: \"bad \\q escape\"\n",
            "key: 'open\n",
        ] {
            let parse = assert_lossless(source);
            assert!(
                !parse.errors.is_empty(),
                "no errors for {source:?}\n{}",
                parse.debug_dump()
            );
        }
    }

    #[test]
    fn tolerates_incomplete_input() {
        for source in [
            "",
            "key: value\n",
            "items:\n  - one\n  - two\n",
            "- name: feature\n  enabled: true\n",
            "flow: [{name: one}, two]\n",
            "block: |-\n  first\n  second\n",
            "---\nfoo: bar\n...\n",
            "key: [unterminated\n",
            "\u{fffd}\u{fffd}:\n",
            "a:\n  b:\n    - ",
            "{a: [b, {c: ",
            "key: \"\\x4",
            "- - -\n",
            ": : :\n",
            "]}\n",
            "a: b: c\n",
            "  \n\t\n# only comment",
            "ä: ö\nü: [é, 😀]\n",
        ] {
            assert_lossless(source);
        }
    }

    #[test]
    fn debug_tree_prints_kinds_ranges_and_token_text() {
        let parse = parse("a: [1]\n");
        assert_eq!(
            parse.debug_dump(),
            "YAML_STREAM@0..7\n  YAML_DOCUMENT@0..7\n    YAML_MAPPING@0..7\n      YAML_PAIR@0..7\n        YAML_SCALAR@0..1\n          YAML_PLAIN_SCALAR_TOKEN@0..1 \"a\"\n        YAML_COLON@1..2 \":\"\n        YAML_FLOW_SEQUENCE@2..6\n          YAML_WHITESPACE@2..3 \" \"\n          YAML_OPEN_BRACKET@3..4 \"[\"\n          YAML_SEQUENCE_ITEM@4..5\n            YAML_SCALAR@4..5\n              YAML_PLAIN_SCALAR_TOKEN@4..5 \"1\"\n          YAML_CLOSE_BRACKET@5..6 \"]\"\n        YAML_LINE_BREAK@6..7 \"\\n\""
        );
    }
}
