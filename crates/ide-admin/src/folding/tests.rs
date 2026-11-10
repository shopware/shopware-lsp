use super::*;
use std::sync::Arc;
use swlsp_lsp::{Context, FoldingRangeProvider, FoldingRangeRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::{
    FOLDING_RANGE_KIND_COMMENT as COMMENT, FOLDING_RANGE_KIND_IMPORTS as IMPORTS,
};
use swlsp_uriutil::file_uri;

fn request(uri: &str, source: &str, version: i32) -> FoldingRangeRequest {
    FoldingRangeRequest {
        params: protocol::FoldingRangeParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            ..Default::default()
        },
        document: Arc::new(TextDocument::new(uri, source, version)),
    }
}

fn fold(start_line: u32, end_line: u32, kind: &str) -> protocol::FoldingRange {
    protocol::FoldingRange {
        start_line,
        end_line,
        kind: kind.to_owned(),
        ..Default::default()
    }
}

fn admin_folds(path: &str, source: &str) -> Vec<protocol::FoldingRange> {
    AdminFoldingProvider::new()
        .get_folding_ranges(&Context::background(), &request(&file_uri(path), source, 1))
        .expect("folding ranges")
}

#[track_caller]
fn assert_contains(ranges: &[protocol::FoldingRange], start: u32, end: u32, kind: &str) {
    let expected = fold(start, end, kind);
    assert!(
        ranges.contains(&expected),
        "expected {start}..{end} kind {kind:?} in {ranges:#?}"
    );
}

#[test]
fn admin_javascript_folding_covers_imports_component_sections_and_comments() {
    let source = "import template from './sw-card.html.twig';
import './sw-card.scss';

Component.register('sw-card', {
    props: {
        title: String,
    },
    methods: {
        save() {
            const options = {
                force: true,
            };
        },
    },
});
/* component
 * details */";
    let ranges = admin_folds("/project/sw-card/index.ts", source);
    assert_contains(&ranges, 0, 1, IMPORTS);
    assert_contains(&ranges, 3, 13, "");
    assert_contains(&ranges, 4, 5, "");
    assert_contains(&ranges, 7, 12, "");
    assert_contains(&ranges, 8, 11, "");
    assert_contains(&ranges, 9, 10, "");
    assert_contains(&ranges, 15, 16, COMMENT);
}

#[test]
fn admin_twig_folding_covers_blocks_markup_and_self_closing_attributes() {
    let source = "{# component
   docs #}
{% block sw_card %}
<sw-card
    :title=\"title\"
>
    <div>
        Content
    </div>
</sw-card>
{% endblock %}
<sw-button
    :disabled=\"true\"
/>";
    let ranges = admin_folds("/project/sw-card/sw-card.html.twig", source);
    assert_contains(&ranges, 0, 1, COMMENT);
    assert_contains(&ranges, 2, 9, "");
    assert_contains(&ranges, 3, 8, "");
    assert_contains(&ranges, 6, 7, "");
    assert_contains(&ranges, 11, 13, "");
}

#[test]
fn admin_folding_uses_the_open_document_snapshot() {
    let ranges = AdminFoldingProvider::new()
        .get_folding_ranges(
            &Context::background(),
            &request(
                &file_uri("/project/live.html.twig"),
                "<sw-card>\n  live\n</sw-card>",
                7,
            ),
        )
        .expect("folding ranges");
    assert_eq!(ranges, vec![fold(0, 1, "")]);
}
