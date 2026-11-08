//! Lossless, error-tolerant SCSS parser (port of Go `internal/parser/scss`).

pub mod lexer;
pub mod query;
pub mod syntax;

use std::sync::Arc;

use swlsp_syntax::{Config, ErrorBuilder, Kind, Parse, Parser};

use syntax::*;

const CLOSE_RECOVERY: &[Kind] = &[TK_SEMICOLON, TK_CLOSE_BRACE];

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
            general_recovery_set: CLOSE_RECOVERY,
            error_kind: ERROR,
        },
    );
    let root = p.start();
    while !p.at_end() {
        if p.at(TK_CLOSE_BRACE) {
            p.add_error(ErrorBuilder::new("SCSS statement"));
            p.bump();
            continue;
        }
        parse_statement(&mut p);
    }
    p.complete(root, SCSS_STYLESHEET);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

fn parse_statement(p: &mut Parser<'_>) {
    let marker = p.start();
    let kind = classify_statement(p);
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        if p.at(TK_OPEN_BRACE) {
            parse_block(p);
            if p.at(TK_SEMICOLON) {
                p.bump();
            }
            break;
        }
        if p.at(TK_SEMICOLON) {
            p.bump();
            break;
        }
        if !parse_component(p, &[]) {
            p.add_error(ErrorBuilder::new("SCSS expression"));
            p.bump();
        }
    }
    p.complete(marker, kind);
}

fn classify_statement(p: &Parser<'_>) -> Kind {
    let (first, has_colon, has_block) = statement_shape(p);
    match first {
        Some(TK_VARIABLE) if has_colon => SCSS_VARIABLE_DECLARATION,
        Some(TK_AT_KEYWORD) => SCSS_AT_RULE,
        _ if has_block => SCSS_RULE,
        _ if has_colon => SCSS_DECLARATION,
        _ => SCSS_RULE,
    }
}

/// Scans ahead to the end of the current statement: its first significant
/// token kind, whether it has a top-level colon, and whether it opens a block.
fn statement_shape(p: &Parser<'_>) -> (Option<Kind>, bool, bool) {
    let mut first = None;
    let (mut paren, mut bracket, mut interpolation) = (0usize, 0usize, 0usize);
    let mut has_colon = false;
    let mut offset = 0;
    while let Some(token) = p.peek_nth_token(offset) {
        offset += 1;
        let kind = token.kind;
        if kind.is_trivia() {
            continue;
        }
        first.get_or_insert(kind);
        let top_level = paren == 0 && bracket == 0 && interpolation == 0;
        match kind {
            TK_OPEN_PAREN => paren += 1,
            TK_CLOSE_PAREN => paren = paren.saturating_sub(1),
            TK_OPEN_BRACKET => bracket += 1,
            TK_CLOSE_BRACKET => bracket = bracket.saturating_sub(1),
            TK_INTERPOLATION_OPEN => interpolation += 1,
            TK_CLOSE_BRACE if interpolation > 0 => interpolation -= 1,
            TK_CLOSE_BRACE if paren == 0 && bracket == 0 => return (first, has_colon, false),
            TK_COLON if top_level => has_colon = true,
            TK_OPEN_BRACE if top_level => return (first, has_colon, true),
            TK_SEMICOLON if top_level => return (first, has_colon, false),
            _ => {}
        }
    }
    (first, has_colon, false)
}

fn parse_block(p: &mut Parser<'_>) {
    let marker = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        parse_statement(p);
    }
    p.expect(TK_CLOSE_BRACE, &[TK_SEMICOLON]);
    p.complete(marker, SCSS_BLOCK);
}

fn parse_component(p: &mut Parser<'_>, stop: &[Kind]) -> bool {
    let Some(kind) = p.peek() else {
        return false;
    };
    if stop.contains(&kind) {
        return false;
    }
    match kind {
        TK_VARIABLE => parse_leaf(p, SCSS_VARIABLE, None),
        TK_SINGLE_QUOTED_STRING => parse_leaf(p, SCSS_STRING, Some(b'\'')),
        TK_DOUBLE_QUOTED_STRING => parse_leaf(p, SCSS_STRING, Some(b'"')),
        TK_IDENTIFIER if p.peek_next_non_trivia_kind() == Some(TK_OPEN_PAREN) => {
            parse_function_call(p)
        }
        TK_OPEN_PAREN => parse_delimited(p, TK_CLOSE_PAREN, SCSS_PARENTHESIZED),
        TK_OPEN_BRACKET => parse_delimited(p, TK_CLOSE_BRACKET, SCSS_BRACKET_LIST),
        TK_INTERPOLATION_OPEN => parse_interpolation(p),
        TK_CLOSE_PAREN | TK_CLOSE_BRACKET => {
            p.add_error(ErrorBuilder::new("SCSS expression"));
            p.bump();
        }
        _ => {
            p.bump();
        }
    }
    true
}

fn parse_leaf(p: &mut Parser<'_>, kind: Kind, expected_closing: Option<u8>) {
    let marker = p.start();
    let token = p.bump();
    if let Some(closing) = expected_closing {
        let text = token.text.as_bytes();
        if text.len() < 2 || text[text.len() - 1] != closing {
            p.add_error(ErrorBuilder::new("closing quote").at_token(&token));
        }
    }
    p.complete(marker, kind);
}

