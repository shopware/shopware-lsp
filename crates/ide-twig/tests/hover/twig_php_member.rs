use std::sync::Arc;

use crate::common::*;
use swlsp_ide_twig::hover::TwigHoverProvider;
use swlsp_lsp::TextDocument;
use swlsp_lsp_protocol as protocol;
use swlsp_twig::TwigIndexer;

fn hover_range_text(source: &str, document: &TextDocument, range: protocol::Range) -> String {
    let lines = document.line_index();
    let start = lines.offset_utf16(range.start.line, range.start.character) as usize;
    let end = lines.offset_utf16(range.end.line, range.end.character) as usize;
    source[start..end].to_owned()
}

fn member_hover(class: &str, source: &str, needle: &str) -> (protocol::Hover, String) {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        &cache.path().join("php"),
        &[("/project/src/Bar.php", class)],
    );
    let twig = Arc::new(TwigIndexer::new(cache.path().join("twig"), None).unwrap());
    let uri = "file:///project/templates/page.html.twig";
    let request = request_at(
        TextDocument::new(uri, source, 1),
        source.rfind(needle).unwrap() + 1,
    );
    let result = hover(
        &TwigHoverProvider::new("/project", None, Some(php), Some(twig)),
        &request,
    )
    .expect("hover");
    let text = hover_range_text(
        source,
        &TextDocument::new(uri, source, 1),
        result.range.expect("range"),
    );
    (result, text)
}

#[test]
fn twig_php_member_hover_shows_deprecation() {
    let (result, text) = member_hover(
        r"<?php
namespace Foo;
class Bar {
    /**
     * Legacy accessor.
     *
     * @deprecated
     */
    public function getDeprecated(): static { return $this; }
}",
        r"{# @var bar \Foo\Bar #} {{ bar.deprecated }}",
        "deprecated",
    );
    let value = &result.contents.value;
    assert!(value.contains("Foo\\Bar::getDeprecated"));
    assert!(value.contains("Legacy accessor"));
    assert!(value.contains("**Deprecated**"));
    assert_eq!(text, "deprecated");
}

#[test]
fn twig_php_class_constant_hover() {
    let (result, text) = member_hover(
        r"<?php
namespace Foo;
class Bar {
    /** The model kind. */
    public const KIND = 'bar';
}",
        r"{# @var bar \Foo\Bar #} {{ bar.KIND }}",
        "KIND",
    );
    let value = &result.contents.value;
    assert!(value.contains("Foo\\Bar::KIND"));
    assert!(value.contains("The model kind"));
    assert_eq!(text, "KIND");
}
