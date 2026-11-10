use super::types::PAGE;
use super::*;
use crate::completion::twig::TwigCompletionProvider;

#[test]
fn twig_test_completion_includes_builtins_and_indexed_tests() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/src/AppExtension.php",
        r#"<?php
use Twig\Attribute\AsTwigTest;
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getTests(): array
    {
        return [
            new \Twig\TwigTest('positive', $this->positive(...)),
            new \Twig\TwigTest('legacy_test', $this->legacy(...), [
                'deprecated' => '3.0',
            ]),
        ];
    }
    public function positive(int $value): bool { return $value > 0; }
    public function legacy(int $value): bool { return false; }

    #[AsTwigTest('uuid')]
    public function uuid(string $value): bool { return true; }
}"#,
    );
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, None);
    let items = caret_items(&provider, PAGE, "{% if value is <caret> %}");
    let names = labels(&items);
    for label in ["defined", "same as", "positive", "uuid"] {
        assert!(names.contains(&label.to_string()), "{label}");
    }
    assert_eq!(
        labels(&caret_items(&provider, PAGE, "{{ value is po<caret> }}")),
        ["positive"]
    );
    assert_eq!(
        labels(&caret_items(
            &provider,
            PAGE,
            "{% set valid = value is not uu<caret> %}"
        )),
        ["uuid"]
    );
    let deprecated = require_completion(&items, "legacy_test");
    assert!(deprecated.deprecated);
    assert_eq!(deprecated.detail, "Deprecated Twig test");
}

#[test]
fn twig_test_completion_can_leave_builtins_to_host_ide() {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/src/AppExtension.php",
        r#"<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getTests(): array
    {
        return [new \Twig\TwigTest('positive', $this->positive(...))];
    }
}"#,
    );
    let provider = TwigCompletionProvider::new("/project", Some(twig), None, None)
        .without_builtin_twig_completions();
    let names = labels(&caret_items(&provider, PAGE, "{% if value is <caret> %}"));
    assert!(!names.contains(&"defined".to_string()));
    assert!(!names.contains(&"same as".to_string()));
    assert!(names.contains(&"positive".to_string()));
}

#[test]
fn twig_test_completion_does_not_leak_into_other_expressions() {
    let dir = tmp();
    let provider = TwigCompletionProvider::new("/project", Some(twig_index(&dir)), None, None);
    for source in [
        "{% if <caret>value %}",
        "{% if value <caret> %}",
        "{{ value <caret> }}",
        "{# value is <caret> #}",
        "{% if value is positive <caret> %}",
    ] {
        let names = labels(&caret_items(&provider, PAGE, source));
        assert!(!names.contains(&"defined".to_string()), "{source}");
        assert!(!names.contains(&"same as".to_string()), "{source}");
    }
}
