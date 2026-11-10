use swlsp_lsp_protocol::CompletionItemKind;
use tempfile::TempDir;

use super::*;
use crate::completion::twig::TwigCompletionProvider;

const URI: &str = "file:///project/templates/operator.html.twig";

fn operator_fixture() -> (TwigCompletionProvider, TempDir) {
    let dir = tmp();
    let twig = twig_index(&dir);
    index(
        twig.as_ref(),
        "/project/src/AppExtension.php",
        r#"<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getOperators(): array
    {
        return [[], ['legacy-or' => []]];
    }

    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(Node::class, 'b-custom', 18),
            new BinaryOperatorExpressionParser(Node::class, 'elvis', 5, aliases: ['? :']),
            new UnaryOperatorExpressionParser(Node::class, 'expression_not', 70),
        ];
    }
}"#,
    );
    (
        TwigCompletionProvider::new("/project", Some(twig), None, None),
        dir,
    )
}

#[test]
fn twig_operator_completion_includes_legacy_modern_and_aliases() {
    let (provider, _dir) = operator_fixture();
    let items = caret_items(&provider, URI, "{% if value <caret> %}");
    let names = labels(&items);
    for label in [
        "**",
        "-",
        "ends with",
        "not",
        "or",
        "starts with",
        "legacy-or",
        "b-custom",
        "expression_not",
        "? :",
    ] {
        assert!(names.contains(&label.to_string()), "{label}");
    }
    let alias = require_completion(&items, "? :");
    assert_eq!(alias.kind, Some(CompletionItemKind::OPERATOR));
    assert_eq!(alias.detail, "Custom Twig binary operator alias");
    assert!(documentation(&alias).contains("AppExtension.php"));
    assert_eq!(
        require_completion(&items, "expression_not").detail,
        "Custom Twig unary operator"
    );
}

#[test]
fn twig_operator_completion_can_leave_builtins_to_host_ide() {
    let (provider, _dir) = operator_fixture();
    let provider = provider.without_builtin_twig_completions();
    let names = labels(&caret_items(&provider, URI, "{% if value <caret> %}"));
    assert!(!names.contains(&"or".to_string()));
    assert!(!names.contains(&"starts with".to_string()));
    assert!(names.contains(&"legacy-or".to_string()));
    assert!(names.contains(&"b-custom".to_string()));
}

#[test]
fn twig_operator_completion_context_matches_twig_if_operands() {
    let (provider, _dir) = operator_fixture();
    for (name, source, found) in [
        ("after operand", "{% if foo <caret> %}", true),
        ("after not operand", "{% if not foo <caret> %}", true),
        (
            "after malformed leading operator and operand",
            "{% if and foo <caret> %}",
            true,
        ),
        (
            "after complete complex expression",
            "{% if foo is red and blue <caret> %}",
            true,
        ),
        (
            "operator fragment after operand",
            "{% if foo fa<caret>ke %}",
            true,
        ),
        ("before first operand", "{% if <caret>foo %}", false),
        (
            "inside first operand after not",
            "{% if not fa<caret>ke %}",
            false,
        ),
        ("twig test position", "{% if foo is <caret> %}", false),
        ("output expression", "{{ foo <caret> }}", false),
        ("other statement", "{% set foo = value <caret> %}", false),
    ] {
        let items = caret_items(&provider, URI, source);
        assert_eq!(
            found,
            labels(&items).contains(&"b-custom".to_string()),
            "{name}"
        );
    }
}
