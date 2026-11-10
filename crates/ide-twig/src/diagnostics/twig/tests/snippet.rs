use std::sync::Arc;

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_snippet::SnippetIndexer;

use super::*;
use crate::diagnostics::twig::SnippetAnalyzer;

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
    check_problems(
        &SnippetAnalyzer::new(Some(index), None),
        "file:///project/src/Resources/app/administration/view.html.twig",
        "<div\n    :title=\"$t('admin.known')\"\n    :label=\"$tc('{|admin.snippet.missing:admin.missing|}')\"\n    title=\"$t('admin.plain-attribute')\"\n/>",
    );
}

#[test]
fn admin_snippet_diagnostics_cover_java_script_translator_forms() {
    let root = tempdir();
    let index = snippet_index_with_admin_known(root.path());
    let fixture = "
translator.$t('admin.known');
this.$root.$tc({|admin.snippet.missing:'admin.missing-root'|});
Shopware.Snippet.t({|admin.snippet.missing:'admin.missing-service'|});
other.t('admin.not-a-translation');
this.$t(dynamicKey);
Module.register('demo', {
    title: 'admin.known',
    description: {|admin.snippet.missing:'admin.missing-module'|},
    navigation: [{ label: 'admin.known' }],
    routes: { index: { meta: { label: 'admin.not-a-module-label' } } },
});
";
    check_problems(
        &SnippetAnalyzer::new(Some(index), None),
        "file:///project/src/Resources/app/administration/index.ts",
        fixture,
    );
}

#[test]
fn computed_admin_keys_are_not_reported() {
    let root = tempdir();
    let index = snippet_index_with_admin_known(root.path());
    check_problems(
        &SnippetAnalyzer::new(Some(index.clone()), None),
        "file:///project/src/Resources/app/administration/view.html.twig",
        "<div\n    :title=\"$t('global.default.' + variant)\"\n    :label=\"$tc('admin.known', 2)\"\n    :aria-label=\"$t('{|admin.snippet.missing:admin.missing|}' )\"\n/>",
    );
    check_problems(
        &SnippetAnalyzer::new(Some(index.clone()), None),
        "file:///project/src/Resources/app/administration/index.js",
        "
this.$t('sw-shortcut.save' + this.platformSuffix);
this.$t(
    'sw-shortcut.clear' +
        suffix,
);
this.$t({|admin.snippet.missing:'admin.missing'|}, { count: 1 });
Module.register('demo', {
    description: 'The module for creating content.',
    navigation: [{ label: 'plain navigation text' }],
});
",
    );
    // Test input is not shipped code.
    check_problems(
        &SnippetAnalyzer::new(Some(index), None),
        "file:///project/src/Resources/app/administration/scripts/__fixtures__/legacy/index.js",
        "this.$t('legacy.missing');",
    );
}

#[test]
fn computed_storefront_keys_are_not_reported() {
    let root = tempdir();
    let index = snippet_index_with_admin_known(root.path());
    check_problems(
        &SnippetAnalyzer::new(Some(index), None),
        "file:///project/src/Resources/views/storefront/page.html.twig",
        "{{ ('cookie.' ~ feature ~ '.title')|trans }}\n\
         {{ \"detail.review#{point}PointRatingText\"|trans }}\n\
         {{ {|frontend.snippet.missing:'static.missing'|}|trans }}",
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
