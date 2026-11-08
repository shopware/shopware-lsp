mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::{HoverRequest, SyntaxContext, TextDocument};
use swlsp_lsp_hover::TwigVersioningHoverProvider;
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_twig::{TwigIndexer, VersioningService};

#[test]
fn twig_versioning_hover_provider_nil_indexer_no_panic() {
    let provider = TwigVersioningHoverProvider::new(None);
    let document = Arc::new(TextDocument::new(
        "file:///tmp/foo.twig",
        "{% block foo %}{% endblock %}",
        1,
    ));
    let root = document.root().unwrap();
    let mut params = protocol::HoverParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position {
        line: 0,
        character: 10,
    };
    let request = HoverRequest {
        params,
        syntax: SyntaxContext {
            language: None,
            document: Some(document),
            root: Some(root.clone()),
            token: None,
            node: Some(root),
        },
    };
    assert!(hover(&provider, &request).is_none());
}

#[test]
fn twig_versioning_hover_shows_third_party_upstream_and_status() {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(root.path().join("cache"), None).unwrap());
    let theme_root = root.path().join("custom/plugins/Theme");
    write(&theme_root.join("composer.json"), r#"{"version":"2.5.0"}"#);
    let upstream_path = p(&theme_root.join("src/Resources/views/storefront/card.html.twig"));
    index_file(
        index.as_ref(),
        &upstream_path,
        "{% block card %}upstream{% endblock %}",
    );
    let hashes = index.get_twig_block_hashes("card").unwrap();
    assert_eq!(hashes.len(), 1);
    let current_path = root
        .path()
        .join("custom/plugins/Plugin/src/Resources/views/storefront/card.html.twig");
    let source = format!(
        "{{% sw_extends '@Theme/storefront/card.html.twig' %}}
{{# shopware-block: {}@2.5.0 #}}
{{% block card %}}local{{% endblock %}}",
        hashes[0].hash
    );
    let document = Arc::new(TextDocument::new(uri(&current_path), &source, 1));
    let offset = source.rfind("{% block card").unwrap() + "{% block ".len() + 1;
    let tree_root = document.root().unwrap();
    let mut params = protocol::HoverParams::default();
    params.text_document.uri = document.uri.clone();
    let request = HoverRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document.clone()),
            root: Some(tree_root.clone()),
            token: tree_root.token_at(TextSize::from(offset as u32)),
            node: tree_root.node_at(TextSize::from(offset as u32)),
        },
    };
    let versioning = Arc::new(VersioningService::new(&p(root.path()), Some(index), ""));
    let result = hover(
        &TwigVersioningHoverProvider::new(Some(versioning)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains(&upstream_path));
    assert!(value.contains("2.5.0"));
    assert!(value.contains("up to date"));
}
