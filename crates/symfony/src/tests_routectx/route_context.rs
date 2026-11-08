use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use super::*;

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

fn node_at(root: &SyntaxNode, offset: usize) -> SyntaxNode {
    root.node_at(TextSize::from(offset as u32)).unwrap()
}

#[test]
fn twig_html_route_references_and_path_matching() {
    let source = r#"<a href="/catalog/42?preview=1">Product</a>
<form action=""></form>
<a href="{{ path('catalog.show') }}">Dynamic</a>
<a href="https://example.test/catalog/42?preview=1#details">Absolute</a>"#;
    let parse = swlsp_parser_twig::parse(source);
    let root = parse.root();
    let strings = twigquery::nodes(&root, &[twigsyntax::HTML_STRING]);
    assert_eq!(strings.len(), 4);
    let link = twig_html_route_reference_at(&strings[0]).unwrap();
    assert_eq!(link.value, "/catalog/42?preview=1");
    assert_eq!(route_url_path(&link.value), "/catalog/42");
    let form = twig_html_route_reference_at(&strings[1]).unwrap();
    assert!(form.value.is_empty());
    assert!(twig_html_route_reference_at(&strings[2]).is_none());
    let external = twig_html_route_reference_at(&strings[3]).unwrap();
    assert_eq!(route_url_path(&external.value), "/catalog/42");
    {
        assert!(crate::route_path_matches("/catalog/{id}", "/catalog/42"));
        assert!(!crate::route_path_matches(
            "/catalog/{id}",
            "/catalog/42/review"
        ));
    }
    let references = twig_html_route_references(&root);
    assert_eq!(references.len(), 2);
    assert_eq!(references[0].value, "/catalog/42?preview=1");
    assert_eq!(
        references[1].value,
        "https://example.test/catalog/42?preview=1#details"
    );
}

#[test]
fn normalize_route_search_path_cases() {
    for (value, expected) in [
        ("", ""),
        ("   ", ""),
        ("https://www.de.test:8664", "/"),
        ("//www.de.test:8664", "/"),
        ("https://www.de.test:8664/foo", "/foo"),
        ("https://www.de.test:8664/foo?utm=1#details", "/foo"),
        ("https://www.de.test:8664/edit/{id}?utm=1", "/edit/{id}"),
        ("//www.de.test:8664/foo#details", "/foo"),
        ("/foo?utm=1", "/foo"),
        ("foo/bar", "foo/bar"),
        ("foo:bar", "foo:bar"),
        ("/edit/foo%2Ebar~v1?preview=1", "/edit/foo%2Ebar~v1"),
        ("/a b/é", "/a%20b/%C3%A9"),
        // The URL standard escapes the space while parsing, so `!` stays
        // (Go: "/a%20b%21"); relative references escape like Go.
        ("https://shop.example/a b!?x", "/a%20b!"),
        ("/a b!?x", "/a%20b%21"),
        ("//shop.example/[x]#y", "/[x]"),
        ("/a%zz?x", "/a%zz"),
        ("mailto:x@y", "mailto:x@y"),
        ("http://", "/"),
    ] {
        assert_eq!(normalize_route_search_path(value), expected, "{value:?}");
    }
}

#[test]
fn route_path_search_matches_partial_and_absolute_urls() {
    for (route, search, expected) in [
        ("/edit/{id}", "/edit/12", true),
        ("/edit/{id}", "/edit/{id}", true),
        ("/class-like-route", "/class-like-route", true),
        (
            "/class-like-route",
            "https://www.de.test:8664/class-like-route?utm=1#details",
            true,
        ),
        (
            "/edit/{id}",
            "https://www.de.test:8664/edit/12?utm=1#details",
            true,
        ),
        ("/edit/{id}", "/edit/foo.bar", true),
        ("/edit/{id}", "/edit/foo%2Ebar~v1", true),
        ("/edit/{id}", "/edit/foo/bar", false),
        (
            "/car/{edit}/foobar",
            "https://www.de.test:8664/car/{edit}/foobar?utm=1",
            true,
        ),
        (
            "/car/{edit}/foobar",
            "https://www.de.test:8664/car/12/foo?utm=1",
            true,
        ),
        ("/car/{edit}/foobar", "ar/12/foo", true),
        ("/edit/{id}", "12", false),
        ("/edit/{id}", "/edit/", true),
    ] {
        assert_eq!(
            crate::route_path_search_matches(route, search),
            expected,
            "{route} {search}"
        );
    }
}

