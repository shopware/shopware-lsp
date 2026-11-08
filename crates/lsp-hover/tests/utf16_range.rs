//! Port of Go `internal/lsp/hover/utf16_range_test.go`.
//!
//! The prefixes put a two-byte (ä) and a four-byte, two-UTF-16-unit (😀)
//! character before the hovered name, so byte and UTF-16 columns differ. The
//! cursor sits inside the name; the range must still cover exactly the name,
//! in UTF-16 units.

mod common;

use std::sync::Arc;

use common::*;
use swlsp_admin::AdminComponentIndexer;
use swlsp_extension::ExtensionIndexer;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::{AdminHoverProvider, SnippetHoverProvider, TwigHoverProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_snippet::SnippetIndexer;

fn range_text(document_source: &str, range: protocol::Range) -> String {
    let index = swlsp_syntax::LineIndex::new(document_source);
    let start = index.offset_utf16(range.start.line, range.start.character) as usize;
    let end = index.offset_utf16(range.end.line, range.end.character) as usize;
    document_source[start..end].to_owned()
}

fn assert_utf16_range(
    hover: &protocol::Hover,
    source: &str,
    name: &str,
    start: u32,
    end: u32,
    case: &str,
) {
    let range = hover.range.unwrap_or_else(|| panic!("{case}: no range"));
    assert_eq!(
        range,
        protocol::Range {
            start: protocol::Position {
                line: 0,
                character: start
            },
            end: protocol::Position {
                line: 0,
                character: end
            },
        },
        "{case}"
    );
    assert_eq!(range_text(source, range), name, "{case}");
}

fn hover_at(
    provider: &dyn swlsp_lsp::HoverProvider,
    uri: &str,
    source: &str,
    name: &str,
) -> protocol::Hover {
    let document = TextDocument::new(uri, source, 1);
    let request = request_at_position(document, source.find(name).unwrap() + 3);
    hover(provider, &request).unwrap_or_else(|| panic!("no hover for {source}"))
}

#[test]
fn snippet_hover_range_uses_utf16_on_non_ascii_lines() {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(SnippetIndexer::new(root.path().join("cache"), None).unwrap());
    index_file(
        index.as_ref(),
        &p(&root
            .path()
            .join("src/Resources/app/administration/src/module/demo/snippet/en-GB.json")),
        r#"{"admin":{"settings":{"title":"Settings"}}}"#,
    );
    index_file(
        index.as_ref(),
        &p(&root
            .path()
            .join("src/Resources/snippet/en_GB/storefront.en-GB.json")),
        r#"{"product":{"title":"Title"}}"#,
    );
    let provider = SnippetHoverProvider::new(p(root.path()), index);
    for (case, uri, source, key, start, end) in [
        (
            "storefront twig trans",
            "file:///project/src/Resources/views/page.html.twig",
            "{# ä😀 #} {{ 'product.title'|trans }}",
            "product.title",
            14,
            27,
        ),
        (
            "administration twig $tc",
            "file:///project/src/Resources/app/administration/view.html.twig",
            "{# ä😀 #} {{ $tc('admin.settings.title') }}",
            "admin.settings.title",
            18,
            38,
        ),
        (
            "php trans",
            "file:///project/src/Controller/PageController.php",
            "<?php /* ä😀 */ $translator->trans('product.title');",
            "product.title",
            36,
            49,
        ),
        (
            "administration javascript",
            "file:///project/src/Resources/app/administration/index.js",
            "/* ä😀 */ this.$root.$t('admin.settings.title')",
            "admin.settings.title",
            25,
            45,
        ),
    ] {
        let result = hover_at(&provider, uri, source, key);
        assert_utf16_range(&result, source, key, start, end, case);
    }
}

#[test]
fn twig_icon_hover_range_uses_utf16_on_non_ascii_lines() {
    let root = tempfile::tempdir().unwrap();
    write(
        &root.path().join(
            "src/Storefront/Resources/app/storefront/dist/assets/icon/default/arrow-down.svg",
        ),
        "<svg/>",
    );
    let extensions = Arc::new(ExtensionIndexer::new(root.path().join("cache"), None).unwrap());
    let provider = TwigHoverProvider::new(p(root.path()), Some(extensions), None, None);
    let source = "{# ä😀 #} {% sw_icon 'arrow-down' %}";
    let result = hover_at(
        &provider,
        &uri(&root.path().join("src/Resources/views/page.html.twig")),
        source,
        "arrow-down",
    );
    assert!(value(&result).contains("**Icon**: `arrow-down`"));
    assert_utf16_range(&result, source, "arrow-down", 22, 32, "sw_icon");
}

#[test]
fn admin_component_hover_range_uses_utf16_on_non_ascii_lines() {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(AdminComponentIndexer::new(&p(&root.path().join("cache")), None).unwrap());
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    index_file(
        index.as_ref(),
        &p(&admin_root.join("main.ts")),
        "Shopware.Component.register('sw-card', { props: { title: String } });",
    );
    let provider = AdminHoverProvider::new(p(root.path()), index);
    for (case, file, source, start, end) in [
        (
            "twig tag",
            "consumer.html.twig",
            "{# ä😀 #} <sw-card></sw-card>",
            11,
            18,
        ),
        (
            "javascript registry",
            "consumer.ts",
            "/* ä😀 */ Shopware.Component.getComponentRegistry().get('sw-card')",
            57,
            64,
        ),
    ] {
        let result = hover_at(&provider, &uri(&admin_root.join(file)), source, "sw-card");
        assert!(value(&result).contains("`sw-card`"), "{case}");
        assert_utf16_range(&result, source, "sw-card", start, end, case);
    }
}
