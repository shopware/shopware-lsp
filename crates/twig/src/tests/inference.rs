//! Types of Twig expressions: assignments, filters, lookups and operators.

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_EXPRESSION, TWIG_VAR};
use swlsp_php::PhpIndex;

use super::indexer::{index_php, php_index};
use crate::macros::direct_child;
use crate::*;

const PHP: &str = r"<?php
namespace App;

const LIMIT = 10;
class Media { public const KIND = 'image'; public string $url; }
class Manufacturer { public string $name; public ?Media $media; }
class Product {
    public string $name;
    public ?Manufacturer $manufacturer;
    /** @var list<Media> */
    public array $images;
    /** @var array<string, Manufacturer> */
    public array $makers;
    public function getCover(): ?Media { return null; }
    /** @return array{title: string, media?: Media} */
    public function getMeta(): array { return []; }
}
";

pub(super) fn app_index(cache: &std::path::Path) -> PhpIndex {
    let php = php_index(cache);
    index_php(&php, "/project/src/Model.php", PHP);
    php
}

/// The types of the `{{ … }}` expressions of `template`, in order.
pub(super) fn printed_types(php: &PhpIndex, template: &str) -> Vec<String> {
    let root = swlsp_parser_twig::parse(template).root();
    let resolver = PhpAccessResolver::new(Some(php), None).for_document(&root);
    twigquery::nodes(&root, &[TWIG_VAR])
        .iter()
        .map(|print| {
            let expression = direct_child(print, TWIG_EXPRESSION);
            resolver
                .expression_type(
                    "/project/templates/page.html.twig",
                    &root,
                    expression.as_ref(),
                )
                .to_string()
        })
        .collect()
}

#[test]
fn set_assigns_the_type_of_its_value() {
    let cache = tempfile::tempdir().unwrap();
    let php = app_index(cache.path());
    let types = printed_types(
        &php,
        r"{# @var product \App\Product #}
{{ early }}
{% set early = product.name %}
{% set maker = product.manufacturer %}
{{ maker }}
{% set first, second = product.name, product.images %}
{{ second }}
{% set captured %}text{% endset %}
{{ captured }}
{% if product.name %}{% set branch = product.cover %}{{ branch }}{% else %}{{ branch }}{% endif %}
{{ branch }}
{% block body %}{% set inner = product %}{{ inner }}{{ maker }}{% endblock %}
{{ inner }}
{% set maker = maker.media %}
{{ maker }}
{% macro show(item) %}{% set local = 1 %}{{ local }}{{ item }}{{ maker }}{{ product }}{% endmacro %}
",
    );
    assert_eq!(
        types,
        [
            "unknown",
            r"App\Manufacturer|null",
            r"list<App\Media>",
            "string",
            r"App\Media|null",
            // Another branch never sees the assignment; after the `if` the
            // variable may still be unset, so nothing is known.
            "unknown",
            "unknown",
            r"App\Product",
            r"App\Manufacturer|null",
            // A block renders with a copy of the context.
            "unknown",
            r"App\Media|null",
            // A macro sees its own assignments and parameters, and what
            // the document declares; not the assignments around it.
            "int",
            "unknown",
            "unknown",
            r"App\Product",
        ]
    );
}

#[test]
fn filters_and_functions_keep_the_element_type() {
    let cache = tempfile::tempdir().unwrap();
    let php = app_index(cache.path());
    let types = printed_types(
        &php,
        r"{# @var product \App\Product #}
{{ product.images|first }}
{{ product.images|last.url }}
{{ product.makers|keys }}
{{ product.images|filter(image => image.url) }}
{{ product.makers|filter(maker => maker.name) }}
{{ product.images|map(image => image.url) }}
{{ product.makers|map((maker, key) => key) }}
{{ product.images|sort((left, right) => left.url <=> right.url)|reverse|slice(0, 2) }}
{{ product.images|merge(product.images) }}
{{ product.makers|column('name') }}
{{ product.images|batch(3) }}
{{ product.manufacturer|default(product) }}
{{ product.name|default }}
{{ product.name|split(',') }}
{{ product.images|find(image => image.url) }}
{{ product.images|length }}
{{ product.name|upper }}
{{ product.name|reverse }}
{{ random(product.images) }}
{{ cycle(product.images, 1) }}
{{ range(1, 3) }}
{{ max(1, 2) }}
{{ product.unknown|first }}
{{ constant('App\\Media::KIND') }}
{{ constant('KIND', product.cover) }}
{{ constant('App\\LIMIT') }}
",
    );
    assert_eq!(
        types,
        [
            r"App\Media|null",
            "string",
            "list<string>",
            r"list<App\Media>",
            r"array<string,App\Manufacturer>",
            "array<int,string>",
            "array<string,string>",
            r"list<App\Media>",
            r"list<App\Media>",
            "list<string>",
            r"list<array<int,App\Media>>",
            r"App\Manufacturer|App\Product",
            "string",
            "list<string>",
            r"App\Media|null",
            "int",
            "string",
            "string",
            r"App\Media",
            r"App\Media",
            "list<int>",
            "int",
            "unknown",
            r#""image""#,
            r#""image""#,
            "10",
        ]
    );
}

#[test]
fn lookups_operators_and_literals() {
    let cache = tempfile::tempdir().unwrap();
    let php = app_index(cache.path());
    let types = printed_types(
        &php,
        r"{# @var product \App\Product #}
{{ product.images[0] }}
{{ product.makers['acme'].name }}
{{ product.meta.title }}
{{ product.meta['media'] }}
{{ product.manufacturer ?? product }}
{{ product.cover ?: product.name }}
{{ product.name ? product.cover : product.manufacturer }}
{{ product.name ? product.cover }}
{{ product.name ~ '!' }}
{{ product.name == 'a' }}
{{ not product.name }}
{{ 1 + 2 }}
{{ 1.5 }}
{{ null }}
{{ [product.name, 1] }}
{{ {name: product.name, 'maker': product.manufacturer, other} }}
{{ {(product.name): 1} }}
{{ (product.manufacturer).media }}
{{ product.unknown ?? product }}
{{ product.manufacturer?.name }}
{{ product.manufacturer?.getMedia()?.url }}
{{ product?.name }}
{{ product.name ? product.cover?.url : 1 }}
",
    );
    assert_eq!(
        types,
        [
            r"App\Media",
            "string",
            "string",
            r"App\Media|null",
            r"App\Manufacturer|App\Product",
            r"App\Media|string",
            r"App\Manufacturer|App\Media|null",
            r"App\Media|null|string",
            "string",
            "bool",
            "bool",
            "int",
            "float",
            "null",
            "list<int|string>",
            r"array{maker:App\Manufacturer|null,name:string,other:mixed}",
            "array<string,mixed>",
            r"App\Media|null",
            "unknown",
            "null|string",
            "unknown",
            "string",
            "int|null|string",
        ]
    );
}

#[test]
fn a_variable_reassigned_in_many_branches_is_typed_once_per_assignment() {
    let cache = tempfile::tempdir().unwrap();
    let php = app_index(cache.path());
    let mut template =
        String::from("{# @var product \\App\\Product #}\n{% set classes = product.name %}\n");
    // Each assignment reads the previous value: without a memo the number
    // of evaluations doubles per branch.
    for _ in 0..40 {
        template.push_str("{% if product.name %}{% set classes = classes ~ ' x' %}{% endif %}\n");
    }
    template.push_str("{{ classes }}");
    assert_eq!(printed_types(&php, &template), ["string"]);
}
