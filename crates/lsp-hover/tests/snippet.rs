mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::{SnippetHoverProvider, extract_locale_from_path};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_snippet::SnippetIndexer;

#[test]
fn extract_locale_from_path_cases() {
    for (path, expected) in [
        (
            "src/Storefront/Resources/snippet/de_DE/storefront.de-DE.json",
            "de-DE",
        ),
        (
            "src/Storefront/Resources/snippet/en_GB/storefront.en_GB.json",
            "en-GB",
        ),
        ("src/Core/Resources/snippet/de-DE/messages.json", "de-DE"),
        ("src/Core/Resources/snippet/de_DE/messages.json", "de-DE"),
        (
            "vendor/shopware/core/Resources/snippet/en_GB/storefront.json",
            "en-GB",
        ),
        ("src/Resources/snippet/de/messages.json", "de"),
        ("src/Resources/snippet/translations.de.json", "de"),
        ("src/Resources/translations/messages.json", "unknown"),
        ("src/Resources/snippet/de_DE/storefront.en-GB.json", "en-GB"),
        (
            "src\\Storefront\\Resources\\snippet\\de_DE\\storefront.json",
            "de-DE",
        ),
        (
            "src\\Storefront\\Resources\\snippet\\translations\\storefront.de-DE.json",
            "de-DE",
        ),
        ("src/Resources/snippet/DE_DE/messages.json", "DE-DE"),
        ("src/snippet/storefront.frontend.de-DE.min.json", "de-DE"),
        ("de-DE/messages.json", "de-DE"),
        (
            "vendor/shopware/platform/src/Storefront/Resources/snippet/de_DE/storefront.json",
            "de-DE",
        ),
    ] {
        assert_eq!(extract_locale_from_path(path), expected, "{path}");
    }
}

#[test]
fn snippet_hover_pattern_matching() {
    for (code, expect_frontend, expect_admin) in [
        ("{{ 'product.detail.title'|trans }}", true, false),
        ("{{ $tc('sw-product.detail.title') }}", false, true),
        ("{{ $t('global.button.save') }}", false, true),
        ("{{ 'just text' }}", false, false),
    ] {
        let parse = swlsp_parser_twig::parse(code);
        let root = parse.tree.root();
        let strings = twigquery::nodes(&root, &[TWIG_LITERAL_STRING]);
        assert!(!strings.is_empty(), "could not find string node");
        assert_eq!(
            twigquery::string_in_filter(&strings[0], &["trans"]),
            expect_frontend,
            "frontend pattern for: {code}"
        );
        assert_eq!(
            twigquery::string_in_function(&strings[0], &["$tc", "$t"]),
            expect_admin,
            "admin pattern for: {code}"
        );
    }
}

fn snippet_index(root: &std::path::Path) -> Arc<SnippetIndexer> {
    let index = Arc::new(SnippetIndexer::new(root.join("cache"), None).unwrap());
    index_file(
        index.as_ref(),
        &p(&root.join("src/Resources/app/administration/src/module/demo/snippet/en-GB.json")),
        r#"{"admin":{"settings":{"title":"Settings"}}}"#,
    );
    index
}

#[test]
fn admin_snippet_hover_inside_vue_bound_twig_attribute() {
    let root = tempfile::tempdir().unwrap();
    let index = snippet_index(root.path());
    let source = r#"<mt-button :label="$t('admin.settings.title')" />"#;
    let document = TextDocument::new(
        "file:///project/src/Resources/app/administration/view.html.twig",
        source,
        1,
    );
    let request = request_at(document, source.find("admin.settings.title").unwrap() + 3);
    let result = hover(&SnippetHoverProvider::new(p(root.path()), index), &request).expect("hover");
    assert!(result.contents.value.contains("admin.settings.title"));
    assert!(result.contents.value.contains("Settings"));
}

#[test]
fn admin_snippet_hover_in_javascript_references() {
    let root = tempfile::tempdir().unwrap();
    let index = snippet_index(root.path());
    for source in [
        "this.$root.$t('admin.settings.title')",
        "Module.register('demo', {
            navigation: [{ label: 'admin.settings.title' }],
        });",
    ] {
        let document = TextDocument::new(
            "file:///project/src/Resources/app/administration/index.js",
            source,
            1,
        );
        let request = request_at(document, source.find("admin.settings.title").unwrap() + 3);
        let result = hover(
            &SnippetHoverProvider::new(p(root.path()), index.clone()),
            &request,
        )
        .expect("hover");
        assert!(result.contents.value.contains("admin.settings.title"));
        assert!(result.contents.value.contains("Settings"));
    }
}
