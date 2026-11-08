//! Lossless, error-tolerant XPath parser for embedded expressions (for
//! example Symfony DomCrawler selectors). The grammar is deliberately flat:
//! it only structures parenthesized groups and predicates and reports
//! malformed tokens, unterminated strings and unbalanced delimiters.

pub mod lexer;
pub mod syntax;

use std::sync::Arc;

use swlsp_syntax::{Config, ErrorBuilder, Kind, Parse, Parser};

use syntax::*;

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
        p.add_error(ErrorBuilder::new("XPath expression"));
    } else {
        parse_sequence(&mut p, None);
    }
    p.complete(root, XPATH_DOCUMENT);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

fn parse_sequence(p: &mut Parser<'_>, closing: Option<Kind>) {
    while let Some(kind) = p.peek()
        && Some(kind) != closing
    {
        match kind {
            TK_OPEN_PAREN => parse_delimited(p, TK_CLOSE_PAREN, XPATH_GROUP),
            TK_OPEN_BRACKET => parse_delimited(p, TK_CLOSE_BRACKET, XPATH_PREDICATE),
            TK_CLOSE_PAREN | TK_CLOSE_BRACKET => {
                p.add_error(ErrorBuilder::new("XPath expression"));
                p.bump();
            }
            TK_STRING => {
                let token = p.bump();
                let bytes = token.text.as_bytes();
                if bytes.len() < 2 || bytes[0] != bytes[bytes.len() - 1] {
                    p.add_error(ErrorBuilder::new("closing quote").at_token(&token));
                }
            }
            TK_UNKNOWN => {
                let token = p.bump();
                p.add_error(ErrorBuilder::new("XPath token").at_token(&token));
            }
            TK_DOLLAR => {
                p.bump();
                if !p.at(TK_NAME) {
                    p.add_error(ErrorBuilder::new("variable name"));
                }
            }
            TK_AT => {
                p.bump();
                if !p.at_set(&[TK_NAME, TK_STAR]) {
                    p.add_error(ErrorBuilder::new("attribute name"));
                }
            }
            _ => {
                p.bump();
            }
        }
    }
}

fn parse_delimited(p: &mut Parser<'_>, closing: Kind, kind: Kind) {
    let marker = p.start();
    p.bump();
    if kind == XPATH_PREDICATE && p.at(closing) {
        p.add_error(ErrorBuilder::new("XPath predicate"));
    }
    parse_sequence(p, Some(closing));
    p.expect(closing, &[]);
    p.complete(marker, kind);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lossless_xpath() {
        let source = r#"count(.//article[@data-id=$id and contains(@class, "active")]) > 0"#;
        let parse = parse(source);
        assert_eq!(parse.root().text().to_string(), source);
        assert!(parse.errors.is_empty(), "{:?}", parse.errors);
        assert_eq!(parse.root().kind(), XPATH_DOCUMENT);
        let kinds: Vec<_> = parse.root().descendants().map(|node| node.kind()).collect();
        assert_eq!(
            kinds,
            [XPATH_DOCUMENT, XPATH_GROUP, XPATH_PREDICATE, XPATH_GROUP]
        );
    }

    #[test]
    fn malformed_xpath_keeps_tree() {
        for (source, expected) in [
            ("", "XPath expression"),
            (r#".//article[@id="open]"#, "closing quote"),
            (".//article[@id=$]", "variable name"),
            (".//article[@]", "attribute name"),
            (".//article[]", "XPath predicate"),
            (".//article[@id='x'", "]"),
            (".//article)", "XPath expression"),
            (".//article#bad", "XPath token"),
        ] {
            let parse = parse(source);
            assert_eq!(parse.root().text().to_string(), source);
            assert!(!parse.errors.is_empty(), "{source}");
            assert!(
                parse.errors.iter().any(|error| error.expected == expected),
                "{source}: {:?}",
                parse.errors
            );
            for error in &parse.errors {
                assert!(error.range.end() <= swlsp_syntax::TextSize::of(source));
            }
        }
    }

    #[test]
    fn debug_dump() {
        assert_eq!(
            parse("a[ ]").debug_dump(),
            "XPATH_DOCUMENT@0..4\n  XPATH_NAME@0..1 \"a\"\n  XPATH_PREDICATE@1..4\n    XPATH_OPEN_BRACKET@1..2 \"[\"\n    XPATH_WHITESPACE@2..3 \" \"\n    XPATH_CLOSE_BRACKET@3..4 \"]\"\nerror at 3..4: expected XPath predicate but found ]"
        );
    }
}
