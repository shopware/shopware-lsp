//! Types that reach a template from the templates around it.

use std::path::Path;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_EXPRESSION, TWIG_VAR};
use swlsp_php::PhpIndex;

use super::inference::app_index;
use crate::macros::direct_child;
use crate::*;

/// Writes and indexes the templates of a project; returns its indexes.
fn project(root: &Path, templates: &[(&str, &str)]) -> (PhpIndex, TwigIndexer) {
    let php = app_index(&root.join("cache"));
    let twig = TwigIndexer::new(root.join("cache"), None).unwrap();
    twig.set_dependencies(Some(php.clone()), None);
    for (name, source) in templates {
        let path = root.join("templates").join(name);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, source).unwrap();
        twig.index(&ParsedFile::new(
            path.to_str().unwrap(),
            source.as_bytes().to_vec(),
        ))
        .unwrap();
    }
    (php, twig)
}

/// The types of the `{{ … }}` expressions of the project's template `name`.
fn printed(root: &Path, php: &PhpIndex, twig: &TwigIndexer, name: &str) -> Vec<String> {
    let path = root.join("templates").join(name);
    let source = std::fs::read_to_string(&path).unwrap();
    let tree = swlsp_parser_twig::parse(&source).root();
    let resolver = PhpAccessResolver::new(Some(php), Some(twig)).for_document(&tree);
    twigquery::nodes(&tree, &[TWIG_VAR])
        .iter()
        .map(|print| {
            let expression = direct_child(print, TWIG_EXPRESSION);
            resolver
                .expression_type(path.to_str().unwrap(), &tree, expression.as_ref())
                .to_string()
        })
        .collect()
}

#[test]
fn include_passes_types_to_the_included_template() {
    let directory = tempfile::tempdir().unwrap();
    let (php, twig) = project(
        directory.path(),
        &[
            (
                "page.html.twig",
                r"{# @var product \App\Product #}
{% set maker = product.manufacturer %}
{% include 'card.html.twig' with {item: product, label: product.name} %}
{% include 'strict.html.twig' with {item: product} only %}
{{ include('call.html.twig', {item: product.cover}) }}
{% for image in product.images %}{% include 'image.html.twig' %}{% endfor %}
",
            ),
            (
                "other.html.twig",
                r"{# @var product \App\Product #}
{% include 'card.html.twig' with {item: product.manufacturer, label: 'x'} %}
",
            ),
            (
                "card.html.twig",
                "{{ item }}{{ label }}{{ maker }}{{ product }}",
            ),
            ("strict.html.twig", "{{ item }}{{ maker }}"),
            ("call.html.twig", "{{ item }}{{ maker }}"),
            ("image.html.twig", "{{ image }}{{ maker }}"),
        ],
    );
    let printed = |name: &str| printed(directory.path(), &php, &twig, name);
    // Two templates include the card: the union of what they pass. Only one
    // assigns `maker`, so nothing is known about it.
    assert_eq!(
        printed("card.html.twig"),
        [
            r"App\Manufacturer|App\Product|null",
            "string",
            "unknown",
            r"App\Product"
        ]
    );
    // `only` cuts the including context.
    assert_eq!(printed("strict.html.twig"), [r"App\Product", "unknown"]);
    assert_eq!(
        printed("call.html.twig"),
        [r"App\Media|null", r"App\Manufacturer|null"]
    );
    assert_eq!(
        printed("image.html.twig"),
        [r"App\Media", r"App\Manufacturer|null"]
    );
}

#[test]
fn extends_shares_context_and_assignments() {
    let directory = tempfile::tempdir().unwrap();
    let (php, twig) = project(
        directory.path(),
        &[
            (
                "base.html.twig",
                r"{% set shared = 1 %}
{% block body %}{{ product }}{{ title }}{% endblock %}
",
            ),
            (
                "page.html.twig",
                r"{% extends 'base.html.twig' %}
{# @var product \App\Product #}
{% set title = product.name %}
{% block body %}{{ shared }}{{ title }}{% endblock %}
",
            ),
        ],
    );
    let printed = |name: &str| printed(directory.path(), &php, &twig, name);
    assert_eq!(printed("page.html.twig"), ["int", "string"]);
    assert_eq!(printed("base.html.twig"), [r"App\Product", "string"]);
}

#[test]
fn embed_and_with_bind_their_hash() {
    let directory = tempfile::tempdir().unwrap();
    let (php, twig) = project(
        directory.path(),
        &[(
            "page.html.twig",
            r"{# @var product \App\Product #}
{% embed 'card.html.twig' with {item: product.cover} %}{% block a %}{{ item }}{{ product }}{% endblock %}{% endembed %}
{% embed 'card.html.twig' with {item: product.cover} only %}{% block a %}{{ item }}{{ product }}{% endblock %}{% endembed %}
{% with {maker: product.manufacturer} %}{{ maker }}{{ product }}{% endwith %}
{% with {maker: product.manufacturer} only %}{{ maker }}{{ product }}{% endwith %}
",
        )],
    );
    assert_eq!(
        printed(directory.path(), &php, &twig, "page.html.twig"),
        [
            r"App\Media|null",
            r"App\Product",
            r"App\Media|null",
            "unknown",
            r"App\Manufacturer|null",
            r"App\Product",
            r"App\Manufacturer|null",
            "unknown",
        ]
    );
}

#[test]
fn macro_parameters_take_the_types_of_their_arguments() {
    let directory = tempfile::tempdir().unwrap();
    let (php, twig) = project(
        directory.path(),
        &[
            (
                "forms.html.twig",
                r"{# @var product \App\Product #}
{% macro field(item, label = 'x', extra) %}{{ item }}{{ label }}{{ extra }}{% endmacro %}
{% macro unused(item) %}{{ item }}{% endmacro %}
{{ _self.field(product) }}
",
            ),
            (
                "page.html.twig",
                r"{# @var product \App\Product #}
{% import 'forms.html.twig' as forms %}
{% from 'forms.html.twig' import field as input %}
{{ forms.field(product.manufacturer, 1) }}
{{ input(product, label=product.cover, extra=product.name) }}
",
            ),
        ],
    );
    let types = printed(directory.path(), &php, &twig, "forms.html.twig");
    assert_eq!(
        types[..4],
        [
            r"App\Manufacturer|App\Product|null",
            r"App\Media|int|null|string",
            "null|string",
            "unknown",
        ]
    );
}
