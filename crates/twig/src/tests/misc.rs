use crate::parser::calculate_block_hash;
use crate::path::bundle_name_by_path;
use crate::*;

fn caret(source: &str) -> (String, u32) {
    let offset = source.find("<caret>").expect("caret");
    (source.replacen("<caret>", "", 1), offset as u32)
}

#[test]
fn convert_to_relative_path_cases() {
    assert_eq!(convert_to_relative_path(""), "");
    assert_eq!(convert_to_relative_path("/"), "");
    assert_eq!(convert_to_relative_path("/Resources/views"), "");
    assert_eq!(convert_to_relative_path("/Resources/views/"), "");
    assert_eq!(
        convert_to_relative_path("/Resources/views/storefront/base.html.twig"),
        "@Storefront/storefront/base.html.twig"
    );
}

#[test]
fn bundle_name_by_path_cases() {
    assert_eq!(
        bundle_name_by_path("foo/Resources/views/storefront/base.html.twig"),
        "foo"
    );
    assert_eq!(
        bundle_name_by_path("vendor/shopware/storefront/Resources/views/storefront/base.html.twig"),
        "storefront"
    );
    assert_eq!(
        bundle_name_by_path(
            "vendor/store.shopware.com/MyFoo/src/Resources/views/storefront/base.html.twig"
        ),
        "MyFoo"
    );
}

#[test]
fn template_names_cases() {
    assert_eq!(
        template_names("/project/templates/base.html.twig"),
        ["base.html.twig"]
    );
    assert_eq!(
        template_names("/project/MyBundle/src/Resources/views/card.html.twig"),
        [
            "card.html.twig",
            "@Storefront/card.html.twig",
            "@MyBundle/card.html.twig",
            "MyBundle::card.html.twig"
        ]
    );
}

#[test]
fn template_names_for_storefront_dist() {
    assert_eq!(
        template_names(
            "/project/src/Storefront/Resources/app/storefront/dist/assets/icon/solid/arrow-medium-right.svg"
        ),
        ["@Storefront/assets/icon/solid/arrow-medium-right.svg"]
    );
    assert_eq!(
        template_names(
            "/project/custom/plugins/MyPlugin/src/Resources/app/storefront/dist/assets/icon/check.svg"
        ),
        ["@MyPlugin/assets/icon/check.svg"]
    );
    assert_eq!(
        template_names(
            "/project/custom/apps/MyApp/Resources/app/storefront/dist/assets/icon/check.svg"
        ),
        ["@MyApp/assets/icon/check.svg"]
    );
    assert_eq!(
        convert_to_relative_path(
            "/project/src/Storefront/Resources/app/storefront/dist/assets/icon/solid/arrow-medium-right.svg"
        ),
        "@Storefront/assets/icon/solid/arrow-medium-right.svg"
    );
}

#[test]
fn template_names_outside_template_roots() {
    assert!(
        template_names("/Users/example/project/custom/apps/MyApp/Resources/scripts/ping/ping.twig")
            .is_empty()
    );
    assert!(template_names("/project/page.html.twig").is_empty());
}

#[test]
fn template_asset_paths() {
    assert!(is_template_asset_path(
        "/project/src/Core/Profiling/Resources/views/Collector/checkmark.svg"
    ));
    assert!(is_template_asset_path("/project/templates/mail/logo.svg"));
    assert!(is_template_asset_path(
        "/project/src/Storefront/Resources/app/storefront/dist/assets/icon/check.svg"
    ));
    assert!(!is_template_asset_path(
        "/project/src/Storefront/Resources/app/storefront/dist/storefront/page.html.twig"
    ));
    assert!(!is_template_asset_path(
        "/project/src/Storefront/Resources/views/storefront/base.html.twig"
    ));
    assert!(!is_template_asset_path(
        "/project/public/bundles/storefront/logo.svg"
    ));
    assert!(!is_template_asset_path(
        "/project/src/Administration/Resources/app/administration/src/icon.svg"
    ));
    assert!(!is_template_asset_path("/project/templates/.gitignore"));
    assert!(!is_template_asset_path(
        "/project/src/Storefront/Resources/views/components/node_modules"
    ));
}

#[test]
fn block_hash_is_stable() {
    let content = "{% block test %}\n    <p>Hello World</p>\n{% endblock %}";
    let hash = calculate_block_hash(content);
    assert!(!hash.is_empty());
    assert_eq!(hash, calculate_block_hash(content));
    assert_ne!(
        hash,
        calculate_block_hash("{% block test %}\n    <p>Different content</p>\n{% endblock %}")
    );
}

