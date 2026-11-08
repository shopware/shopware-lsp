use std::sync::Arc;

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_snippet::SnippetIndexer;

use super::*;
use crate::twig::SnippetAnalyzer;

fn frontend_matches(code: &str) -> Vec<swlsp_syntax::SyntaxNode> {
    let root = swlsp_parser_twig::parse(code).root();
    twigquery::nodes(&root, &[TWIG_LITERAL_STRING])
        .into_iter()
        .filter(|candidate| twigquery::string_in_filter(candidate, &["trans"]))
        .collect()
}

fn admin_matches(code: &str) -> Vec<swlsp_syntax::SyntaxNode> {
    let root = swlsp_parser_twig::parse(code).root();
    twigquery::string_arguments_in_functions(&root, &["$tc", "$t"])
}

#[test]
fn snippet_diagnostics_pattern_detection() {
    let cases: &[(&str, usize, usize)] = &[
        ("{{ 'checkout.cart.title'|trans }}", 1, 0),
        ("{{ $tc('sw-settings.title') }}", 0, 1),
        ("{{ $t('global.save') }}", 0, 1),
        ("{{ 'first.key'|trans }} {{ 'second.key'|trans }}", 2, 0),
        ("{{ $tc('first.key') }} {{ $t('second.key') }}", 0, 2),
        ("{{ 'just text' }}", 0, 0),
        (
            "{% block content %}{{ $tc('block.snippet') }}{% endblock %}",
            0,
            1,
        ),
        (
            "{% block header %}\n{{ $tc('header.title') }}\n{% endblock %}\n{% block content %}\n{{ $tc('content.text') }}\n{% endblock %}",
            0,
            2,
        ),
    ];
    for (code, frontend, admin) in cases {
        assert_eq!(frontend_matches(code).len(), *frontend, "frontend: {code}");
        assert_eq!(admin_matches(code).len(), *admin, "admin: {code}");
    }
}

fn snippet_index_with_admin_known(root: &Path) -> Arc<SnippetIndexer> {
    let index = Arc::new(SnippetIndexer::new(root.join("cache"), None).unwrap());
    index_file(
        index.as_ref(),
        &root
            .join("src/Resources/app/administration/src/module/demo/snippet/en-GB.json")
            .to_string_lossy(),
        r#"{"admin":{"known":"Known"}}"#,
    );
    index
}

#[test]
fn admin_snippet_diagnostics_cover_vue_bound_twig_attributes() {
    let root = tempdir();
    let index = snippet_index_with_admin_known(root.path());
    let source = "<div\n    :title=\"$t('admin.known')\"\n    :label=\"$tc('admin.missing')\"\n    title=\"$t('admin.plain-attribute')\"\n/>";
    let document = doc(
        "file:///project/src/Resources/app/administration/view.html.twig",
        source,
    );
    let problems = analyze(&SnippetAnalyzer::new(Some(index), None), &document);
    assert_eq!(problems.len(), 1, "{problems:?}");
    assert_eq!(problems[0].id.as_str(), "admin.snippet.missing");
    assert_eq!(range_text(&document, problems[0].range), "admin.missing");
}

#[test]
fn admin_snippet_diagnostics_cover_java_script_translator_forms() {
    let root = tempdir();
    let index = snippet_index_with_admin_known(root.path());
    let source = "
translator.$t('admin.known');
this.$root.$tc('admin.missing-root');
Shopware.Snippet.t('admin.missing-service');
other.t('admin.not-a-translation');
this.$t(dynamicKey);
Module.register('demo', {
    title: 'admin.known',
    description: 'admin.missing-module',
    navigation: [{ label: 'admin.known' }],
    routes: { index: { meta: { label: 'admin.not-a-module-label' } } },
});
";
    let document = doc(
        "file:///project/src/Resources/app/administration/index.ts",
        source,
    );
    let problems = analyze(&SnippetAnalyzer::new(Some(index), None), &document);
    assert_eq!(problems.len(), 3, "{problems:?}");
    assert_eq!(
        range_text(&document, problems[0].range),
        "'admin.missing-root'"
    );
    assert_eq!(
        range_text(&document, problems[1].range),
        "'admin.missing-service'"
    );
    assert_eq!(
        range_text(&document, problems[2].range),
        "'admin.missing-module'"
    );
}

#[test]
fn snippet_diagnostics_admin_file_detection() {
    let cases = [
        (
            "/project/custom/plugins/MyPlugin/src/Resources/app/administration/src/module/my-module/component/my-component/my-component.html.twig",
            true,
        ),
        (
            "/project/custom/plugins/MyPlugin/src/Resources/views/storefront/page/checkout/cart.html.twig",
            false,
        ),
        (
            "/project/vendor/shopware/storefront/Resources/views/storefront/base.html.twig",
            false,
        ),
        (
            "/project/vendor/shopware/administration/Resources/app/administration/src/module/sw-product/page/sw-product-list/sw-product-list.html.twig",
            true,
        ),
    ];
    for (uri, expected) in cases {
        assert_eq!(
            uri.contains("/Resources/app/administration/"),
            expected,
            "{uri}"
        );
    }
}

#[test]
fn snippet_key_extraction() {
    let cases = [
        ("{{ 'checkout.cart.title'|trans }}", "checkout.cart.title"),
        (
            "{{ $tc('sw-settings.index.title') }}",
            "sw-settings.index.title",
        ),
        (
            "{{ $tc('sw-product.count', { count: 5 }) }}",
            "sw-product.count",
        ),
        (
            "{{ 'checkout.items'|trans({'%count%': count}) }}",
            "checkout.items",
        ),
    ];
    for (code, expected) in cases {
        let mut matches = frontend_matches(code);
        if matches.is_empty() {
            matches = admin_matches(code);
        }
        assert!(!matches.is_empty(), "{code}");
        assert_eq!(
            twigquery::string_value(&matches[0]).unwrap_or_default(),
            expected,
            "{code}"
        );
    }
}