#[test]
fn route_path_matches_remain_exact() {
    assert!(crate::route_path_matches(
        "/edit/{id}",
        "https://www.de.test/edit/foo.bar?preview=1#details"
    ));
    assert!(crate::route_path_matches(
        "/edit/{id}",
        "/edit/foo%2Ebar~v1"
    ));
    assert!(!crate::route_path_matches("/edit/{id}", "/edit/foo/bar"));
    assert!(!crate::route_path_matches("/edit/{id}", "/edit/"));
}

#[test]
fn js_route_url_reference_contexts() {
    for (name, source, found) in [
        ("fetch", "fetch('/edit/12')", true),
        ("axios", "axios('/edit/12')", true),
        ("axios create URL", "axios.create('/edit/12')", true),
        ("request constructor", "new Request('/edit/12')", true),
        ("URL property", "const options = { url: '/edit/12' }", true),
        (
            "Axios base URL",
            "axios.create({ baseURL: '/edit/12' })",
            true,
        ),
        ("unrelated call", "logger.info('/edit/12')", false),
        (
            "unrelated base URL",
            "const options = { baseURL: '/edit/12' }",
            false,
        ),
        ("nested fetch value", "fetch({ path: '/edit/12' })", false),
        ("dynamic template", "fetch(`/edit/${id}`)", false),
    ] {
        let parse = swlsp_parser_js::parse(source);
        let offset = match source.find("/edit/12") {
            Some(index) if !source.contains("${id}") => index + 3,
            _ => source.find("/edit/").unwrap() + 3,
        };
        let node = node_at(&parse.root(), offset);
        let reference = js_route_url_reference_at(&node);
        assert_eq!(reference.is_some(), found, "{name}");
        if let Some(reference) = reference {
            assert_eq!(reference.value, "/edit/12", "{name}");
            assert_eq!(slice(source, reference.range), "/edit/12", "{name}");
        }
    }
}

#[test]
fn php_route_path_reference_uses_only_attribute_path_arguments() {
    for (name, source, found) in [
        (
            "positional",
            "<?php #[Route('/catalog/products/detail')] function show() {}",
            true,
        ),
        (
            "named",
            "<?php #[Route(name: 'catalog', path: '/catalog/products/detail')] function show() {}",
            true,
        ),
        (
            "name",
            "<?php #[Route(name: '/catalog/products/detail')] function show() {}",
            false,
        ),
        (
            "methods",
            "<?php #[Route('/catalog', methods: ['/catalog/products/detail'])] function show() {}",
            false,
        ),
        (
            "unrelated attribute",
            "<?php #[Cache('/catalog/products/detail')] function show() {}",
            false,
        ),
    ] {
        let parse = swlsp_parser_php::parse(source);
        let offset = source.rfind("/catalog/products/detail").unwrap() + "/catalog/prod".len();
        let reference = php_route_path_reference_at(&node_at(&parse.root(), offset));
        assert_eq!(reference.is_some(), found, "{name}");
        if let Some(reference) = reference {
            assert_eq!(reference.value, "/catalog/products/detail");
            assert_eq!(
                reference.prefix_at(offset as u32).as_deref(),
                Some("/catalog/products")
            );
        }
    }
}