#[test]
fn version_comments() {
    let valid =
        parse_version_comment("{# shopware-block: abc123def456@6.4.15.0 #}", 10).expect("valid");
    assert_eq!(
        (valid.hash.as_str(), valid.version.as_str(), valid.line),
        ("abc123def456", "6.4.15.0", 10)
    );
    assert!(parse_version_comment("{# just a regular comment #}", 5).is_none());
    let bare = parse_version_comment("{# shopware-block: abc123 #}", 8).expect("bare");
    assert_eq!(
        (bare.hash.as_str(), bare.version.as_str(), bare.line),
        ("abc123", "", 8)
    );
    assert!(parse_version_comment("{# shopware-block: not-a-hash@6.7.0.0 #}", 9).is_none());
}

#[test]
fn format_version_comment_omits_unknown_version() {
    assert_eq!(
        format_version_comment("abc123", ""),
        "{# shopware-block: abc123 #}\n"
    );
    assert_eq!(
        format_version_comment("abc123", "v6.7.2.0"),
        "{# shopware-block: abc123@v6.7.2.0 #}\n"
    );
}

#[test]
fn hash_compatibility_with_phpstorm() {
    for (content, expected) in [
        (
            "{% block test %}\n    <p>Hello World</p>\n{% endblock %}",
            "86eec44546f994424c37efba9b8f58be1ec94259be09302891e4a681c4b02918",
        ),
        (
            "{% block test_block %}content{% endblock %}",
            "851bf4d9e13400b18923d9428b8f2d681f6f50b2d2f5c9bf0944ef09aa44a8d1",
        ),
        (
            "{% block test_block %}\n    <p>Hello World</p>\n{% endblock %}",
            "fa4dc0f67b4ec5acf55b934657a181b9de40d3b5435e5b04f8ece858a9e8bff4",
        ),
    ] {
        assert_eq!(calculate_block_hash(content), expected);
    }
}

