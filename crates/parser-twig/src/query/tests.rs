//! Port of `internal/parser/twig/query/query_test.go`.

use std::collections::BTreeMap;

use super::*;
use crate::parse;

fn root_of(source: &str) -> SyntaxNode {
    let root = parse(source).root();
    assert_eq!(root.text().to_string(), source);
    root
}

fn map(entries: &[(&str, &str)]) -> BTreeMap<String, String> {
    entries
        .iter()
        .map(|(key, value)| ((*key).to_owned(), (*value).to_owned()))
        .collect()
}

#[test]
fn iterate_nodes_matches_nodes() {
    let root = root_of(
        "
{% block card %}
    {{ path('frontend.home') }}
    {% if visible %}<span>{{ title }}</span>{% endif %}
{% endblock %}
",
    );
    let kinds = [TWIG_BLOCK, TWIG_FUNCTION_CALL, HTML_STARTING_TAG];
    let iterated: Vec<_> = iterate_nodes(&root, &kinds).collect();
    assert_eq!(iterated, nodes(&root, &kinds));
    assert_eq!(iterated.len(), 3);
    assert_eq!(
        iterate_nodes(&root, &[TWIG_LITERAL_NAME]).take(1).count(),
        1
    );
}

#[test]
fn semantic_queries() {
    let source = "{{ 'checkout.cart'|trans }} {{ path('frontend.home', {'nested': 'not-a-route'}) }} {% sw_icon 'missing' {'pack': 'custom'} %}";
    let root = root_of(source);

    let strings = string_arguments_in_functions(&root, &["path"]);
    assert_eq!(strings.len(), 1);
    assert_eq!(string_value(&strings[0]).as_deref(), Some("frontend.home"));

    let translated = nodes(&root, &[TWIG_LITERAL_STRING]).iter().any(|literal| {
        string_in_filter(literal, &["trans"])
            && string_value(literal).as_deref() == Some("checkout.cart")
    });
    assert!(translated);

    let icons = nodes(&root, &[SHOPWARE_ICON]);
    assert_eq!(icons.len(), 1);
    assert_eq!(
        hash_string_map(&icons[0]).get("pack").map(String::as_str),
        Some("custom")
    );

    let icon_strings = nodes(&icons[0], &[TWIG_LITERAL_STRING]);
    assert_eq!(icon_strings.len(), 3);
    assert!(string_in_tag(&icon_strings[0], &["sw_icon"]));
    assert!(!string_in_tag(&icon_strings[1], &["sw_icon"]));
    assert!(!string_in_tag(&icon_strings[2], &["sw_icon"]));
    assert!(string_is_hash_value_for_key(&icon_strings[2], "pack"));
}

#[test]
fn hash_string_map_with_unquoted_keys() {
    let root = root_of("{% sw_icon 'arrow-medium-right' style { size: 'fluid', pack: 'solid' } %}");
    let icons = nodes(&root, &[SHOPWARE_ICON]);
    assert_eq!(icons.len(), 1);

    assert_eq!(
        hash_string_map(&icons[0]),
        map(&[("pack", "solid"), ("size", "fluid")])
    );
    assert!(hash_has_key(&icons[0], "pack"));
    assert!(!hash_has_key(&icons[0], "missing"));

    // Icon name, size value and pack value; unquoted keys are not strings.
    let icon_strings = nodes(&icons[0], &[TWIG_LITERAL_STRING]);
    assert_eq!(icon_strings.len(), 3);
    assert!(string_is_hash_value_for_key(&icon_strings[2], "pack"));
    assert!(!string_is_hash_value_for_key(&icon_strings[1], "pack"));
}

#[test]
fn hash_string_map_skips_dynamic_entries() {
    let root = root_of(
        "{{ path('route', {id: product.id, 'quoted': 'v', 42: 'num', (dyn): 'p', pack: somePack, nested: {inner: 'x'}}) }}",
    );
    let hashes = nodes(&root, &[TWIG_LITERAL_HASH]);
    assert!(!hashes.is_empty());

    assert_eq!(
        hash_string_map(&hashes[0]),
        map(&[("quoted", "v"), ("42", "num")])
    );
    assert!(hash_has_key(&hashes[0], "pack"));
    assert!(!hash_has_key(&hashes[0], "dyn"));
    assert!(!hash_has_key(&hashes[0], "inner"));
}

