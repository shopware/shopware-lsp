use super::types::PAGE;
use super::*;
use crate::completion::twig::TwigCompletionProvider;

#[test]
fn twig_guard_completion_types_and_callables() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/src/AppExtension.php",
        r#"<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getFunctions(): array
    {
        return [new \Twig\TwigFunction('asset_url', $this->asset(...))];
    }

    public function getFilters(): array
    {
        return [new \Twig\TwigFilter('money', $this->money(...))];
    }

    public function getTests(): array
    {
        return [new \Twig\TwigTest('positive', $this->positive(...))];
    }

    public function asset(string $path): string { return $path; }
    public function money(int $value): string { return (string) $value; }
    public function positive(int $value): bool { return $value > 0; }
}"#,
    );
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, None);

    let mut types = labels(&caret_items(&provider, PAGE, "{% guard <caret> %}"));
    types.sort();
    assert_eq!(types, ["filter", "function", "test"]);
    assert_eq!(
        labels(&caret_items(&provider, PAGE, "{% guard fun<caret> %}")),
        ["function"]
    );

    for (source, expected, excluded) in [
        (
            "{% guard function <caret> %}",
            "asset_url",
            ["money", "positive"],
        ),
        (
            "{% guard filter mon<caret> %}",
            "money",
            ["asset_url", "positive"],
        ),
        (
            "{% guard test <caret> %}",
            "positive",
            ["asset_url", "money"],
        ),
    ] {
        let items = caret_items(&provider, PAGE, source);
        require_completion(&items, expected);
        let names = labels(&items);
        for excluded in excluded {
            assert!(!names.contains(&excluded.to_string()), "{source}");
        }
    }
}

#[test]
fn twig_guard_completion_ignores_comments_and_raw_bodies() {
    let dir = tmp();
    let provider = TwigCompletionProvider::new("/project", Some(twig_index(&dir)), None, None);
    for source in [
        "{# {% guard <caret> #}",
        "{% raw %}{% guard function <caret>",
        "{% guard function complete <caret>%}",
    ] {
        assert!(caret_items(&provider, PAGE, source).is_empty(), "{source}");
    }
}
