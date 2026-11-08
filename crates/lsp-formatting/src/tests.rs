use std::sync::Arc;

use swlsp_lsp::{Context, DocumentFormattingProvider, DocumentFormattingRequest, TextDocument};
use swlsp_lsp_protocol as protocol;

use super::TwigProvider;

fn format(uri: &str, source: &str, options: protocol::FormattingOptions) -> Option<String> {
    let request = DocumentFormattingRequest {
        params: protocol::DocumentFormattingParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            options,
        },
        document: Arc::new(TextDocument::new(uri, source, 1)),
    };
    TwigProvider::new()
        .format_document(&Context::background(), &request)
        .expect("format")
}

#[test]
fn twig_provider_selects_administration_and_storefront_indentation() {
    let source = "{% block content %}<div>content</div>{% endblock %}";
    let cases = [
        (
            "file:///workspace/src/Resources/app/administration/src/page.html.twig",
            "{% block content %}\n<div>content</div>\n{% endblock %}",
        ),
        (
            "file:///workspace/src/Resources/views/storefront/page.html.twig",
            "{% block content %}\n  <div>content</div>\n{% endblock %}",
        ),
    ];
    for (uri, want) in cases {
        let formatted = format(
            uri,
            source,
            protocol::FormattingOptions {
                tab_size: 2,
                insert_spaces: true,
                ..Default::default()
            },
        );
        assert_eq!(formatted.as_deref(), Some(want), "{uri}");
    }
}

#[test]
fn twig_provider_ignores_unsupported_documents() {
    let formatted = format(
        "file:///workspace/file.php",
        "<?php",
        protocol::FormattingOptions::default(),
    );
    assert_eq!(formatted, None);
}

#[test]
fn twig_provider_reports_cancellation() {
    let uri = "file:///workspace/page.html.twig";
    let request = DocumentFormattingRequest {
        params: protocol::DocumentFormattingParams::default(),
        document: Arc::new(TextDocument::new(uri, "{{ x }}", 1)),
    };
    let ctx = Context::background();
    ctx.token().cancel();
    let error = TwigProvider::new()
        .format_document(&ctx, &request)
        .expect_err("cancelled");
    assert!(swlsp_lsp::is_cancellation(error.as_ref()));
}
