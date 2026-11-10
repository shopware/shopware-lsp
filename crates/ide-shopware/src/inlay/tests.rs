use super::*;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, TextDocument};
use swlsp_lsp_protocol::{self as protocol};
use swlsp_snippet::SnippetIndexer;

fn request(document: TextDocument) -> InlayHintRequest {
    let mut params = protocol::InlayHintParams::default();
    params.text_document.uri = document.uri.clone();
    let (line, character) = document
        .line_index()
        .position_utf16(document.source().len() as u32);
    params.range.end = protocol::Position { line, character };
    InlayHintRequest {
        params,
        document: Arc::new(document),
    }
}

fn hints(provider: &dyn InlayHintProvider, request: &InlayHintRequest) -> Vec<protocol::InlayHint> {
    provider
        .get_inlay_hints(&Context::background(), request)
        .unwrap()
}

fn parts(hint: &protocol::InlayHint) -> Vec<protocol::InlayHintLabelPart> {
    match &hint.label {
        protocol::InlayHintLabel::Parts(parts) => parts.clone(),
        protocol::InlayHintLabel::String(_) => panic!("label parts expected"),
    }
}

// -- snippets -----------------------------------------------------------------

#[test]
fn snippet_preview_inlay_hints_prefer_english_and_navigate() {
    let dir = tempfile::tempdir().unwrap();
    let index = SnippetIndexer::new(dir.path(), None).unwrap();
    for (path, source) in [
        (
            "/project/src/Resources/snippet/de-DE/messages.json",
            r#"{"checkout":{"finish":"Bestellung abschliessen"}}"#,
        ),
        (
            "/project/src/Resources/snippet/en-GB/messages.json",
            r#"{"checkout":{"finish":"Complete order"}}"#,
        ),
        (
            "/project/src/Resources/app/administration/src/module/snippet/en-GB.json",
            r#"{"admin":{"title":"Administration title"}}"#,
        ),
    ] {
        index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let provider = SnippetPreviewProvider::new(Arc::new(index));
    for (name, uri, source) in [
        (
            "twig frontend",
            "file:///project/templates/checkout.html.twig",
            "{{ 'checkout.finish'|trans }}",
        ),
        (
            "twig admin",
            "file:///project/src/Resources/app/administration/card.html.twig",
            "{{ $tc('admin.title') }}",
        ),
        (
            "twig admin bound attribute",
            "file:///project/src/Resources/app/administration/card.html.twig",
            "<mt-card :title=\"$t('admin.title')\" />",
        ),
        (
            "javascript admin",
            "file:///project/src/Resources/app/administration/card.js",
            "this.$tc('admin.title');",
        ),
        (
            "javascript injected translator",
            "file:///project/src/Resources/app/administration/service.ts",
            "translator.$t('admin.title');",
        ),
        (
            "javascript snippet service",
            "file:///project/src/Resources/app/administration/service.js",
            "Shopware.Snippet.tc('admin.title');",
        ),
        (
            "javascript module metadata",
            "file:///project/src/Resources/app/administration/module.js",
            "Module.register('demo', { title: 'admin.title' });",
        ),
    ] {
        let result = hints(&provider, &request(TextDocument::new(uri, source, 1)));
        assert_eq!(result.len(), 1, "{name}");
        let parts = parts(&result[0]);
        assert_eq!(parts.len(), 1, "{name}");
        let location = parts[0].location.as_ref().expect(name);
        if name == "twig frontend" {
            assert_eq!(parts[0].value, "→ Complete order");
            assert!(location.uri.ends_with("/messages.json"));
        } else {
            assert_eq!(parts[0].value, "→ Administration title", "{name}");
        }
        assert!(result[0].padding_left);
    }
}
