//! Lossless, error-tolerant XML parser.

pub mod lexer;
pub mod query;
pub mod syntax;

use std::sync::Arc;

use swlsp_syntax::{Config, ErrorBuilder, Kind, Parse, Parser};

use syntax::*;

const TAG_RECOVERY: &[Kind] = &[TK_OPEN_TAG, TK_OPEN_END_TAG];

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
            general_recovery_set: TAG_RECOVERY,
            error_kind: ERROR,
        },
    );
    let root = p.start();
    while !p.at_end() {
        parse_document_item(&mut p);
    }
    p.complete(root, XML_DOCUMENT);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

/// Node kind for a leaf token that may appear both at document level and
/// inside element content.
fn leaf_kind(kind: Kind) -> Option<Kind> {
    Some(match kind {
        TK_TEXT => XML_TEXT,
        TK_ENTITY_REFERENCE => XML_ENTITY_REFERENCE,
        TK_COMMENT => XML_COMMENT,
        TK_CDATA => XML_CDATA,
        TK_PROCESSING_INSTRUCTION => XML_PROCESSING_INSTRUCTION,
        TK_DOCTYPE => XML_DOCTYPE,
        _ => return None,
    })
}

fn parse_document_item(p: &mut Parser<'_>) {
    match p.peek() {
        Some(TK_OPEN_TAG) => parse_element(p),
        Some(TK_OPEN_END_TAG) => {
            p.add_error(ErrorBuilder::new("XML element"));
            parse_end_tag(p);
        }
        Some(kind) if let Some(node) = leaf_kind(kind) => parse_leaf(p, node),
        _ => {
            p.add_error(ErrorBuilder::new("XML document item"));
            parse_leaf(p, ERROR);
        }
    }
}

fn parse_element(p: &mut Parser<'_>) {
    let element = p.start();
    let (name, empty) = parse_start_tag(p);
    if empty {
        p.complete(element, XML_ELEMENT);
        return;
    }

    let content = p.start();
    while let Some(kind) = p.peek()
        && kind != TK_OPEN_END_TAG
    {
        if kind == TK_OPEN_TAG {
            parse_element(p);
        } else if let Some(node) = leaf_kind(kind) {
            parse_leaf(p, node);
        } else {
            p.add_error(ErrorBuilder::new("XML content"));
            parse_leaf(p, ERROR);
        }
    }
    p.complete(content, XML_CONTENT);

    let name = name.unwrap_or("");
    if p.at(TK_OPEN_END_TAG) {
        let closing = parse_end_tag(p);
        if let Some(closing) = closing
            && !name.is_empty()
            && name != closing
        {
            p.add_error(ErrorBuilder::new(format!("closing tag </{name}>")));
        }
    } else {
        p.add_error(ErrorBuilder::new(format!("closing tag </{name}>")));
    }
    p.complete(element, XML_ELEMENT);
}

/// Parses a start or empty-element tag; returns the element name and whether
/// the tag was self-closing.
fn parse_start_tag<'a>(p: &mut Parser<'a>) -> (Option<&'a str>, bool) {
    let tag = p.start();
    p.bump();

    let name = if p.at(TK_NAME) {
        Some(parse_name(p))
    } else {
        p.add_error(ErrorBuilder::new("element name"));
        None
    };

    while let Some(kind) = p.peek()
        && !matches!(
            kind,
            TK_CLOSE_TAG | TK_CLOSE_EMPTY_TAG | TK_OPEN_TAG | TK_OPEN_END_TAG
        )
    {
        if kind == TK_NAME {
            parse_attribute(p);
            continue;
        }
        p.add_error(ErrorBuilder::new("attribute or tag close"));
        parse_leaf(p, ERROR);
    }

    match p.peek() {
        Some(TK_CLOSE_EMPTY_TAG) => {
            p.bump();
            p.complete(tag, XML_EMPTY_ELEMENT_TAG);
            (name, true)
        }
        Some(TK_CLOSE_TAG) => {
            p.bump();
            p.complete(tag, XML_START_TAG);
            (name, false)
        }
        _ => {
            p.add_error(ErrorBuilder::new("\">\" or \"/>\""));
            p.complete(tag, XML_START_TAG);
            (name, false)
        }
    }
}

