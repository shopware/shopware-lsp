use swlsp_lsp_protocol::CompletionItemKind;
use tempfile::TempDir;

use super::types::PAGE;
use super::*;
use crate::completion::twig::TwigCompletionProvider;

fn tag_index() -> (Arc<TwigIndexer>, TempDir) {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/src/LegacyTokenParser.php",
        r#"<?php
/** @deprecated Use the modern tag instead. */
class LegacyTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}
class ModernTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'modern'; }
}"#,
    );
    (twig, dir)
}

fn at_end(provider: &TwigCompletionProvider, source: &str) -> Vec<protocol::CompletionItem> {
    complete(provider, &twig_completion_at(PAGE, source, source.len()))
}

#[test]
fn twig_custom_tag_completion() {
    let (twig, _dir) = tag_index();
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, None);

    let legacy = require_completion(&at_end(&provider, "{% leg"), "legacy");
    assert_eq!(legacy.kind, Some(CompletionItemKind::KEYWORD));
    assert!(legacy.deprecated);
    assert!(documentation(&legacy).contains("modern"));

    let types = require_completion(&at_end(&provider, "{% typ"), "types");
    assert_eq!(types.kind, Some(CompletionItemKind::SNIPPET));

    let closing_items = at_end(&provider, "{%- endtra");
    let closing = require_completion(&closing_items, "endtrans");
    assert!(!closing.deprecated);
    assert!(!labels(&closing_items).contains(&"endlegacy".to_string()));

    require_completion(&at_end(&provider, "{% en"), "endtrans");
}

#[test]
fn twig_custom_tag_completion_respects_active_duplicate() {
    let (twig, _dir) = tag_index();
    index(
        twig.as_ref(),
        "/project/src/OverrideTokenParser.php",
        r#"<?php
class OverrideTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}"#,
    );
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, None);
    let item = require_completion(&at_end(&provider, "{% legacy"), "legacy");
    assert!(!item.deprecated);
}

#[test]
fn twig_custom_tag_completion_skips_verbatim_bodies() {
    let (twig, _dir) = tag_index();
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, None);
    for source in [
        "{% raw %}{% leg",
        "{% verbatim %}{% leg",
        "{% set source = \"{% leg",
        "{# {% leg",
    ] {
        assert!(
            !labels(&at_end(&provider, source)).contains(&"legacy".to_string()),
            "{source}"
        );
    }
}
