use std::sync::Arc;

use crate::common::*;
use swlsp_ide_twig::hover::TwigRenderBlockHoverProvider;
use swlsp_lsp::TextDocument;
use swlsp_twig::TwigIndexer;

#[test]
fn twig_render_block_hover_shows_template_and_declarations() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let source = r"<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->renderBlock('base.html.twig', 'content');
    }
}";
    let path = root.path().join("src/Controller.php");
    let php = php_index(
        &cache.path().join("php"),
        &[
            (
                &p(&root.path().join("vendor/AbstractController.php")),
                r"<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}",
            ),
            (&p(&path), source),
        ],
    );
    let twig = Arc::new(TwigIndexer::new(cache.path().join("twig"), None).unwrap());
    index_file(
        twig.as_ref(),
        &p(&root.path().join("templates/base.html.twig")),
        "{## Main page content. ##}
{% block content %}{% endblock %}",
    );
    let document = TextDocument::new(uri(&path), source, 1);
    let request = request_at(document, source.find("'content'").unwrap() + 2);
    let result = hover(
        &TwigRenderBlockHoverProvider::new(p(root.path()), Some(twig), Some(php)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Twig block"));
    assert!(value.contains("`content`"));
    assert!(value.contains("`base.html.twig`"));
    assert!(value.contains("templates/base.html.twig:2"));
    assert!(value.contains("Main page content."));
    let range = result.range.expect("range");
    assert_eq!(
        (range.end.character - range.start.character) as usize,
        "content".len()
    );
}