#[test]
fn php_route_annotation_path_reference_uses_only_path_arguments() {
    for (name, source, value) in [
        (
            "default",
            "<?php\n/** @Route(\"/catalog/<caret>products\") */\nfunction show() {}\n",
            Some("/catalog/products"),
        ),
        (
            "named path",
            "<?php\n/** @Route(name=\"catalog\", path=\"/catalog/<caret>products\") */\nfunction show() {}\n",
            Some("/catalog/products"),
        ),
        (
            "annotation namespace alias",
            "<?php\n/** @Routing\\Route(path=\"/catalog/<caret>products\") */\nfunction show() {}\n",
            Some("/catalog/products"),
        ),
        (
            "name",
            "<?php\n/** @Route(name=\"/catalog/<caret>products\") */\nfunction show() {}\n",
            None,
        ),
        (
            "methods",
            "<?php\n/** @Route(\"/catalog\", methods={\"/catalog/<caret>products\"}) */\nfunction show() {}\n",
            None,
        ),
        (
            "second positional argument",
            "<?php\n/** @Route(\"/catalog\", \"/catalog/<caret>products\") */\nfunction show() {}\n",
            None,
        ),
        (
            "unrelated annotation",
            "<?php\n/** @Cache(\"/catalog/<caret>products\") */\nfunction show() {}\n",
            None,
        ),
    ] {
        let offset = source.find("<caret>").unwrap();
        let source = source.replacen("<caret>", "", 1);
        let reference = php_route_annotation_path_reference_at(&source, offset as u32);
        assert_eq!(
            reference.as_ref().map(|reference| reference.value.as_str()),
            value,
            "{name}"
        );
        if let Some(reference) = reference {
            assert_eq!(Some(slice(&source, reference.range)), value, "{name}");
        }
    }
}

#[test]
fn php_route_name_references_use_only_named_name_arguments() {
    for (name, source, annotation, found) in [
        (
            "attribute name",
            "<?php #[Route(path: '/catalog', name: 'draft<caret>')] function show() {}",
            false,
            true,
        ),
        (
            "attribute path",
            "<?php #[Route(path: '/catalog<caret>', name: 'draft')] function show() {}",
            false,
            false,
        ),
        (
            "annotation name",
            "<?php\n/** @Route(path=\"/catalog\", name=\"draft<caret>\") */\nfunction show() {}\n",
            true,
            true,
        ),
        (
            "annotation default",
            "<?php\n/** @Route(\"/catalog<caret>\") */\nfunction show() {}\n",
            true,
            false,
        ),
    ] {
        let offset = source.find("<caret>").unwrap();
        let source = source.replacen("<caret>", "", 1);
        let reference = if annotation {
            php_route_annotation_name_reference_at(&source, offset as u32)
        } else {
            let parse = swlsp_parser_php::parse(&source);
            php_route_name_reference_at(&node_at(&parse.root(), offset))
        };
        assert_eq!(reference.is_some(), found, "{name}");
        if let Some(reference) = reference {
            assert_eq!(reference.value, "draft");
        }
    }
}

#[test]
fn php_route_name_suggestion_mirrors_controller_convention() {
    for (source, marker, expected) in [
        (
            "<?php\nnamespace App\\Controller;\nclass FooController {\n    #[Route(name: 'draft')]\n    public function foo1(): void {}\n}",
            "draft",
            "app_foo_foo1",
        ),
        (
            "<?php\nnamespace Foo\\ParkResortBundle\\Controller\\SubController\\BundleController;\nclass FooController {\n    public function nestedFooAction(): void {}\n}",
            "nestedFooAction",
            "foo_parkresort_sub_bundle_foo_nestedfoo",
        ),
        (
            "<?php\nnamespace App\\Controller;\nclass FooController {\n    public function _fragmentAction(): void {}\n}",
            "_fragmentAction",
            "app_foo_fragment",
        ),
    ] {
        let parse = swlsp_parser_php::parse(source);
        let root = parse.root();
        let offset = source.find(marker).unwrap();
        assert_eq!(
            php_route_name_suggestion(&root, &node_at(&root, offset), offset as u32),
            expected
        );
    }
}

#[test]
fn php_route_path_prefix_requires_intermediate_segment() {
    let source = "<?php #[Route('/catalog/products/detail')] function show() {}";
    let parse = swlsp_parser_php::parse(source);
    let start = source.find("/catalog/products/detail").unwrap();
    let reference = php_route_path_reference_at(&node_at(&parse.root(), start + 2)).unwrap();
    assert_eq!(
        reference
            .prefix_at((start + "/catalog/prod".len()) as u32)
            .as_deref(),
        Some("/catalog/products")
    );
    assert!(
        reference
            .prefix_at((start + "/catalog/products/det".len()) as u32)
            .is_none()
    );
}

