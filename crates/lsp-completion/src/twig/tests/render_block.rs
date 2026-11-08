use tempfile::TempDir;

use super::*;
use crate::twig::TwigRenderBlockCompletionProvider;

fn render_block_fixture() -> (Arc<TwigIndexer>, PhpIndex, TempDir, Vec<TempDir>) {
    let (root, php_dir, twig_dir) = (tmp(), tmp(), tmp());
    let php = php_index(&php_dir);
    index(
        &php,
        &p(&root.path().join("vendor/AbstractController.php")),
        r"<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}",
    );
    let twig = twig_index(&twig_dir);
    index(
        twig.as_ref(),
        &p(&root.path().join("templates/base.html.twig")),
        "{## Main page content. ##}\n{% block content %}{% endblock %}\n{% block sidebar %}{% endblock %}",
    );
    index(
        twig.as_ref(),
        &p(&root.path().join("templates/child.html.twig")),
        "{% extends 'base.html.twig' %}\n{% block child %}{% endblock %}",
    );
    (twig, php, root, vec![php_dir, twig_dir])
}

#[test]
fn twig_render_block_completion_includes_inherited_blocks_and_named_arguments() {
    let (twig, php, root, _dirs) = render_block_fixture();
    let source = r"<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->renderBlock('child.html.twig', '');
        $this->renderBlockView(block: '', view: 'child.html.twig');
    }
}";
    let path = p(&root.path().join("src/Controller.php"));
    index(&php, &path, source);
    let document = Arc::new(TextDocument::new(swlsp_uriutil::file_uri(&path), source, 1));
    let provider = TwigRenderBlockCompletionProvider::new(Some(twig), Some(php));
    let mut start = 0;
    for _ in 0..2 {
        let offset = start + source[start..].find("''").unwrap() + 1;
        let request = request_with_node(document.clone(), offset as u32, offset as u32);
        let items = complete(&provider, &request);
        let content = require_completion(&items, "content");
        assert!(documentation(&content).contains("Main page content."));
        require_completion(&items, "sidebar");
        require_completion(&items, "child");
        start = offset + 1;
    }
}

#[test]
fn twig_render_block_completion_requires_abstract_controller() {
    let (twig, php, root, _dirs) = render_block_fixture();
    let source = r"<?php
namespace App;
class Unrelated {
    public function page(): void {
        $this->renderBlock('child.html.twig', '');
    }
}";
    let path = p(&root.path().join("src/Unrelated.php"));
    let document = Arc::new(TextDocument::new(swlsp_uriutil::file_uri(&path), source, 1));
    let offset = (source.find("''").unwrap() + 1) as u32;
    let provider = TwigRenderBlockCompletionProvider::new(Some(twig), Some(php));
    assert!(complete(&provider, &request_with_node(document, offset, offset)).is_empty());
}
