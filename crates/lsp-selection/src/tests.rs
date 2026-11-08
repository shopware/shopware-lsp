use std::sync::Arc;

use swlsp_lsp::{Context, SelectionRangeProvider, SelectionRangeRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_uriutil::file_uri;

use super::{AdminSelectionRangeProvider, PhpSelectionRangeProvider};

fn position(line: u32, character: u32) -> protocol::Position {
    protocol::Position { line, character }
}

fn request(document: TextDocument, positions: Vec<protocol::Position>) -> SelectionRangeRequest {
    SelectionRangeRequest {
        params: protocol::SelectionRangeParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            positions,
            ..Default::default()
        },
        document: Arc::new(document),
    }
}

fn admin_selection_for_needles(
    path: &str,
    source: &str,
    needles: &[&str],
) -> Vec<protocol::SelectionRange> {
    let document = TextDocument::new(file_uri(path), source, 8);
    let mut positions = Vec::with_capacity(needles.len());
    let mut search_from = 0;
    for needle in needles {
        let relative = source[search_from..].find(needle).expect("needle");
        let mut offset = search_from + relative;
        if !needle.is_empty() {
            offset += needle.len().saturating_sub(1);
        }
        let (line, character) = document.line_index().position_utf16(offset as u32);
        positions.push(position(line, character));
        search_from = offset + 1;
    }
    AdminSelectionRangeProvider::new()
        .get_selection_ranges(&Context::background(), &request(document, positions))
        .expect("selection ranges")
}

fn selection_texts(source: &str, selection: &protocol::SelectionRange) -> Vec<String> {
    let document = TextDocument::new(file_uri("/project/selection.html.twig"), source, 1);
    let line_index = document.line_index();
    let mut result = Vec::new();
    let mut current = Some(selection);
    while let Some(value) = current {
        let start = line_index.offset_utf16(value.range.start.line, value.range.start.character);
        let end = line_index.offset_utf16(value.range.end.line, value.range.end.character);
        result.push(source[start as usize..end as usize].to_owned());
        current = value.parent.as_deref();
    }
    result
}

fn position_less_or_equal(left: protocol::Position, right: protocol::Position) -> bool {
    left.line < right.line || left.line == right.line && left.character <= right.character
}

fn assert_strictly_nested(selection: &protocol::SelectionRange) {
    let mut current = selection;
    while let Some(parent) = current.parent.as_deref() {
        assert!(position_less_or_equal(
            parent.range.start,
            current.range.start
        ));
        assert!(position_less_or_equal(current.range.end, parent.range.end));
        assert_ne!(current.range, parent.range);
        current = parent;
    }
}

fn contains(texts: &[String], value: &str) -> bool {
    texts.iter().any(|text| text == value)
}

#[test]
fn admin_javascript_selection_expands_through_semantic_syntax_containers() {
    let source = "// 😀
Component.register('sw-card', {
    methods: {
        save() {
            return this.value;
        },
    },
});";
    let ranges = admin_selection_for_needles("/project/sw-card/index.ts", source, &["value"]);
    assert_eq!(ranges.len(), 1);
    let texts = selection_texts(source, &ranges[0]);
    assert_eq!(texts[0], "value");
    assert!(contains(&texts, "this.value"), "{texts:?}");
    assert!(contains(&texts, "return this.value;"), "{texts:?}");
    assert!(contains(&texts, source));
    assert_strictly_nested(&ranges[0]);
}

