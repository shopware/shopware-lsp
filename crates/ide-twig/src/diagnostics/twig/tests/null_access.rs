use swlsp_php::PhpIndex;

use super::*;
use crate::diagnostics::twig::TwigNullAccessAnalyzer;

fn indexes() -> (tempfile::TempDir, PhpIndex) {
    let cache = tempdir();
    let php = php_index(cache.path());
    index_file(
        &php,
        "/project/src/Model.php",
        r"<?php
namespace App;

class Media { public string $url; public ?Media $thumbnail; }
class Manufacturer { public string $name; public ?Media $media; }
class Product {
    public string $name;
    public Manufacturer $brand;
    public ?Manufacturer $manufacturer;
    /** @var list<Media> */
    public array $images;
    public function getCover(): ?Media { return null; }
    public function find(string $name): ?Media { return null; }
}
",
    );
    (cache, php)
}

fn check(fixture: &str) {
    let (_cache, php) = indexes();
    check_problems(
        &TwigNullAccessAnalyzer::new(None, Some(php)),
        "file:///project/templates/page.html.twig",
        &format!("{{# @var product \\App\\Product #}}\n{fixture}"),
    );
}

#[test]
fn null_access_is_reported_without_a_guard() {
    check(
        "{{ product.brand.name }}
{{ product.manufacturer.{|twig.member.null:name|} }}
{{ product.cover.{|twig.member.null:url|} }}
{{ product.manufacturer.{|twig.member.null:media|}.{|twig.member.null:url|} }}
{{ product.images|first.{|twig.member.null:url|} }}
{% set maker = product.manufacturer %}
{{ maker.{|twig.member.null:name|} }}
{% if product.name %}{{ product.manufacturer.{|twig.member.null:name|} }}{% endif %}
{% if product.manufacturer %}{% else %}{{ product.manufacturer.{|twig.member.null:name|} }}{% endif %}
{{ product.manufacturer or product.manufacturer.{|twig.member.null:name|} }}
{% if product.find('a') %}{{ product.find('b').{|twig.member.null:url|} }}{{ product.find(product.name).{|twig.member.null:url|} }}{% endif %}
{% if product.name %}{% set guard = product.manufacturer %}{% endif %}
{% if guard %}{{ product.manufacturer.{|twig.member.null:name|} }}{% endif %}
{# The condition itself reads from null; what it guards is safe. #}
{% if product.manufacturer.{|twig.member.null:media|} %}{{ product.manufacturer.media.url }}{% endif %}
",
    );
}

#[test]
fn conditions_rule_null_out() {
    check(
        "{% if product.manufacturer %}{{ product.manufacturer.name }}{% endif %}
{% if product.manufacturer is not null %}{{ product.manufacturer.name }}{% endif %}
{% if product.manufacturer is not none and product.name %}{{ product.manufacturer.name }}{% endif %}
{% if product.manufacturer != null %}{{ product.manufacturer.name }}{% endif %}
{% if product.manufacturer is defined %}{{ product.manufacturer.name }}{% endif %}
{% if product.manufacturer is not empty %}{{ product.manufacturer.name }}{% endif %}
{% if product.manufacturer is null %}{% else %}{{ product.manufacturer.name }}{% endif %}
{% if not product.manufacturer %}{% elseif product.name %}{{ product.manufacturer.name }}{% endif %}
{% if product.manufacturer and product.manufacturer.media %}{{ product.manufacturer.media.url }}{% endif %}
{% if product.getCover() %}{{ product.getCover().url }}{% endif %}
{% if product.find('a.b') %}{{ product.find('a.b').url }}{% endif %}
{% if product.images|length > 0 %}{{ product.name }}{% endif %}
{{ product.manufacturer ? product.manufacturer.name : '' }}
{{ product.manufacturer is null ? '' : product.manufacturer.name }}
{{ product.manufacturer and product.manufacturer.name }}
{{ not product.manufacturer or product.manufacturer.name }}
{% set maker = product.manufacturer %}
{% if maker %}{{ maker.name }}{% endif %}
{% set hasMaker = product.manufacturer is not null and product.name %}
{% set hasBoth = hasMaker and product.cover %}
{% if hasBoth %}{{ product.manufacturer.name }}{{ product.cover.url }}{% endif %}
{{ hasMaker ? product.manufacturer.name : '' }}
",
    );
}

#[test]
fn null_tolerant_expressions_are_left_alone() {
    check(
        "{{ product.manufacturer.name ?? '' }}
{{ product.manufacturer.media.url ?? product.name }}
{{ product.manufacturer.name|default('') }}
{{ product.manufacturer.name is defined }}
{% if product.manufacturer.media.url is defined %}{% endif %}
{{ (product.manufacturer ?? product.brand).name }}
{{ product.manufacturer|default(product.brand).name }}
{{ product.unknown.name }}
{{ product.manufacturer?.name }}
{{ product.manufacturer?.media.url }}
{{ product.manufacturer?.media?.thumbnail.url }}
",
    );
}

#[test]
fn a_member_of_a_known_subclass_is_not_missing() {
    use crate::diagnostics::twig::TwigMemberMissingAnalyzer;
    let cache = tempdir();
    let php = php_index(cache.path());
    index_file(
        &php,
        "/project/src/Result.php",
        r"<?php
namespace App;

abstract class Result { public string $name; }
class EntityResult extends Result { public array $entities; }
class Results { public function get(string $name): ?Result { return null; } }
",
    );
    check_problems(
        &TwigMemberMissingAnalyzer::new(None, Some(php)),
        "file:///project/templates/page.html.twig",
        "{# @var results \\App\\Results #}
{% set result = results.get('a') %}
{{ result.name }}{{ result.entities }}{{ result.{|twig.member.missing:nothing|} }}
",
    );
}
