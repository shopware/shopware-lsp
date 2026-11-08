use std::sync::Arc;

use swlsp_lsp::{
    Context, LinkedEditingRangeProvider, LinkedEditingRangeRequest, SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

use super::*;

fn document(source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(
        file_uri("/project/component.html.twig"),
        source,
        version,
    ))
}

fn linked_at(source: &str, needle: &str, within: usize) -> Option<protocol::LinkedEditingRanges> {
    let document = document(source, 2);
    let offset = (source.find(needle).expect("needle") + within) as u32;
    let (line, character) = document.line_index().position_utf16(offset);
    let root = document.root().expect("root");
    let node = root.node_at(TextSize::from(offset));
    let request = LinkedEditingRangeRequest {
        params: protocol::LinkedEditingRangeParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            position: protocol::Position { line, character },
        },
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document.clone()),
            root: Some(root),
            token: None,
            node,
        },
    };
    AdminLinkedEditingProvider::new()
        .get_linked_editing_ranges(&Context::background(), &request)
        .expect("linked editing")
}

fn range_text(source: &str, range: protocol::Range) -> String {
    let document = document(source, 1);
    let lines = document.line_index();
    let start = lines.offset_utf16(range.start.line, range.start.character) as usize;
    let end = lines.offset_utf16(range.end.line, range.end.character) as usize;
    source[start..end].to_owned()
}

#[test]
fn links_the_exact_nested_component_tag_pair() {
    let source = "😀 <sw-card>\n  <sw-button>Save</sw-button>\n</sw-card>";
    let card = linked_at(source, "sw-card", 2).expect("card");
    assert_eq!(card.ranges.len(), 2);
    assert_eq!(
        [
            range_text(source, card.ranges[0]),
            range_text(source, card.ranges[1])
        ],
        ["sw-card", "sw-card"]
    );
    // The leading astral rune occupies two UTF-16 units, not four UTF-8 bytes.
    assert_eq!(
        card.ranges[0].start,
        protocol::Position {
            line: 0,
            character: 4
        }
    );
    assert_eq!(card.word_pattern, ADMIN_COMPONENT_TAG_WORD_PATTERN);

    let button = linked_at(source, "sw-button", 4).expect("button");
    assert_eq!(button.ranges.len(), 2);
    assert_eq!(
        [
            range_text(source, button.ranges[0]),
            range_text(source, button.ranges[1])
        ],
        ["sw-button", "sw-button"]
    );
}

#[test]
fn works_from_closing_name_and_word_boundary() {
    let source = "<sw-card>Content</sw-card>";
    let result = linked_at(source, "</sw-card", "</sw-card".len()).expect("ranges");
    assert_eq!(result.ranges.len(), 2);
    assert_eq!(
        [
            range_text(source, result.ranges[0]),
            range_text(source, result.ranges[1])
        ],
        ["sw-card", "sw-card"]
    );
}

#[test]
fn is_conservative_outside_paired_vue_tags() {
    for (name, source, needle) in [
        ("native HTML", "<div></div>", "div"),
        ("self closing", "<sw-card />", "sw-card"),
        ("missing close", "<sw-card>", "sw-card"),
    ] {
        assert!(linked_at(source, needle, 1).is_none(), "{name}");
    }

    let dynamic = linked_at("<component :is=\"name\"></component>", "component", 2)
        .expect("dynamic component");
    assert_eq!(dynamic.ranges.len(), 2);
}
