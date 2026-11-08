use super::types::PAGE;
use super::*;
use crate::twig::TwigCompletionProvider;

fn by_insert_text(
    items: &[protocol::CompletionItem],
    insert_text: &str,
) -> protocol::CompletionItem {
    items
        .iter()
        .find(|item| item.insert_text == insert_text)
        .cloned()
        .unwrap_or_else(|| panic!("{insert_text:?} not found"))
}

#[test]
fn twig_callable_completions_mark_deprecation() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/src/AppExtension.php",
        r#"<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;
class AppExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [
            new TwigFunction('legacy_function', $this->legacy(...)),
            new TwigFunction('active_function', $this->active(...)),
        ];
    }
    public function getFilters(): array {
        return [
            new TwigFilter('legacy_filter', $this->legacyFilter(...)),
            new TwigFilter('active_filter', $this->activeFilter(...)),
        ];
    }
    public function legacy(): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_function" Twig function is deprecated. Use active_function instead.',
        );
        return '';
    }
    public function active(): string { return ''; }
    public function legacyFilter(string $value): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_filter" Twig filter is deprecated. Use active_filter instead.',
        );
        return $value;
    }
    public function activeFilter(string $value): string { return $value; }
}"#,
    );
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, None);

    let source = "{{ leg }}";
    let functions = complete(
        &provider,
        &twig_completion_at(PAGE, source, source.find("leg").unwrap() + 3),
    );
    let legacy = by_insert_text(&functions, "legacy_function($0)");
    assert!(legacy.deprecated);
    assert_eq!(legacy.detail, "Deprecated Twig function");
    assert!(documentation(&legacy).contains("active_function"));
    assert!(!by_insert_text(&functions, "active_function($0)").deprecated);

    let source = "{{ value|leg }}";
    let filters = complete(
        &provider,
        &twig_completion_at(PAGE, source, source.find("leg").unwrap() + 3),
    );
    let legacy = by_insert_text(&filters, "legacy_filter($0)");
    assert!(legacy.deprecated);
    assert_eq!(legacy.detail, "Deprecated Twig filter");
    assert!(documentation(&legacy).contains("active_filter"));
    assert!(!by_insert_text(&filters, "active_filter($0)").deprecated);
}