fn parse_function_call(p: &mut Parser<'_>) {
    let marker = p.start();
    p.bump();
    let arguments = p.start();
    p.expect(TK_OPEN_PAREN, &[TK_CLOSE_PAREN, TK_SEMICOLON]);
    while !p.at_end() && !p.at(TK_CLOSE_PAREN) {
        let position = p.get_pos();
        let argument = p.start();
        while !p.at_end() && !p.at(TK_COMMA) && !p.at(TK_CLOSE_PAREN) {
            if !parse_component(p, &[TK_COMMA, TK_CLOSE_PAREN]) {
                break;
            }
        }
        p.complete(argument, SCSS_ARGUMENT);
        if p.at(TK_COMMA) {
            p.bump();
        } else if !p.at(TK_CLOSE_PAREN) {
            p.add_error(ErrorBuilder::new("\",\" or \")\""));
            p.recover(&[TK_COMMA, TK_CLOSE_PAREN]);
            if p.at(TK_COMMA) {
                p.bump();
            }
        }
        if p.get_pos() == position {
            break;
        }
    }
    p.expect(TK_CLOSE_PAREN, CLOSE_RECOVERY);
    p.complete(arguments, SCSS_ARGUMENT_LIST);
    p.complete(marker, SCSS_FUNCTION_CALL);
}

fn parse_delimited(p: &mut Parser<'_>, closing: Kind, kind: Kind) {
    let marker = p.start();
    p.bump();
    while !p.at_end() && !p.at(closing) {
        if !parse_component(p, &[closing]) {
            break;
        }
    }
    p.expect(closing, CLOSE_RECOVERY);
    p.complete(marker, kind);
}

fn parse_interpolation(p: &mut Parser<'_>) {
    let marker = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        if !parse_component(p, &[TK_CLOSE_BRACE]) {
            break;
        }
    }
    p.expect(
        TK_CLOSE_BRACE,
        &[TK_SEMICOLON, TK_CLOSE_PAREN, TK_CLOSE_BRACKET],
    );
    p.complete(marker, SCSS_INTERPOLATION);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_valid_scss_losslessly() {
        let source = "@use \"sass:map\";\n\n$sw-color-brand-primary: #0042a0 !default;\n$variants: (primary: blue, danger: red);\n\n@mixin button($variant) {\n  .button-#{$variant}:hover {\n    color: map.get($variants, $variant);\n    content: feature(\"ACCESSIBILITY_TWEAKS\");\n  }\n}";
        let parse = parse(source);
        assert_eq!(parse.root().text().to_string(), source);
        assert!(parse.errors.is_empty(), "{}", parse.debug_dump());
    }

    #[test]
    fn reports_malformed_scss_and_keeps_tree() {
        for source in [
            "}",
            ".button { color: red;",
            "color: feature('flag';",
            "content: \"unterminated;",
            "width: calc(100% - (2rem);",
        ] {
            let parse = parse(source);
            assert_eq!(parse.root().text().to_string(), source);
            assert!(
                !parse.errors.is_empty(),
                "no errors for {source:?}:\n{}",
                parse.debug_dump()
            );
            for error in &parse.errors {
                assert!(u32::from(error.range.end()) as usize <= source.len());
            }
        }
    }

    #[test]
    fn fuzz_seeds_are_lossless() {
        for source in [
            "",
            "$value: red;",
            ".button { color: $value; }",
            "feature(\"FLAG\")",
            "@media (width > 10px) { .a { display: block; } }",
            ".button-#{$variant} {}",
            "calc(100% - var(--gap))",
            "/* unterminated",
            "\u{fffd}é{}\\",
            "a(#{",
            "a: (b, [c)];",
            "#{ } } ;",
        ] {
            let parse = parse(source);
            assert_eq!(parse.root().text().to_string(), source);
            for error in &parse.errors {
                assert!(u32::from(error.range.end()) as usize <= source.len());
            }
            let _ = parse.debug_dump();
        }
    }

    #[test]
    fn classifies_statements() {
        let parse = parse("$a: 1; @include x; a { b: c; } d;");
        let kinds: Vec<_> = parse.root().children().map(|node| node.kind()).collect();
        assert_eq!(
            kinds,
            [
                SCSS_VARIABLE_DECLARATION,
                SCSS_AT_RULE,
                SCSS_RULE,
                SCSS_RULE
            ]
        );
        let block = parse
            .root()
            .descendants()
            .find(|node| node.kind() == SCSS_BLOCK)
            .unwrap();
        assert_eq!(block.children().next().unwrap().kind(), SCSS_DECLARATION);
    }

    #[test]
    fn debug_dump_matches_go() {
        let parse = parse("a: f('x)");
        assert_eq!(
            parse.debug_dump(),
            "SCSS_STYLESHEET@0..8\n  SCSS_DECLARATION@0..8\n    SCSS_IDENTIFIER@0..1 \"a\"\n    SCSS_COLON@1..2 \":\"\n    SCSS_FUNCTION_CALL@2..8\n      SCSS_WHITESPACE@2..3 \" \"\n      SCSS_IDENTIFIER@3..4 \"f\"\n      SCSS_ARGUMENT_LIST@4..8\n        SCSS_OPEN_PAREN@4..5 \"(\"\n        SCSS_ARGUMENT@5..8\n          SCSS_STRING@5..8\n            SCSS_SINGLE_QUOTED_STRING@5..8 \"'x)\"\nerror at 5..8: expected closing quote but found single-quoted string\nerror at 5..8: expected \",\" or \")\" but reached end of file\nerror at 5..8: expected ) but reached end of file"
        );
    }
}