#[test]
fn see_references_support_explicit_and_legacy_targets() {
    let source = r"{# @see App\Controller\ProductController::show #}
{# @see @Storefront/storefront/page/product.html.twig #}
{# \DateTime:format #}
{# ordinary prose #}
{% raw %}{# @see App\Ignored #}{% endraw %}";
    let root = swlsp_parser_twig::parse(source).root();
    let references = see_references(&root);
    assert_eq!(references.len(), 3);
    assert_eq!(
        references[0].target,
        r"App\Controller\ProductController::show"
    );
    assert_eq!(
        references[1].target,
        "@Storefront/storefront/page/product.html.twig"
    );
    assert_eq!(references[2].target, r"\DateTime:format");
    for reference in &references {
        assert_eq!(reference.target, &source[reference.range]);
    }
}

#[test]
fn see_completion_at_returns_exact_target_range() {
    let (source, offset) = caret(r"{# @see App\Controller\Prod<caret>uctController #}");
    let root = swlsp_parser_twig::parse(&source).root();
    let context = see_completion_at(&root, offset).expect("context");
    assert_eq!(context.prefix, r"App\Controller\Prod");
    assert_eq!(&source[context.range], r"App\Controller\ProductController");
}

#[test]
fn guard_completion_at_cases() {
    let cases: [(&str, bool, bool, GuardKind, &str); 8] = [
        ("{% guard <caret> %}", true, false, GuardKind::None, ""),
        (
            "{%- guard fun<caret> %}",
            true,
            false,
            GuardKind::None,
            "fun",
        ),
        (
            "{% guard\nfunction <caret>%}",
            true,
            true,
            GuardKind::Function,
            "",
        ),
        (
            "{% guard filter upp<caret> %}",
            true,
            true,
            GuardKind::Filter,
            "upp",
        ),
        (
            "{% guard test ev<caret> %}",
            true,
            true,
            GuardKind::Test,
            "ev",
        ),
        (
            "{# {% guard function <caret> #}",
            false,
            false,
            GuardKind::None,
            "",
        ),
        (
            "{% raw %}{% guard function <caret>",
            false,
            false,
            GuardKind::None,
            "",
        ),
        (
            "{% guard function importmap <caret>%}",
            false,
            false,
            GuardKind::None,
            "",
        ),
    ];
    for (with_caret, found, callable, kind, prefix) in cases {
        let (source, offset) = caret(with_caret);
        let context = guard_completion_at(source.as_bytes(), offset);
        assert_eq!(context.is_some(), found, "{with_caret}");
        let Some(context) = context else { continue };
        assert_eq!(context.callable, callable, "{with_caret}");
        assert_eq!(context.kind, kind, "{with_caret}");
        assert_eq!(context.prefix, prefix, "{with_caret}");
    }
}

#[test]
fn guard_reference_at_cases() {
    let source = "{% guard function importmap %}
{% guard filter upper %}
{% guard test even %}
{# {% guard function hidden_comment %} #}
{% raw %}{% guard filter hidden_raw %}{% endraw %}";
    for (needle, kind) in [
        ("importmap", GuardKind::Function),
        ("upper", GuardKind::Filter),
        ("even", GuardKind::Test),
    ] {
        let offset = source.find(needle).unwrap() as u32 + 2;
        let reference = guard_reference_at(source.as_bytes(), offset).expect(needle);
        assert_eq!(reference.kind, kind);
        assert_eq!(reference.name, needle);
        assert_eq!(&source[reference.range], needle);
    }
    for needle in ["hidden_comment", "hidden_raw"] {
        let offset = source.find(needle).unwrap() as u32 + 2;
        assert!(
            guard_reference_at(source.as_bytes(), offset).is_none(),
            "{needle}"
        );
    }
}

/// The memoized `*_at` lookups must answer exactly like a fresh document
/// scan, for every offset and with several trees alternating in the memo.
#[test]
fn memoized_lookups_match_fresh_scans() {
    let twig_sources = [
        "{# @see App\\Foo::bar #}{% if a is defined and b is not empty %}{% import 'm.html.twig' as m %}{{ m.box(1) }}{% endif %}{% macro box(x) %}{{ x is same as(1) }}{% endmacro %}",
        "{# App\\Legacy #}\n{{ value is odd ? 1 : 2 }}{% raw %}{# @see Hidden #}{% endraw %}{{ dump(x) }}",
    ];
    let php_source = "<?php\nclass C extends AbstractController {\n  function a() {\n    $this->renderBlock('page.html.twig', 'content', []);\n    return $this->renderBlockView(view: '@Storefront/a.html.twig', block: 'b');\n  }\n}\n";
    let twig_roots: Vec<_> = twig_sources
        .iter()
        .map(|source| swlsp_parser_twig::parse(source).tree.root())
        .collect();
    let php_root = swlsp_parser_php::parse(php_source).tree.root();
    for _ in 0..2 {
        for (source, root) in twig_sources.iter().zip(&twig_roots) {
            for offset in 0..=source.len() as u32 {
                let see = see_references(root).into_iter().find(|reference| {
                    offset >= u32::from(reference.range.start())
                        && offset <= u32::from(reference.range.end())
                });
                assert_eq!(see_reference_at(root, offset), see);
                let test = test_expressions(root).into_iter().find(|expression| {
                    expression.range.contains(offset.into())
                        || offset == u32::from(expression.range.end())
                });
                assert_eq!(test_expression_at(root, offset), test);
                let path = "/project/templates/a.html.twig";
                let reference =
                    macro_references_in_document(path, root)
                        .into_iter()
                        .find(|reference| {
                            rowan::TextSize::from(offset) >= reference.range.start()
                                && rowan::TextSize::from(offset) <= reference.range.end()
                        });
                assert_eq!(macro_reference_at(path, root, offset), reference);
                let usage = extension_usages_in_document(path, root)
                    .into_iter()
                    .find(|usage| {
                        offset >= u32::from(usage.range.start())
                            && offset <= u32::from(usage.range.end())
                    });
                assert_eq!(extension_usage_at(path, root, offset), usage);
            }
        }
        let mut found = 0;
        for offset in 0..=php_source.len() as u32 {
            let expected =
                render_block_references_in_php(&php_root)
                    .into_iter()
                    .find(|reference| {
                        offset >= u32::from(reference.block_range.start())
                            && offset <= u32::from(reference.block_range.end())
                    });
            found += usize::from(expected.is_some());
            assert_eq!(render_block_reference_at(&php_root, offset), expected);
            let references = php_template_references("/project/C.php", &php_root);
            assert_eq!(
                references,
                crate::reference::php_template_references_uncached("/project/C.php", &php_root)
            );
        }
        assert!(found > 0);
    }
}