#[test]
fn function_argument_and_hash_key_queries() {
    let root = root_of("{{ path('product.show', {'id': product.id}) }}");
    let calls = nodes(&root, &[TWIG_FUNCTION_CALL]);
    assert_eq!(calls.len(), 1);

    let route = string_argument(&calls[0], 0).expect("route argument");
    assert_eq!(string_value(&route).as_deref(), Some("product.show"));
    assert_eq!(function_argument_index(&route), Some(0));

    let argument = function_argument(&calls[0], 1).expect("second argument");
    assert_eq!(function_argument_index(&argument), Some(1));
    assert!(function_argument(&calls[0], 2).is_none());

    let hashes = nodes(&calls[0], &[TWIG_LITERAL_HASH]);
    let keys = nodes(&calls[0], &[TWIG_LITERAL_HASH_KEY]);
    assert_eq!(hashes.len(), 1);
    assert_eq!(keys.len(), 1);
    assert_eq!(function_argument_index(&hashes[0]), Some(1));
    assert_eq!(hash_at(&keys[0]).as_ref(), Some(&hashes[0]));
    assert_eq!(hash_key_at(&keys[0]).as_ref(), Some(&keys[0]));
}

#[test]
fn template_tag_queries() {
    let root = root_of(
        "{% use 'macros.html.twig' %}
{% embed 'card.html.twig' %}{% endembed %}
{% from 'forms.html.twig' import input %}
{% import 'icons.html.twig' as icons %}",
    );
    let mut found = BTreeMap::new();
    for literal in nodes(&root, &[TWIG_LITERAL_STRING]) {
        let Some(tag) = tag_at(&literal).and_then(|tag| tag_name(&tag)) else {
            continue;
        };
        if string_in_tag(&literal, &[tag.as_str()]) {
            found.insert(tag, string_value(&literal).unwrap_or_default());
        }
    }
    assert_eq!(
        found,
        map(&[
            ("use", "macros.html.twig"),
            ("embed", "card.html.twig"),
            ("from", "forms.html.twig"),
            ("import", "icons.html.twig"),
        ])
    );
}

#[test]
fn form_theme_tag_query() {
    let source = "{% form_theme form with [
    'forms/base.html.twig',
    'forms/custom.html.twig'
] %}";
    let result = parse(source);
    assert!(result.errors.is_empty(), "{:?}", result.errors);
    let root = result.root();
    let tags = nodes(&root, &[TWIG_FORM_THEME]);
    assert_eq!(tags.len(), 1);
    assert_eq!(tag_name(&tags[0]).as_deref(), Some("form_theme"));
    let names: Vec<_> = nodes(&tags[0], &[TWIG_LITERAL_STRING])
        .iter()
        .filter(|literal| string_in_tag(literal, &["form_theme"]))
        .filter_map(string_value)
        .collect();
    assert_eq!(names, ["forms/base.html.twig", "forms/custom.html.twig"]);
}

#[test]
fn assetic_tag_query() {
    let source = "{% stylesheets
    'css/app.css'
    'css/base.css'
    'css/' ~ dynamic_name
    filter='cssrewrite'
%}
<link href=\"{{ asset_url }}\">
{% endstylesheets %}";
    let result = parse(source);
    assert!(result.errors.is_empty(), "{:?}", result.errors);
    let root = result.root();
    let names: Vec<_> = nodes(&root, &[TWIG_LITERAL_STRING])
        .iter()
        .filter(|literal| string_in_tag(literal, &["stylesheets"]))
        .filter_map(string_value)
        .collect();
    assert_eq!(names, ["css/app.css", "css/base.css"]);
}

#[test]
fn function_and_filter_names() {
    let root = root_of("{{ 'x'|trans({'%a%': 1}) }} {{ url('route') }}");
    let filters = nodes(&root, &[TWIG_FILTER]);
    assert_eq!(filter_name(&filters[0]).as_deref(), Some("trans"));
    let calls = nodes(&root, &[TWIG_FUNCTION_CALL]);
    let names: Vec<_> = calls.iter().filter_map(function_name).collect();
    assert!(names.contains(&"url".to_owned()));
    let url = calls
        .iter()
        .find(|call| function_name(call).as_deref() == Some("url"))
        .unwrap();
    let route = string_argument(url, 0).unwrap();
    assert!(string_in_function(&route, &["url"]));
    assert!(!string_in_function(&route, &["path"]));
    assert!(string_is_static(&route));
    let name_token = url
        .descendants()
        .find(|node| node.kind() == TWIG_LITERAL_NAME)
        .unwrap();
    assert_eq!(function_name_at(&name_token).as_deref(), Some("url"));
    assert_eq!(function_name_at(&route), None);
}

#[test]
fn html_queries() {
    let root = root_of("<sw-button :disabled=\"x\" class=\"a\">b</sw-button>");
    let tag = nodes(&root, &[HTML_STARTING_TAG]);
    assert_eq!(html_tag_name(&tag[0]).as_deref(), Some("sw-button"));
    let attributes: Vec<_> = nodes(&root, &[HTML_ATTRIBUTE])
        .iter()
        .filter_map(html_attribute_name)
        .collect();
    assert_eq!(attributes, [":disabled", "class"]);
}