fn parse_attribute(p: &mut Parser<'_>) {
    let attribute = p.start();
    parse_name(p);

    if p.at(TK_EQUALS) {
        p.bump();
        if p.at(TK_ATTRIBUTE_VALUE) {
            let value = p.start();
            let token = p.bump();
            if !is_closed_attribute_value(token.text) {
                p.add_error(ErrorBuilder::new("closing quote").at_token(&token));
            }
            p.complete(value, XML_ATTRIBUTE_VALUE);
        } else if p.at(TK_NAME) {
            // Preserve invalid unquoted values as usable editor context.
            let value = p.start();
            p.bump();
            p.add_error(ErrorBuilder::new("quoted attribute value"));
            p.complete(value, XML_ATTRIBUTE_VALUE);
        } else {
            p.add_error(ErrorBuilder::new("attribute value"));
        }
    } else {
        p.add_error(ErrorBuilder::new("\"=\""));
    }

    p.complete(attribute, XML_ATTRIBUTE);
}

fn parse_name<'a>(p: &mut Parser<'a>) -> &'a str {
    let name = p.start();
    let token = p.bump();
    p.complete(name, XML_NAME);
    token.text
}

fn parse_end_tag<'a>(p: &mut Parser<'a>) -> Option<&'a str> {
    let tag = p.start();
    p.bump();

    let name = if p.at(TK_NAME) {
        Some(parse_name(p))
    } else {
        p.add_error(ErrorBuilder::new("element name"));
        None
    };

    p.expect(TK_CLOSE_TAG, TAG_RECOVERY);
    p.complete(tag, XML_END_TAG);
    name
}

fn parse_leaf(p: &mut Parser<'_>, kind: Kind) {
    let node = p.start();
    p.bump();
    p.complete(node, kind);
}

fn is_closed_attribute_value(text: &str) -> bool {
    let bytes = text.as_bytes();
    bytes.len() >= 2 && matches!(bytes[0], b'"' | b'\'') && bytes[bytes.len() - 1] == bytes[0]
}

#[cfg(test)]
mod tests {
    use super::*;

    fn assert_lossless(source: &str) -> Parse {
        let parse = parse(source);
        assert_eq!(parse.root().text().to_string(), source);
        parse
    }

    #[test]
    fn document_structure_and_losslessness() {
        let source = r#"<?xml version="1.0"?><container><services><service id="App\Service"><tag name="kernel.event_subscriber"/></service></services></container>"#;
        let parse = assert_lossless(source);
        assert!(parse.errors.is_empty(), "{:?}", parse.errors);
        assert_eq!(parse.root().kind(), XML_DOCUMENT);
        let elements = parse
            .root()
            .descendants()
            .filter(|node| node.kind() == XML_ELEMENT)
            .count();
        assert_eq!(elements, 4);
    }

    #[test]
    fn incomplete_xml_produces_usable_tree() {
        let parse = assert_lossless(r#"<container><service id="App\Service"#);
        assert!(!parse.errors.is_empty());
    }

    #[test]
    fn mismatched_tags_recover() {
        let parse = assert_lossless("<root><child>value</root><next/>");
        assert!(!parse.errors.is_empty());
        assert!(
            parse
                .errors
                .iter()
                .any(|error| error.expected == "closing tag </child>")
        );
    }

    #[test]
    fn project_xml_corpus() {
        // A Shopware app manifest and a system-config definition.
        let root = concat!(env!("CARGO_MANIFEST_DIR"), "/testdata");
        for path in ["manifest.xml", "common.xml"] {
            let content = std::fs::read_to_string(format!("{root}/{path}")).unwrap();
            let parse = assert_lossless(&content);
            assert!(parse.errors.is_empty(), "{path}: {:?}", parse.errors);
        }
    }

    #[test]
    fn fuzz_seeds_and_recovery_are_lossless() {
        for source in [
            "",
            "<",
            r#"<service id=""#,
            r#"<?xml version="1.0"?><root/>"#,
            "<!DOCTYPE root [<!ELEMENT root (#PCDATA)>]><root>&amp;</root>",
            "<root><![CDATA[<broken>]]><!-- comment --></root>",
            "</orphan><root a=b c d=></root",
            "<root <child/></root>",
            "<a x='1' \u{c}\r\n y=\"ü\" ?></a>",
            "<ä>ß</ä>",
        ] {
            assert_lossless(source);
        }
    }

    #[test]
    fn attribute_errors() {
        let parse = assert_lossless("<a b=c d e=/>");
        let expected: Vec<_> = parse
            .errors
            .iter()
            .map(|error| error.expected.as_str())
            .collect();
        assert_eq!(
            expected,
            ["quoted attribute value", "\"=\"", "attribute value"]
        );
        let parse = assert_lossless("<a b=\"open/>");
        assert_eq!(parse.errors[0].expected, "closing quote");
    }
}
