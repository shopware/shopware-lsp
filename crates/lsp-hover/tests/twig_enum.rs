mod common;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::TwigEnumHoverProvider;

#[test]
fn twig_enum_hover_shows_enum_cases() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        cache.path(),
        &[(
            &p(&root.path().join("src/Status.php")),
            r"<?php
namespace App;
enum Status { case Open; case Closed; }",
        )],
    );
    let source = r"{{ enum('App\\Status') }}";
    let document = TextDocument::new(
        uri(&root.path().join("templates/status.html.twig")),
        source,
        1,
    );
    let request = request_at(document, source.find("Status").unwrap() + 2);
    let result = hover(&TwigEnumHoverProvider::new(Some(php)), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("PHP enum"));
    assert!(value.contains("App\\Status"));
    assert!(value.contains("Closed"));
    assert!(value.contains("Open"));
}
