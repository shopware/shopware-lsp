use std::path::Path;
use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, DocumentHighlightProvider, DocumentHighlightRequest, SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

use crate::highlight::AdminDocumentHighlightProvider;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn index(index: &AdminComponentIndexer, path: &Path, source: &str) {
    index
        .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
        .unwrap();
}

/// Highlights at one byte after the first `needle`, with their texts.
fn highlight_texts(
    provider: &AdminDocumentHighlightProvider,
    path: &Path,
    source: &str,
    needle: &str,
) -> Vec<String> {
    let document = Arc::new(TextDocument::new(file_uri(&p(path)), source, 2));
    let offset = (source.find(needle).unwrap() + 1) as u32;
    let lines = document.line_index();
    let (line, character) = lines.position_utf16(offset);
    let mut params = protocol::DocumentHighlightParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    let root = document.root();
    let node = root
        .as_ref()
        .and_then(|root| root.node_at(TextSize::from(offset)));
    let highlights = provider
        .get_document_highlights(
            &Context::background(),
            &DocumentHighlightRequest {
                params,
                syntax: SyntaxContext {
                    document: Some(document.clone()),
                    language: document.language,
                    root,
                    token: None,
                    node,
                },
            },
        )
        .unwrap();
    highlights
        .iter()
        .map(|highlight| {
            assert_eq!(highlight.kind, Some(protocol::DocumentHighlightKind::TEXT));
            let start =
                lines.offset_utf16(highlight.range.start.line, highlight.range.start.character);
            let end = lines.offset_utf16(highlight.range.end.line, highlight.range.end.character);
            source[start as usize..end as usize].to_owned()
        })
        .collect()
}

fn admin_index(root: &Path) -> Arc<AdminComponentIndexer> {
    Arc::new(AdminComponentIndexer::new(&p(&root.join("cache")), None).unwrap())
}

#[test]
fn admin_document_highlights_use_live_twig_prop_occurrences() {
    let root = tempfile::tempdir().unwrap();
    let admin = admin_index(root.path());
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let definition = admin_root.join("component/sw-card/index.ts");
    let template = admin_root.join("component/sw-card/sw-card.html.twig");
    let consumer = admin_root.join("page/consumer.html.twig");
    for (path, source) in [
        (
            &definition,
            "import template from './sw-card.html.twig';
Component.register('sw-card', {
    template,
    props: { title: { type: String } },
    methods: { save(id: string): void {} },
});",
        ),
        (&template, "{{ save(product.id) }}"),
        (&consumer, r#"<sw-card :title="first" />"#),
    ] {
        index(&admin, path, source);
    }
    let provider = AdminDocumentHighlightProvider::new(Some(admin));
    assert_eq!(
        highlight_texts(
            &provider,
            &consumer,
            r#"<sw-card :title="first" /><sw-card :title="second" />"#,
            r#":title="first""#,
        ),
        ["title", "title"]
    );
    assert_eq!(
        highlight_texts(
            &provider,
            &template,
            r#"{{ save(product.id) }}<button @click="save(other.id)" />"#,
            "save(product.id)",
        ),
        ["save", "save"]
    );
}

#[test]
fn admin_document_highlights_replace_stale_javascript_occurrences() {
    let root = tempfile::tempdir().unwrap();
    let admin = admin_index(root.path());
    let path = root
        .path()
        .join("src/Administration/Resources/app/administration/src/component/sw-card/index.ts");
    index(&admin, &path, "Component.register('sw-card', {});");
    let live = "Component.register('sw-card', {});\n\nComponent.override('sw-card', {});";
    assert_eq!(
        highlight_texts(
            &AdminDocumentHighlightProvider::new(Some(admin)),
            &path,
            live,
            "'sw-card'",
        ),
        ["sw-card", "sw-card"]
    );
}
