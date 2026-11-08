use std::collections::BTreeSet;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_syntax::{LineIndex, SyntaxNodeExt};

use crate::*;

fn set<I: IntoIterator<Item = S>, S: Into<String>>(values: I) -> BTreeSet<String> {
    values.into_iter().map(Into::into).collect()
}

#[test]
fn template_input_variables_exclude_locals_and_retain_props() {
    let source = "{% props title, tone = 'info' %}
{% set local = source %}
{% import 'macros.html.twig' as forms %}
{% from 'macros.html.twig' import input as field %}
{% for key, item in items %}
    {{ item.name }} {{ outside }}
{% endfor %}
{{ product.name }}
{{ helper(value = argument) }}
{{ {'ignored': hashValue} }}
{% component Alert with {message: componentValue} %}{% endcomponent %}
{{ forms.input(title) }} {{ field(title) }}
{% macro card(label) %}{{ label }} {{ macroGlobal }}{% endmacro %}
";
    let root = swlsp_parser_twig::parse(source).root();
    let variables =
        template_input_variables_in_document("/project/templates/card.html.twig", &root);
    let mut names = Vec::new();
    for variable in &variables {
        assert_eq!(variable.name, &source[variable.range]);
        names.push(variable.name.clone());
    }
    assert_eq!(names.len(), 10);
    assert_eq!(
        set(names),
        set([
            "argument",
            "componentValue",
            "hashValue",
            "items",
            "macroGlobal",
            "outside",
            "product",
            "source",
            "title",
            "tone"
        ])
    );
}

#[test]
fn template_dependencies_respect_provided_and_isolated_contexts() {
    let source = "{% extends 'base.html.twig' %}
{% include 'nested.html.twig' with {foo: supplied} %}
{% include 'isolated.html.twig' with {value: isolatedValue} only %}
{{ include('function.html.twig', {known: explicit}) }}
{{ include('hidden.html.twig', {}, with_context: false) }}
{% embed 'layout.html.twig' with {slot: content} %}{% endembed %}
";
    let mut dependencies =
        template_dependencies_in_document(&swlsp_parser_twig::parse(source).root());
    let dependency = |template: &str, provided: &[&str], propagate: bool| TemplateDependency {
        template: template.into(),
        provided: provided.iter().map(|s| (*s).to_owned()).collect(),
        propagate,
    };
    let mut expected = vec![
        dependency("base.html.twig", &[], true),
        dependency("nested.html.twig", &["foo"], true),
        dependency("isolated.html.twig", &["value"], false),
        dependency("layout.html.twig", &["slot"], true),
        dependency("function.html.twig", &["known"], true),
        dependency("hidden.html.twig", &[], false),
    ];
    dependencies.sort_by(|a, b| a.template.cmp(&b.template));
    expected.sort_by(|a, b| a.template.cmp(&b.template));
    assert_eq!(dependencies, expected);
}

#[test]
fn include_parameter_context_recognizes_supported_forms() {
    for (source, needle, template, kind) in [
        (
            "{{ include('card.html.twig', {'title': value}) }}",
            "title",
            "card.html.twig",
            IncludeParameterKind::Function,
        ),
        (
            "{% include 'card.html.twig' with {plainKey: value} %}",
            "plainKey",
            "card.html.twig",
            IncludeParameterKind::Tag,
        ),
        (
            "{% embed 'layout.html.twig' with {\"slot\": value} %}{% endembed %}",
            "slot",
            "layout.html.twig",
            IncludeParameterKind::Embed,
        ),
    ] {
        let root = swlsp_parser_twig::parse(source).root();
        let offset = source.find(needle).unwrap() as u32 + 1;
        let node = root.node_at(offset.into());
        let context = include_parameter_context_at(&root, node.as_ref(), offset).expect(source);
        assert_eq!(context.template, template);
        assert_eq!(context.kind, kind);
        let parameter = include_parameter_at(&root, node.as_ref(), offset).expect(source);
        assert_eq!(parameter.name, needle);
        assert_eq!(&source[parameter.range], needle);
    }
}

#[test]
fn include_parameter_context_rejects_nested_hash_keys_and_values() {
    let source = "{% include 'card.html.twig' with {
    direct: value,
    nested: {ignored: other}
} %}";
    let root = swlsp_parser_twig::parse(source).root();
    for needle in ["value", "ignored", "other"] {
        let offset = source.find(needle).unwrap() as u32 + 1;
        assert!(
            include_parameter_at(&root, root.node_at(offset.into()).as_ref(), offset).is_none(),
            "{needle}"
        );
    }
    let offset = source.find("direct").unwrap() as u32 + 1;
    let parameter =
        include_parameter_at(&root, root.node_at(offset.into()).as_ref(), offset).expect("direct");
    assert_eq!(parameter.name, "direct");
}

