use std::sync::Arc;

use crate::common::*;
use swlsp_ide_twig::hover::TwigConstantHoverProvider;
use swlsp_lsp::TextDocument;
use swlsp_twig::TwigIndexer;

#[test]
fn twig_constant_hover_shows_class_constant_metadata() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        &cache.path().join("php"),
        &[(
            &p(&root.path().join("src/CardSuite.php")),
            r"<?php
namespace App;
class CardSuite {
    /**
     * Card suit code.
     * @deprecated Use HEARTS.
     */
    public const CLUBS = 'clubs';
}",
        )],
    );
    let twig = Arc::new(TwigIndexer::new(cache.path().join("twig"), None).unwrap());
    twig.set_dependencies(Some(php.clone()), None);
    let source = r"{# @var suite \App\CardSuite #}
{{ constant('CLUBS', suite) }}";
    let document = TextDocument::new(
        uri(&root.path().join("templates/cards.html.twig")),
        source,
        1,
    );
    let request = request_at(document, source.rfind("CLUBS").unwrap() + 2);
    let result = hover(
        &TwigConstantHoverProvider::new(Some(php), Some(twig)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("PHP class constant"));
    assert!(value.contains("App\\CardSuite::CLUBS"));
    assert!(value.contains("Card suit code"));
    assert!(value.contains("Deprecated"));
}
