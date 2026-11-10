use swlsp_lsp_protocol::CompletionItemKind;

use super::types::PAGE;
use super::*;
use crate::completion::twig::TwigCompletionProvider;

#[test]
fn twig_see_completes_php_classes_methods_and_templates() {
    let (root, php_dir, twig_dir) = (tmp(), tmp(), tmp());
    let root_path = p(root.path());
    let php = php_index(&php_dir);
    index(
        &php,
        &p(&root.path().join("src/ProductController.php")),
        r"<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
}",
    );
    let twig = twig_index(&twig_dir);
    index(
        twig.as_ref(),
        &p(&root.path().join("templates/product/show.html.twig")),
        "product",
    );
    let provider = TwigCompletionProvider::new(root_path, Some(twig), None, Some(php));

    let items = caret_items(&provider, PAGE, r"{# @see App\Controller\Prod<caret> #}");
    let class = require_completion(&items, "App\\Controller\\ProductController");
    assert_eq!(class.kind, Some(CompletionItemKind::CLASS));

    let items = caret_items(
        &provider,
        PAGE,
        r"{# @see App\Controller\ProductController::sh<caret> #}",
    );
    assert_eq!(
        require_completion(&items, "show").kind,
        Some(CompletionItemKind::METHOD)
    );

    let items = caret_items(&provider, PAGE, "{# @see product/sh<caret> #}");
    assert_eq!(
        require_completion(&items, "product/show.html.twig").kind,
        Some(CompletionItemKind::FILE)
    );
}