#[test]
fn twig_indexer_stores_restores_and_resolves_template_contracts() {
    let cache = tempfile::tempdir().unwrap();
    let root = tempfile::tempdir().unwrap();
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    for (name, source) in [
        ("base.html.twig", "{{ baseValue }}"),
        ("nested.html.twig", "{{ supplied }} {{ nestedValue }}"),
        (
            "card.html.twig",
            "{% extends 'base.html.twig' %}\n{% include 'nested.html.twig' with {supplied: explicitValue} %}\n{{ ownValue }}",
        ),
    ] {
        let path = format!("{}/templates/{name}", root.path().display());
        index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let assert_contract = |index: &TwigIndexer| {
        let names: Vec<String> = index
            .get_template_variables(&["card.html.twig"])
            .unwrap()
            .into_iter()
            .map(|variable| variable.name)
            .collect();
        assert_eq!(names.len(), 4);
        assert_eq!(
            set(names),
            set(["baseValue", "explicitValue", "nestedValue", "ownValue"])
        );
    };
    assert_contract(&index);
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    assert_contract(&restored);
    restored.close().unwrap();
}

#[test]
fn twig_template_strings() {
    let source = "{% extends 'base.html.twig' %}
{% use 'macros.html.twig' %}
{{ include('partial.html.twig') }}
{{ source('source.html.twig') }}
{% include ['fallback.html.twig', 'second.html.twig'] with {'label': 'not-a-template.html.twig'} %}
{% form_theme form with ['forms/base.html.twig', 'forms/custom.html.twig'] %}
{% sw_extends { template: 'scoped.html.twig', scopes: ['not-a-template.html.twig'] } %}
{{ block('title', 'blocks/page.html.twig') }}
{{ block('not-a-template.html.twig') }}
{{ include(dynamic_name) }}
{% include '@Storefront/section-' ~ section.type ~ '.html.twig' %}
{% include 'static/' ~ 'card.html.twig' %}";
    let root = swlsp_parser_twig::parse(source).root();
    let mut names = Vec::new();
    for reference in twig_template_references("/project/templates/page.html.twig", &root) {
        assert_eq!(reference.template, &source[reference.range]);
        names.push(reference.template);
    }
    assert_eq!(names.len(), 10);
    assert_eq!(
        set(names),
        set([
            "base.html.twig",
            "macros.html.twig",
            "partial.html.twig",
            "source.html.twig",
            "fallback.html.twig",
            "second.html.twig",
            "forms/base.html.twig",
            "forms/custom.html.twig",
            "scoped.html.twig",
            "blocks/page.html.twig"
        ])
    );
}

#[test]
fn twig_template_target_groups_cases() {
    let source = "{% include 'base' ~ '.html.twig' %}
{% include '@Storefront/section-' ~ section.type ~ '.html.twig' %}
{% include ['fallback.html.twig', 'second.html.twig'] %}
{% include ['known.html.twig', dynamic_name] %}
{% include 'optional.html.twig' ignore missing %}
{{ include(template: 'function.html.twig', ignore_missing: true) }}
{{ source('source.html.twig', true) }}";
    let root = swlsp_parser_twig::parse(source).root();
    let groups = twig_template_target_groups(&root);
    assert_eq!(groups.len(), 7);
    let names = |index: usize| -> Vec<&str> {
        groups[index]
            .targets
            .iter()
            .map(|t| t.template.as_str())
            .collect()
    };
    assert!(groups[0].exact);
    assert_eq!(groups[0].targets[0].template, "base.html.twig");
    assert!(!groups[1].exact);
    assert!(groups[1].targets.is_empty());
    assert!(groups[2].exact);
    assert_eq!(names(2), ["fallback.html.twig", "second.html.twig"]);
    assert!(!groups[3].exact);
    assert_eq!(names(3), ["known.html.twig"]);
    assert!(groups[4].ignore_missing && groups[5].ignore_missing && groups[6].ignore_missing);
    let prefix = twigquery::nodes(&root, &[TWIG_LITERAL_STRING])
        .into_iter()
        .find(|literal| twigquery::string_value(literal).as_deref() == Some("@Storefront/section-"))
        .expect("dynamic prefix");
    assert!(is_twig_template_string(&prefix));
}

#[test]
fn php_template_strings() {
    let source = r#"<?php
$this->render('page.html.twig');
$this->renderView('view.html.twig');
$this->renderStorefront('storefront.html.twig');
$this->stream('stream.html.twig');
$this->render(template: 'named.html.twig');
$this->renderBlock('ignored.html.twig', 'block');
$this->render(['nested.html.twig']);
$this->render("$dynamic.html.twig");
#[Template('attribute.html.twig')]
/** @Template(template="annotation.html.twig") */
function page() {}
"#;
    let root = swlsp_parser_php::parse(source).root();
    let mut names = Vec::new();
    for reference in php_template_references("/project/Controller.php", &root) {
        assert_eq!(reference.template, &source[reference.range]);
        names.push(reference.template);
    }
    assert_eq!(names.len(), 7);
    assert_eq!(
        set(names),
        set([
            "page.html.twig",
            "view.html.twig",
            "storefront.html.twig",
            "stream.html.twig",
            "named.html.twig",
            "ignored.html.twig",
            "attribute.html.twig"
        ])
    );
}

fn token_parsers(path: &str, source: &str) -> Vec<TwigTag> {
    let root = swlsp_parser_php::parse(source).root();
    parse_twig_token_parsers(path, &root, source, &LineIndex::new(source))
}

#[test]
fn parse_twig_token_parsers_cases() {
    let source = r#"<?php
namespace Twig\TokenParser;
interface TokenParserInterface {}

/**
 * @deprecated Foobar deprecated message
 */
class SpacelessTokenParser implements TokenParserInterface
{
    public function getTag(): string { return 'spaceless'; }
}

class SandboxTokenParser extends AbstractTokenParser
{
    public function getTag(): string { return 'sandbox'; }
    public function parse(): void {
        trigger_deprecation(
            'twig/twig',
            '3.9',
            'The "sandbox" tag is deprecated.',
        );
    }
}

#[\Deprecated]
class AttributeDeprecatedTokenParser implements TokenParserInterface
{
    public function getTag(): string { return 'attribute_deprecated'; }
}

class ActiveTokenParser implements TokenParserInterface
{
    public function getTag(): string { return 'active'; }
}

class DerivedTokenParser extends ActiveTokenParser
{
    public function getTag(): string { return 'derived'; }
}
"#;
    let tags = token_parsers("/project/TokenParsers.php", source);
    assert_eq!(tags.len(), 5);
    let by_name: std::collections::HashMap<&str, &TwigTag> =
        tags.iter().map(|tag| (tag.name.as_str(), tag)).collect();
    for tag in &tags {
        assert_eq!(tag.name, &source[tag.range]);
    }
    assert!(by_name["spaceless"].deprecated);
    assert_eq!(
        by_name["spaceless"].deprecation,
        "Foobar deprecated message"
    );
    assert!(by_name["sandbox"].deprecated);
    assert!(by_name["sandbox"].deprecation.contains("sandbox"));
    assert!(by_name["attribute_deprecated"].deprecated);
    assert!(!by_name["active"].deprecated);
    assert!(!by_name["derived"].deprecated);
    assert_eq!(
        by_name["spaceless"].class,
        r"Twig\TokenParser\SpacelessTokenParser"
    );
    for name in ["spaceless", "sandbox", "attribute_deprecated"] {
        assert!(!by_name[name].deprecated_range.is_empty(), "{name}");
    }
}

#[test]
fn php_deprecated_attribute_flags_token_parser_class() {
    let tags = token_parsers(
        "/project/Parser.php",
        r"<?php
#[\Deprecated]
class Parser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}",
    );
    assert_eq!(tags.len(), 1);
    assert!(tags[0].deprecated);
}

