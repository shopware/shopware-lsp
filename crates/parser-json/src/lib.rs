//! Lossless, error-tolerant JSON parser.

pub mod lexer;
pub mod syntax;

use std::sync::Arc;

use swlsp_syntax::{Config, ErrorBuilder, Kind, Parse, Parser};

use syntax::*;

const PAIR_RECOVERY: &[Kind] = &[
    TK_COMMA,
    TK_CLOSE_BRACE,
    TK_OPEN_BRACE,
    TK_OPEN_BRACKET,
    TK_STRING,
    TK_NUMBER,
    TK_TRUE,
    TK_FALSE,
    TK_NULL,
];
const ARRAY_RECOVERY: &[Kind] = &[
    TK_CLOSE_BRACKET,
    TK_OPEN_BRACE,
    TK_OPEN_BRACKET,
    TK_STRING,
    TK_NUMBER,
    TK_TRUE,
    TK_FALSE,
    TK_NULL,
];

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
            general_recovery_set: &[],
            error_kind: ERROR,
        },
    );
    let root = p.start();
    if p.at_end() {
        p.add_error(ErrorBuilder::new("JSON value"));
    } else {
        parse_value(&mut p);
    }
    if !p.at_end() {
        p.add_error(ErrorBuilder::new("end of file"));
        p.recover(&[]);
    }
    p.complete(root, JSON_DOCUMENT);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

fn parse_value(p: &mut Parser<'_>) -> bool {
    match p.peek() {
        Some(TK_OPEN_BRACE) => parse_object(p),
        Some(TK_OPEN_BRACKET) => parse_array(p),
        Some(TK_STRING) => parse_scalar(p, JSON_STRING),
        Some(TK_NUMBER) => parse_scalar(p, JSON_NUMBER),
        Some(TK_TRUE | TK_FALSE) => parse_scalar(p, JSON_BOOLEAN),
        Some(TK_NULL) => parse_scalar(p, JSON_NULL_VALUE),
        _ => return false,
    }
    true
}

fn parse_scalar(p: &mut Parser<'_>, kind: Kind) {
    let marker = p.start();
    let token = p.bump();
    if kind == JSON_STRING && !valid_json_string(token.text) {
        p.add_error(ErrorBuilder::new("valid JSON string").at_token(&token));
    }
    p.complete(marker, kind);
}

fn valid_json_string(text: &str) -> bool {
    let bytes = text.as_bytes();
    if bytes.len() < 2 || bytes[0] != b'"' || bytes[bytes.len() - 1] != b'"' {
        return false;
    }
    let content = &bytes[1..bytes.len() - 1];
    let mut position = 0;
    while position < content.len() {
        let byte = content[position];
        if byte < 0x20 {
            return false;
        }
        if byte != b'\\' {
            position += 1;
            continue;
        }
        if position + 1 >= content.len() {
            return false;
        }
        position += 1;
        match content[position] {
            b'"' | b'\\' | b'/' | b'b' | b'f' | b'n' | b'r' | b't' => {}
            b'u' => {
                if position + 4 >= content.len() {
                    return false;
                }
                if !content[position + 1..position + 5]
                    .iter()
                    .all(u8::is_ascii_hexdigit)
                {
                    return false;
                }
                position += 4;
            }
            _ => return false,
        }
        position += 1;
    }
    true
}

fn parse_object(p: &mut Parser<'_>) {
    let marker = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        if p.at(TK_STRING) {
            parse_pair(p);
        } else {
            p.add_error(ErrorBuilder::new("string object key"));
            p.recover(&[TK_COMMA, TK_CLOSE_BRACE]);
        }
        if p.at(TK_COMMA) {
            p.bump();
            if p.at(TK_CLOSE_BRACE) {
                p.add_error(ErrorBuilder::new("string object key"));
            }
        } else if !p.at_end() && !p.at(TK_CLOSE_BRACE) {
            p.expect(TK_COMMA, &[TK_STRING, TK_CLOSE_BRACE]);
        }
        if p.get_pos() == position {
            break;
        }
    }
    p.expect(TK_CLOSE_BRACE, &[]);
    p.complete(marker, JSON_OBJECT);
}

fn parse_pair(p: &mut Parser<'_>) {
    let marker = p.start();
    parse_scalar(p, JSON_STRING);
    p.expect(TK_COLON, PAIR_RECOVERY);
    if !parse_value(p) {
        p.add_error(ErrorBuilder::new("JSON value"));
        p.recover(&[TK_COMMA, TK_CLOSE_BRACE]);
    }
    p.complete(marker, JSON_PAIR);
}

fn parse_array(p: &mut Parser<'_>) {
    let marker = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACKET) {
        let position = p.get_pos();
        if !parse_value(p) {
            p.add_error(ErrorBuilder::new("JSON value"));
            p.recover(&[TK_COMMA, TK_CLOSE_BRACKET]);
        }
        if p.at(TK_COMMA) {
            p.bump();
            if p.at(TK_CLOSE_BRACKET) {
                p.add_error(ErrorBuilder::new("JSON value"));
            }
        } else if !p.at_end() && !p.at(TK_CLOSE_BRACKET) {
            p.expect(TK_COMMA, ARRAY_RECOVERY);
        }
        if p.get_pos() == position {
            break;
        }
    }
    p.expect(TK_CLOSE_BRACKET, &[]);
    p.complete(marker, JSON_ARRAY);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lossless_and_recovers() {
        for source in ["", "{\"a\": [1, true, null], }", "{\"a\" 1", "[1 2", "{x}"] {
            let parse = parse(source);
            assert_eq!(parse.root().text().to_string(), source);
        }
        let parse = parse("{\"a\": 1}");
        assert!(parse.errors.is_empty());
        assert_eq!(
            parse.debug_dump(),
            "JSON_DOCUMENT@0..8\n  JSON_OBJECT@0..8\n    JSON_OPEN_BRACE@0..1 \"{\"\n    JSON_PAIR@1..7\n      JSON_STRING@1..4\n        JSON_STRING_TOKEN@1..4 \"\\\"a\\\"\"\n      JSON_COLON@4..5 \":\"\n      JSON_NUMBER@5..7\n        JSON_WHITESPACE@5..6 \" \"\n        JSON_NUMBER_TOKEN@6..7 \"1\"\n    JSON_CLOSE_BRACE@7..8 \"}\""
        );
    }
}
