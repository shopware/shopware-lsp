//! Snippet copy and `SnippetCodeActionProvider` tests.

use crate::test_support::{request_at, request_for_range};
use std::sync::Arc;
use swlsp_lsp::TextDocument;

use super::*;

/// Empty range and node at the
/// needle offset.
fn needle_request(uri: &str, source: &str, needle: &str) -> CodeActionRequest {
    let offset = source.find(needle).unwrap_or(0) as u32;
    let mut request = request_at(uri, source, offset, offset);
    let root = request.root.clone().unwrap();
    request.syntax.node = swlsp_syntax::SyntaxNodeExt::node_at(&root, offset.into());
    request
}

#[test]
fn snippet_copy_builds_nested_key() {
    let request = needle_request(
        "file:///project/src/Resources/snippet/en-GB/messages.json",
        r#"{"checkout":{"finish":{"button":"Complete order"}}}"#,
        "Complete order",
    );
    let actions =
        SnippetCopyCodeActionProvider::new().get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(
        actions[0].title,
        "Shopware: Copy snippet usage for 'checkout.finish.button'"
    );
    let command = actions[0].command.as_ref().unwrap();
    assert_eq!(command.command, COPY_SNIPPET_USAGE_ACTION);
    assert_eq!(command.arguments[0], "checkout.finish.button");
}

#[test]
fn snippet_copy_ignores_non_snippet_json() {
    let request = needle_request(
        "file:///project/composer.json",
        r#"{"name":"acme/plugin"}"#,
        "acme",
    );
    assert!(
        SnippetCopyCodeActionProvider::new()
            .get_code_actions(&Context::background(), &request)
            .is_empty()
    );
}

#[test]
fn snippet_actions_in_twig() {
    let provider = SnippetCodeActionProvider::new(None);
    let ctx = Context::background();
    let source = "<p>Hello world</p>";
    let start = source.find("Hello").unwrap() as u32;

    let insert = provider.get_code_actions(
        &ctx,
        &request_at("file:///project/views/page.html.twig", source, start, start),
    );
    assert_eq!(insert.len(), 1);
    assert_eq!(insert[0].title, "Insert Snippet");
    assert_eq!(
        insert[0].command.as_ref().unwrap().command,
        "shopware.insertSnippet"
    );

    let storefront = provider.get_code_actions(
        &ctx,
        &request_at(
            "file:///project/views/page.html.twig",
            source,
            start,
            start + 11,
        ),
    );
    assert_eq!(storefront.len(), 1);
    assert_eq!(storefront[0].title, "Create snippet from selection");
    let command = storefront[0].command.as_ref().unwrap();
    assert_eq!(command.command, "shopware.createSnippetFromSelection");
    assert_eq!(command.arguments[1], "Hello world");

    let admin_uri = "file:///project/Resources/app/administration/src/a.html.twig";
    let admin = provider.get_code_actions(&ctx, &request_at(admin_uri, source, start, start + 5));
    assert_eq!(admin.len(), 1);
    assert_eq!(admin[0].title, "Create admin snippet from selection");
    assert_eq!(admin[0].command.as_ref().unwrap().arguments[1], "Hello");

    let document = Arc::new(TextDocument::new("file:///project/a.php", "<?php", 1));
    assert!(
        provider
            .get_code_actions(
                &ctx,
                &request_for_range(document, protocol::Range::default())
            )
            .is_empty()
    );
}