#[test]
fn admin_twig_selection_supports_multiple_positions_and_utf16() {
    let source =
        "😀 <sw-card :title=\"product.name\">\n    <span>{{ product.name }}</span>\n</sw-card>";
    let ranges = admin_selection_for_needles(
        "/project/sw-card/sw-card.html.twig",
        source,
        &["sw-card", "product.name", "product.name"],
    );
    assert_eq!(ranges.len(), 3);

    let tag_texts = selection_texts(source, &ranges[0]);
    assert_eq!(tag_texts[0], "sw-card");
    assert!(
        contains(&tag_texts, "<sw-card :title=\"product.name\">"),
        "{tag_texts:?}"
    );
    assert!(contains(&tag_texts, source));
    // The astral character counts as two UTF-16 units before '<' and the tag name.
    assert_eq!(ranges[0].range.start, position(0, 4));

    let attribute_texts = selection_texts(source, &ranges[1]);
    assert_eq!(attribute_texts[0], "name");
    assert!(
        contains(&attribute_texts, "product.name"),
        "{attribute_texts:?}"
    );
    assert!(
        contains(&attribute_texts, ":title=\"product.name\""),
        "{attribute_texts:?}"
    );

    let interpolation_texts = selection_texts(source, &ranges[2]);
    assert_eq!(interpolation_texts[0], "name");
    assert!(
        contains(&interpolation_texts, "product.name"),
        "{interpolation_texts:?}"
    );
    assert!(
        contains(&interpolation_texts, "{{ product.name }}"),
        "{interpolation_texts:?}"
    );
    for range in &ranges {
        assert_strictly_nested(range);
    }
}

#[test]
fn admin_selection_returns_the_empty_document_range() {
    let ranges = admin_selection_for_needles("/project/empty.ts", "", &[""]);
    assert_eq!(ranges.len(), 1);
    assert_eq!(ranges[0].range, protocol::Range::default());
    assert!(ranges[0].parent.is_none());
}

#[test]
fn php_selections_expand_through_current_syntax() {
    let source = "<?php\nclass Example {\n function run($value) {\n  echo '😀'; return $value->method();\n }\n}\n";
    let document = TextDocument::new("file:///live.php", source, 1);
    let offset = source.find("method").expect("method") as u32;
    let (line, column) = document.line_index().position_utf16(offset);
    let positions = vec![position(line, column), position(3, 0), position(6, 0)];
    let result = PhpSelectionRangeProvider::new()
        .get_selection_ranges(
            &Context::background(),
            &request(document, positions.clone()),
        )
        .expect("selection ranges");
    assert_eq!(result.len(), positions.len());
    let texts = selection_texts(source, &result[0]);
    assert_eq!(texts[0], "method");
    assert!(contains(&texts, "$value->method()"), "{texts:?}");
    assert!(contains(&texts, "return $value->method();"), "{texts:?}");
    assert!(contains(&texts, source));
    for (item, cursor) in result.iter().zip(&positions) {
        assert_strictly_nested(item);
        let mut current = Some(item);
        while let Some(value) = current {
            assert!(position_less_or_equal(value.range.start, *cursor));
            assert!(position_less_or_equal(*cursor, value.range.end));
            current = value.parent.as_deref();
        }
    }
}

#[test]
fn php_selection_empty_incomplete_and_unsupported() {
    let provider = PhpSelectionRangeProvider::new();
    for source in ["", "<?php function incomplete($value", "<?php\n/* 😀 */\n"] {
        let document = TextDocument::new("file:///live.php", source, 1);
        let result = provider
            .get_selection_ranges(
                &Context::background(),
                &request(document, vec![position(0, 0)]),
            )
            .expect("selection ranges");
        assert_eq!(result.len(), 1, "{source:?}");
        assert_strictly_nested(&result[0]);
    }
    let document = TextDocument::new("file:///x.js", "x", 1);
    let result = provider
        .get_selection_ranges(
            &Context::background(),
            &request(document, vec![position(0, 0)]),
        )
        .expect("selection ranges");
    assert!(result.is_empty());

    let ctx = Context::background();
    ctx.token().cancel();
    let document = TextDocument::new("file:///live.php", "<?php", 1);
    let error = provider
        .get_selection_ranges(&ctx, &request(document, vec![position(0, 0)]))
        .expect_err("cancelled");
    assert!(swlsp_lsp::is_cancellation(error.as_ref()));
}