#[test]
fn twig_tag_usages_cases() {
    let source = r#"{% spaceless %}
{% endspaceless %}
{%- attribute_deprecated -%}
{% verbatim %}{% spaceless %}{% endverbatim %}
{% raw %}{% spaceless %}{% endraw %}
{# {% spaceless %} #}
{{ "spaceless" }}
"#;
    let usages = twig_tag_usages(source.as_bytes());
    let names: Vec<&str> = usages.iter().map(|usage| usage.name.as_str()).collect();
    assert_eq!(
        names,
        [
            "spaceless",
            "endspaceless",
            "attribute_deprecated",
            "verbatim",
            "raw"
        ]
    );
    for usage in &usages {
        assert_eq!(usage.name, &source[usage.range]);
    }
}

#[test]
fn parse_twig_token_parsers_excludes_tests() {
    let source = r"<?php
class ParserTest implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'from_class_test'; }
}
class Parser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'from_file_test'; }
}";
    assert!(token_parsers("/project/ParserTest.php", source).is_empty());
    let class_test = r"<?php
class ParserTest implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'from_class_test'; }
}";
    assert!(token_parsers("/project/Parser.php", class_test).is_empty());
}

#[test]
fn twig_indexer_persists_token_parser_tags() {
    let cache = tempfile::tempdir().unwrap();
    let dir = tempfile::tempdir().unwrap();
    let path = format!("{}/LegacyTokenParser.php", dir.path().display());
    let source = r"<?php
/** @deprecated Use the modern tag instead. */
class LegacyTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}";
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
    let tags = index.get_twig_tag("legacy").unwrap();
    assert_eq!(tags.len(), 1);
    assert!(tags[0].deprecated);
    assert!(tags[0].deprecation.contains("modern"));
    index.close().unwrap();
    let reopened = TwigIndexer::new(cache.path(), None).unwrap();
    assert_eq!(reopened.get_twig_tag("legacy").unwrap(), tags);
    let message = reopened
        .twig_tag_deprecation("endlegacy")
        .unwrap()
        .expect("deprecated");
    assert!(message.contains("modern"));
    reopened.close().unwrap();
}
