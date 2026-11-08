use super::*;
use crate::twig::TwigIncludeParameterCompletionProvider;

fn include_items(
    provider: &TwigIncludeParameterCompletionProvider,
    source: &str,
) -> Vec<protocol::CompletionItem> {
    complete(
        provider,
        &twig_completion_at(
            "file:///project/templates/page.html.twig",
            source,
            source.len(),
        ),
    )
}

#[test]
fn twig_include_parameter_completion_across_supported_forms() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/templates/card.html.twig",
        "{% props title, subtitle = 'Fallback' %}\n{{ product.name }}",
    );
    let provider = TwigIncludeParameterCompletionProvider::new(Some(twig), None);
    for source in [
        "{{ include('card.html.twig', {ti",
        "{% include 'card.html.twig' with {ti",
        "{% embed 'card.html.twig' with {ti",
    ] {
        let items = include_items(&provider, source);
        require_completion(&items, "title");
        require_completion(&items, "subtitle");
        require_completion(&items, "product");
    }
}

#[test]
fn twig_include_parameter_completion_excludes_already_provided_keys() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/templates/card.html.twig",
        "{{ title }} {{ subtitle }}",
    );
    let provider = TwigIncludeParameterCompletionProvider::new(Some(twig), None);
    let items = include_items(
        &provider,
        "{{ include('card.html.twig', {'title': value, su",
    );
    require_completion(&items, "subtitle");
    assert!(!labels(&items).contains(&"title".to_string()));
}

#[test]
fn twig_include_parameter_completion_uses_inherited_contract() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/templates/base.html.twig",
        "{{ baseValue }}",
    );
    index(
        twig.as_ref(),
        "/project/templates/card.html.twig",
        "{% extends 'base.html.twig' %}\n{{ ownValue }}",
    );
    let provider = TwigIncludeParameterCompletionProvider::new(Some(twig), None);
    let items = include_items(&provider, "{% include 'card.html.twig' with {ba");
    require_completion(&items, "baseValue");
    require_completion(&items, "ownValue");
}

#[test]
fn twig_include_parameter_completion_carries_php_type() {
    let (twig_dir, php_dir) = (tmp(), tmp());
    let twig = twig_index(&twig_dir);
    index(
        twig.as_ref(),
        "/project/templates/card.html.twig",
        "{{ product.name }}",
    );
    let php = php_index(&php_dir);
    index(
        &php,
        "/project/src/Controller.php",
        r"<?php
namespace App;
class Product {}
class Controller {
    public function card(Product $product) {
        return $this->render('card.html.twig', ['product' => $product]);
    }
}",
    );
    let provider = TwigIncludeParameterCompletionProvider::new(Some(twig), Some(php));
    let items = include_items(&provider, "{{ include('card.html.twig', {pro");
    assert_eq!(require_completion(&items, "product").detail, "App\\Product");
}

#[test]
fn twig_include_parameter_completion_uses_unsaved_target_overlay() {
    let root = tmp();
    let twig = Arc::new(TwigIndexer::new(root.path().join(".cache"), None).unwrap());
    let path = p(&root.path().join("templates/recursive.html.twig"));
    index(twig.as_ref(), &path, "{{ stale }}");
    let source = "{{ fresh }}\n{{ include('recursive.html.twig', {fr";
    let document = Arc::new(TextDocument::new(format!("file://{path}"), source, 1));
    let offset = source.len() as u32;
    let items = complete(
        &TwigIncludeParameterCompletionProvider::new(Some(twig), None),
        &request_with_node(document, offset, offset - 1),
    );
    require_completion(&items, "fresh");
    assert!(!labels(&items).contains(&"stale".to_string()));
}
