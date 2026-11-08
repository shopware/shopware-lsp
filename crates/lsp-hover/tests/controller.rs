mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::{HoverRequest, SyntaxContext, TextDocument};
use swlsp_lsp_hover::ControllerHoverProvider;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;

#[test]
fn controller_hover_shows_resolved_twig_controller_signature() {
    let cache = tempfile::tempdir().unwrap();
    let classes = tempfile::tempdir().unwrap();
    let php = php_index(
        cache.path(),
        &[(
            &p(&classes.path().join("NavController.php")),
            r"<?php
namespace App\Controller;
class NavController {
    /** Builds the navigation. */
    public function menu(string $section): array {}
}",
        )],
    );
    let document = Arc::new(TextDocument::new(
        "file:///project/navigation.html.twig",
        r"{{ controller('App\\Controller\\NavController::menu') }}",
        1,
    ));
    let root = document.root().unwrap();
    let literals = twigquery::nodes(&root, &[TWIG_LITERAL_STRING]);
    assert_eq!(literals.len(), 1);
    let mut params = protocol::HoverParams::default();
    params.text_document.uri = document.uri.clone();
    let request = HoverRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token: None,
            node: Some(literals[0].clone()),
        },
    };
    let result = hover(&ControllerHoverProvider::new(None, Some(php)), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("App\\Controller\\NavController::menu"));
    assert!(value.contains("$section"));
    assert!(value.contains("Builds the navigation."));
}