#[test]
fn twig_route_parameter_reference_supports_identifier_hash_keys() {
    for (source, marker, expected) in [
        (
            "{{ path('product.show', {'slug': product.slug}) }}",
            "slug':",
            Some(("product.show", "slug")),
        ),
        (
            "{{ path('product.show', {slug: product.slug}) }}",
            "slug:",
            Some(("product.show", "slug")),
        ),
        (
            "{{ path('product.show', {slug}) }}",
            "slug}",
            Some(("product.show", "slug")),
        ),
        (
            "{{ path('product.show', {slug: product.value}) }}",
            "value",
            None,
        ),
        (
            "{{ include('product.show', {slug: product.slug}) }}",
            "slug:",
            None,
        ),
    ] {
        let parse = swlsp_parser_twig::parse(source);
        let offset = source.find(marker).unwrap();
        let node = node_at(&parse.root(), offset + 1);
        let actual = twig_route_parameter_reference_at(&node);
        assert_eq!(
            actual
                .as_ref()
                .map(|(route, parameter)| (route.as_str(), parameter.as_str())),
            expected,
            "{source} / {marker}"
        );
    }
}

#[test]
fn twig_route_comparison_references_cases() {
    let source = r#"{% if app.request.attributes.get('_route') == 'route.equal' %}{% endif %}
{% if 'route.reverse' !== app.request.attributes.get("_route") %}{% endif %}
{% if app.request.attributes.get('_route') is same as('route.same') %}{% endif %}
{% if app.request.attributes.get('_route') is not same as('route.not_same') %}{% endif %}
{% if app.request.attributes.get('_route') in ['route.in_a', 'route.in_b'] %}{% endif %}
{% if app.request.attributes.get('_route') not in ['route.not_in'] %}{% endif %}"#;
    let parse = swlsp_parser_twig::parse(source);
    let references = twig_route_comparison_references(&parse.root());
    let mut names: Vec<&str> = Vec::new();
    for reference in &references {
        names.push(&reference.value);
        assert_eq!(slice(source, reference.range), reference.value);
        assert!(is_twig_route_name(&reference.node));
    }
    names.sort_unstable();
    let mut expected = vec![
        "route.equal",
        "route.reverse",
        "route.same",
        "route.not_same",
        "route.in_a",
        "route.in_b",
        "route.not_in",
    ];
    expected.sort_unstable();
    assert_eq!(names, expected);
}

#[test]
fn twig_route_comparison_rejects_partial_and_unrelated_strings() {
    for source in [
        "{% if app.request.attributes.get('_route') starts with 'product.' %}{% endif %}",
        "{% if app.request.query.get('_route') == 'product.show' %}{% endif %}",
        "{% if app.request.attributes.get('_controller') == 'product.show' %}{% endif %}",
        "{% if route == 'product.show' %}{% endif %}",
        "{% if app.request.attributes.get('_route') == \"product.#{kind}\" %}{% endif %}",
        "{% if app.request.attributes.get('_route') in [['product.show']] %}{% endif %}",
    ] {
        let parse = swlsp_parser_twig::parse(source);
        let root = parse.root();
        assert!(
            twig_route_comparison_references(&root).is_empty(),
            "{source}"
        );
        for literal in twigquery::nodes(&root, &[twigsyntax::TWIG_LITERAL_STRING]) {
            if twigquery::string_value(&literal).as_deref() == Some("product.show") {
                assert!(!is_twig_route_name(&literal), "{source}");
            }
        }
    }
}

#[test]
fn route_url_path_rejects_opaque_and_fragments() {
    assert_eq!(route_url_path("mailto:info@example.test"), "");
    assert_eq!(route_url_path("#details"), "");
    assert_eq!(route_url_path("catalog/42"), "/catalog/42");
    assert_eq!(route_url_path("tel:+4912345"), "");
    assert_eq!(route_url_path("javascript:void(0)"), "");
    assert_eq!(
        route_url_path("https://shop.example/catalog/42?x#y"),
        "/catalog/42"
    );
}
