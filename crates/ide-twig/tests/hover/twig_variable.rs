use std::sync::Arc;

use crate::common::*;
use swlsp_ide_twig::hover::TwigHoverProvider;
use swlsp_lsp::TextDocument;
use swlsp_twig::TwigIndexer;

fn twig_hover(provider: &TwigHoverProvider, uri: &str, source: &str, offset: usize) -> String {
    let request = request_at(TextDocument::new(uri, source, 1), offset);
    hover(provider, &request).expect("hover").contents.value
}

#[test]
fn twig_controller_variable_hover() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        cache.path(),
        &[(
            "/project/src/ProductController.php",
            r"<?php
namespace App;
class Product {}
class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}",
        )],
    );
    let source = "{{ product }}";
    let value = twig_hover(
        &TwigHoverProvider::new("/project", None, Some(php), None),
        "file:///project/templates/product/show.html.twig",
        source,
        source.find("product").unwrap() + 1,
    );
    assert!(value.contains("Twig variable"));
    assert!(value.contains("App\\Product"));
    assert!(value.contains("src/ProductController.php"));
}

#[test]
fn twig_types_variable_hover_includes_documentation_and_optionality() {
    let source = r"{% types {
    ## User shown in the profile card.
    user?: 'App\\User',
} %}
{{ user }}";
    let value = twig_hover(
        &TwigHoverProvider::new("/project", None, None, None),
        "file:///project/templates/profile.html.twig",
        source,
        source.rfind("user").unwrap() + 1,
    );
    assert!(value.contains("Declared type: `App\\User`"));
    assert!(value.contains("Optional in the Twig `types` tag"));
    assert!(value.contains("User shown in the profile card."));
}

#[test]
fn twig_construct_hover_shows_documentation_comment() {
    let source = "{## Main page content. ##}
{% block content %}{% endblock %}";
    let value = twig_hover(
        &TwigHoverProvider::new("/project", None, None, None),
        "file:///project/templates/profile.html.twig",
        source,
        source.find("block").unwrap() + 1,
    );
    assert_eq!(value, "Main page content.");
}

#[test]
fn twig_output_documentation_enriches_variable_hover() {
    let source = r"{# @var user App\User #}
{## User rendered by this expression. #}
{{ user }}";
    let value = twig_hover(
        &TwigHoverProvider::new("/project", None, None, None),
        "file:///project/templates/profile.html.twig",
        source,
        source.rfind("user").unwrap() + 1,
    );
    assert!(value.contains("Declared type: `App\\User`"));
    assert!(value.contains("User rendered by this expression."));
}

#[test]
fn twig_global_variable_hover() {
    let root = tempfile::tempdir().unwrap();
    let twig = Arc::new(TwigIndexer::new(root.path().join(".cache"), None).unwrap());
    index_file(
        twig.as_ref(),
        &p(&root.path().join("config/packages/twig.yaml")),
        "twig:
  globals:
    site_name: 'Store'
",
    );
    let source = "{{ site_name }}";
    let value = twig_hover(
        &TwigHoverProvider::new(p(root.path()), None, None, Some(twig)),
        "file:///project/templates/page.html.twig",
        source,
        source.find("site_name").unwrap() + 1,
    );
    assert!(value.contains("Twig global"));
    assert!(value.contains("PHP type: `string`"));
    assert!(value.contains("Twig configuration"));
    assert!(value.contains("config/packages/twig.yaml"));
}
